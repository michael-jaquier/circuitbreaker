package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/michael-jaquier/circuitbreaker"
)

// APIClient wraps an HTTP client with circuit breaker protection
// This example shows how to protect HTTP API calls from cascading failures
type APIClient struct {
	baseURL string
	breaker circuitbreaker.CircuitBreaker
	client  *http.Client
}

// APIClientConfig configures the API client
type APIClientConfig struct {
	BaseURL        string
	Timeout        time.Duration
	CooldownTimer  time.Duration
	SuccessToClose int64
}

// NewAPIClient creates a new circuit-breaker protected HTTP client
// The circuit breaker will:
// - Open on any 5xx server error (500-599)
// - Allow 4xx client errors through (not the server's fault)
// - Close after SuccessToClose consecutive successful requests
func NewAPIClient(config APIClientConfig) (*APIClient, error) {
	cb, err := circuitbreaker.NewZeroTolerance(
		circuitbreaker.WithCooldownTimer(config.CooldownTimer),
		circuitbreaker.WithSuccessToClose(config.SuccessToClose),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create circuit breaker: %w", err)
	}

	return &APIClient{
		baseURL: config.BaseURL,
		breaker: cb,
		client:  &http.Client{Timeout: config.Timeout},
	}, nil
}

// Get performs a GET request with circuit breaker protection
func (a *APIClient) Get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", a.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	var resp *http.Response
	var httpErr error

	timer, execErr := a.breaker.Execute(ctx, func(ctx context.Context) error {
		resp, httpErr = a.client.Do(req)
		if httpErr != nil {
			return httpErr
		}

		// Check for 5xx errors (based on config)
		if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
			return fmt.Errorf("server error: %d", resp.StatusCode)
		}
		return nil
	})

	if timer != nil {
		return nil, fmt.Errorf("circuit breaker is open, service unavailable")
	}
	if execErr != nil {
		return nil, fmt.Errorf("request failed: %w", execErr)
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// Post performs a POST request with circuit breaker protection
func (a *APIClient) Post(ctx context.Context, path string, payload interface{}) ([]byte, error) {
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", a.baseURL+path, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	var resp *http.Response
	var httpErr error

	timer, execErr := a.breaker.Execute(ctx, func(ctx context.Context) error {
		resp, httpErr = a.client.Do(req)
		if httpErr != nil {
			return httpErr
		}

		// Check for 5xx errors (based on config)
		if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
			return fmt.Errorf("server error: %d", resp.StatusCode)
		}
		return nil
	})

	if timer != nil {
		return nil, fmt.Errorf("circuit breaker is open")
	}
	if execErr != nil {
		return nil, fmt.Errorf("request failed: %w", execErr)
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// GetBlocking performs a GET request with automatic retry on circuit open
// This method blocks until success or context cancellation
func (a *APIClient) GetBlocking(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", a.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	var resp *http.Response
	var httpErr error

	execErr := a.breaker.ExecuteBlocking(ctx, func(ctx context.Context) error {
		resp, httpErr = a.client.Do(req)
		if httpErr != nil {
			return httpErr
		}

		// Check for 5xx errors (based on config)
		if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
			return fmt.Errorf("server error: %d", resp.StatusCode)
		}
		return nil
	})

	if execErr != nil {
		return nil, fmt.Errorf("request failed: %w", execErr)
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

func main() {
	// Example 1: Basic usage with real API
	client, err := NewAPIClient(APIClientConfig{
		BaseURL:        "https://jsonplaceholder.typicode.com",
		Timeout:        10 * time.Second,
		CooldownTimer:  60 * time.Second,
		SuccessToClose: 3,
	})
	if err != nil {
		panic(err)
	}

	ctx := context.Background()

	// Successful GET request
	fmt.Println("=== Example 1: Successful GET request ===")
	data, err := client.Get(ctx, "/posts/1")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	} else {
		fmt.Printf("Success! Received %d bytes\n", len(data))
		fmt.Printf("Response preview: %.100s...\n", string(data))
		fmt.Println()
	}

	// Successful POST request
	fmt.Println("=== Example 2: Successful POST request ===")
	payload := map[string]interface{}{
		"title":  "Circuit Breaker Example",
		"body":   "This demonstrates circuit breaker with HTTP POST",
		"userId": 1,
	}
	data, err = client.Post(ctx, "/posts", payload)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	} else {
		fmt.Printf("Success! Received %d bytes\n", len(data))
		fmt.Printf("Response preview: %.100s...\n", string(data))
		fmt.Println()
	}

	// Example with 404 (client error, doesn't open circuit)
	fmt.Println("=== Example 3: 404 Not Found (client error) ===")
	data, err = client.Get(ctx, "/posts/999999")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		fmt.Println("Note: 404 is a client error, circuit remains closed")
		fmt.Println()
	}

	// Example with invalid URL (network error, opens circuit)
	fmt.Println("=== Example 4: Network error (opens circuit) ===")
	badClient, _ := NewAPIClient(APIClientConfig{
		BaseURL:        "http://localhost:99999", // Invalid port
		Timeout:        2 * time.Second,
		CooldownTimer:  5 * time.Second,
		SuccessToClose: 3,
	})

	_, err = badClient.Get(ctx, "/test")
	if err != nil {
		fmt.Printf("First request error: %v\n", err)
	}

	// Immediate retry should be blocked by circuit breaker
	_, err = badClient.Get(ctx, "/test")
	if err != nil {
		fmt.Printf("Second request error: %v\n", err)
		if strings.Contains(err.Error(), "circuit breaker is open") {
			fmt.Println("Circuit breaker successfully blocked the request!")
			fmt.Println()
		}
	}

	// Example with ExecuteBlocking (automatic retry on circuit open)
	fmt.Println("=== Example 5: ExecuteBlocking (automatic retry) ===")
	blockingClient, _ := NewAPIClient(APIClientConfig{
		BaseURL:        "https://jsonplaceholder.typicode.com",
		Timeout:        10 * time.Second,
		CooldownTimer:  3 * time.Second,
		SuccessToClose: 1,
	})

	// First request succeeds
	data, err = blockingClient.GetBlocking(ctx, "/posts/1")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	} else {
		fmt.Printf("Success! Received %d bytes\n", len(data))
	}

	// Simulate circuit opening with bad request
	badBlockingClient, _ := NewAPIClient(APIClientConfig{
		BaseURL:        "http://localhost:99999",
		Timeout:        1 * time.Second,
		CooldownTimer:  2 * time.Second,
		SuccessToClose: 1,
	})

	// Open the circuit
	_, _ = badBlockingClient.Get(ctx, "/test")

	// ExecuteBlocking will wait for circuit to become half-open
	fmt.Println("Calling GetBlocking (will wait for circuit to open)...")
	ctxWithTimeout, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	_, err = badBlockingClient.GetBlocking(ctxWithTimeout, "/test")
	if err != nil {
		if strings.Contains(err.Error(), "context deadline") {
			fmt.Println("Request cancelled after timeout (circuit was waiting)")
		} else {
			fmt.Printf("Request failed: %v\n", err)
		}
	}
	fmt.Println()

	fmt.Println("=== Circuit Breaker Integration Complete ===")
	fmt.Println("Key takeaways:")
	fmt.Println("1. Circuit opens on network errors and 5xx server errors")
	fmt.Println("2. 4xx client errors don't open the circuit")
	fmt.Println("3. Execute() fails fast with timer when circuit is open")
	fmt.Println("4. ExecuteBlocking() automatically waits when circuit is open")
	fmt.Println("5. Circuit recovers after cooldown period with successful probes")
}
