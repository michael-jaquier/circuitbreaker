package circuitbreaker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHttpRequest_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	}))
	defer server.Close()

	fakeClock := &FakeClock{now: time.Now()}
	cb, err := NewZeroTolerance(WithClock(fakeClock))
	if err != nil {
		t.Fatalf("Failed to create circuit breaker: %v", err)
	}

	req, _ := http.NewRequest("GET", server.URL, nil)

	var resp *http.Response
	var httpErr error
	ztcb := cb.(*circuitBreaker)

	timer, err := cb.Execute(req.Context(), func(ctx context.Context) error {
		client := &http.Client{Timeout: 10 * time.Second}
		resp, httpErr = client.Do(req)
		if httpErr != nil {
			return httpErr
		}

		if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
			return fmt.Errorf("HTTP error: status %d", resp.StatusCode)
		}
		return nil
	})

	if err != nil || httpErr != nil {
		t.Errorf("Expected no error, got err=%v, httpErr=%v", err, httpErr)
	}
	if timer != nil {
		t.Error("Expected timer to be nil (circuit not open)")
	}
	if resp == nil {
		t.Fatal("Expected response, got nil")
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	if State(ztcb.state.Load()) != Closed {
		t.Errorf("Circuit should remain closed after success, got %v", State(ztcb.state.Load()))
	}
}

func TestHttpRequest_4xxError(t *testing.T) {
	testCases := []struct {
		name       string
		statusCode int
	}{
		{"400 Bad Request", http.StatusBadRequest},
		{"401 Unauthorized", http.StatusUnauthorized},
		{"403 Forbidden", http.StatusForbidden},
		{"404 Not Found", http.StatusNotFound},
		{"429 Too Many Requests", http.StatusTooManyRequests},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
			}))
			defer server.Close()

			fakeClock := &FakeClock{now: time.Now()}
			cb, err := NewZeroTolerance(WithClock(fakeClock))
			if err != nil {
				t.Fatalf("Failed to create circuit breaker: %v", err)
			}

			req, _ := http.NewRequest("GET", server.URL, nil)

			var resp *http.Response
			var httpErr error
			ztcb := cb.(*circuitBreaker)

			_, err = cb.Execute(req.Context(), func(ctx context.Context) error {
				client := &http.Client{Timeout: 10 * time.Second}
				resp, httpErr = client.Do(req)
				if httpErr != nil {
					return httpErr
				}

				if resp.StatusCode >= 400 {
					return fmt.Errorf("HTTP error: status %d", resp.StatusCode)
				}
				return nil
			})

			// 4xx errors should trigger the circuit to open when we check >= 400
			if err == nil {
				t.Errorf("Expected error for %d status, got nil", tc.statusCode)
			}
			if resp == nil {
				t.Fatal("Expected response, got nil")
			}
			if resp.StatusCode != tc.statusCode {
				t.Errorf("Expected status %d, got %d", tc.statusCode, resp.StatusCode)
			}

			if State(ztcb.state.Load()) != Open {
				t.Errorf("Circuit should open after %d error (zero tolerance), got %v", tc.statusCode, State(ztcb.state.Load()))
			}
		})
	}
}

func TestHttpRequest_5xxError(t *testing.T) {
	testCases := []struct {
		name       string
		statusCode int
	}{
		{"500 Internal Server Error", http.StatusInternalServerError},
		{"502 Bad Gateway", http.StatusBadGateway},
		{"503 Service Unavailable", http.StatusServiceUnavailable},
		{"504 Gateway Timeout", http.StatusGatewayTimeout},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
			}))
			defer server.Close()

			fakeClock := &FakeClock{now: time.Now()}
			cb, err := NewZeroTolerance(WithClock(fakeClock))
			if err != nil {
				t.Fatalf("Failed to create circuit breaker: %v", err)
			}

			req, _ := http.NewRequest("GET", server.URL, nil)

			var resp *http.Response
			var httpErr error
			ztcb := cb.(*circuitBreaker)

			_, err = cb.Execute(req.Context(), func(ctx context.Context) error {
				client := &http.Client{Timeout: 10 * time.Second}
				resp, httpErr = client.Do(req)
				if httpErr != nil {
					return httpErr
				}

				if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
					return fmt.Errorf("HTTP error: status %d", resp.StatusCode)
				}
				return nil
			})

			if err == nil {
				t.Errorf("Expected error for %d status, got nil", tc.statusCode)
			}
			if resp == nil {
				t.Fatal("Expected response, got nil")
			}
			if resp.StatusCode != tc.statusCode {
				t.Errorf("Expected status %d, got %d", tc.statusCode, resp.StatusCode)
			}

			if State(ztcb.state.Load()) != Open {
				t.Errorf("Circuit should open after %d error (zero tolerance), got %v", tc.statusCode, State(ztcb.state.Load()))
			}
		})
	}
}

func TestHttpRequest_NetworkError(t *testing.T) {
	fakeClock := &FakeClock{now: time.Now()}
	cb, err := NewZeroTolerance(WithClock(fakeClock))
	if err != nil {
		t.Fatalf("Failed to create circuit breaker: %v", err)
	}

	req, _ := http.NewRequest("GET", "http://localhost:99999", nil)

	var resp *http.Response
	var httpErr error
	ztcb := cb.(*circuitBreaker)

	_, err = cb.Execute(req.Context(), func(ctx context.Context) error {
		client := &http.Client{Timeout: 10 * time.Second}
		resp, httpErr = client.Do(req)
		if httpErr != nil {
			return httpErr
		}

		if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
			return fmt.Errorf("HTTP error: status %d", resp.StatusCode)
		}
		return nil
	})

	if err == nil {
		t.Error("Expected network error, got nil")
	}
	if resp != nil {
		t.Errorf("Expected nil response on network error, got %v", resp)
	}

	if State(ztcb.state.Load()) != Open {
		t.Errorf("Circuit should open after network error, got %v", State(ztcb.state.Load()))
	}
}

func TestHttpRequest_CircuitOpenBlocksRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	fakeClock := &FakeClock{now: time.Now()}
	cb, err := NewZeroTolerance(WithClock(fakeClock))
	if err != nil {
		t.Fatalf("Failed to create circuit breaker: %v", err)
	}

	// Open the circuit with a failure
	cb.Execute(context.Background(), func(ctx context.Context) error {
		return errors.New("simulated failure")
	})

	ztcb := cb.(*circuitBreaker)
	if State(ztcb.state.Load()) != Open {
		t.Fatal("Circuit should be open")
	}

	req, _ := http.NewRequest("GET", server.URL, nil)

	var resp *http.Response
	var httpErr error

	timer, err := cb.Execute(req.Context(), func(ctx context.Context) error {
		client := &http.Client{Timeout: 10 * time.Second}
		resp, httpErr = client.Do(req)
		if httpErr != nil {
			return httpErr
		}

		if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
			return fmt.Errorf("HTTP error: status %d", resp.StatusCode)
		}
		return nil
	})

	if timer == nil {
		t.Error("Expected timer when circuit is open")
	}
	if err != nil {
		t.Errorf("Expected nil error when circuit is open, got %v", err)
	}
	if resp != nil {
		t.Errorf("Expected nil response when circuit is open, got %v", resp)
	}
}

func TestHttpRequest_HalfOpenProbeSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	fakeClock := &FakeClock{now: time.Now()}
	cb, err := NewZeroTolerance(WithClock(fakeClock))
	if err != nil {
		t.Fatalf("Failed to create circuit breaker: %v", err)
	}

	// Open the circuit
	cb.Execute(context.Background(), func(ctx context.Context) error {
		return errors.New("simulated failure")
	})
	// Advance past cooldown to enable lazy Open→HalfOpen transition
	fakeClock.Advance(121 * time.Second)

	ztcb := cb.(*circuitBreaker)

	req, _ := http.NewRequest("GET", server.URL, nil)

	for i := 0; i < int(ztcb.config.successToClose); i++ {
		var resp *http.Response
		var httpErr error

		timer, err := cb.Execute(req.Context(), func(ctx context.Context) error {
			client := &http.Client{Timeout: 10 * time.Second}
			resp, httpErr = client.Do(req)
			if httpErr != nil {
				return httpErr
			}

			if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
				return fmt.Errorf("HTTP error: status %d", resp.StatusCode)
			}
			return nil
		})

		if err != nil || httpErr != nil {
			t.Fatalf("Request %d failed: err=%v, httpErr=%v", i+1, err, httpErr)
		}
		if timer != nil {
			t.Fatalf("Request %d: circuit unexpectedly open", i+1)
		}
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Request %d: expected 200, got %d", i+1, resp.StatusCode)
		}
	}

	if State(ztcb.state.Load()) != Closed {
		t.Errorf("Circuit should be closed after %d successes, got %v", ztcb.config.successToClose, State(ztcb.state.Load()))
	}
}

func TestHttpRequest_HalfOpenProbeFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	fakeClock := &FakeClock{now: time.Now()}
	cb, err := NewZeroTolerance(WithClock(fakeClock))
	if err != nil {
		t.Fatalf("Failed to create circuit breaker: %v", err)
	}

	// Open the circuit
	cb.Execute(context.Background(), func(ctx context.Context) error {
		return errors.New("simulated failure")
	})
	// Advance past cooldown to enable lazy Open→HalfOpen transition
	fakeClock.Advance(121 * time.Second)

	ztcb := cb.(*circuitBreaker)

	req, _ := http.NewRequest("GET", server.URL, nil)

	var resp *http.Response
	var httpErr error

	_, err = cb.Execute(req.Context(), func(ctx context.Context) error {
		client := &http.Client{Timeout: 10 * time.Second}
		resp, httpErr = client.Do(req)
		if httpErr != nil {
			return httpErr
		}

		if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
			return fmt.Errorf("HTTP error: status %d", resp.StatusCode)
		}
		return nil
	})

	if err == nil {
		t.Error("Expected error for 500 status")
	}
	if resp != nil && resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("Expected 500, got %d", resp.StatusCode)
	}

	if State(ztcb.state.Load()) != Open {
		t.Errorf("Circuit should reopen after failure in half-open, got %v", State(ztcb.state.Load()))
	}
}

func TestHttpRequest_CustomHttpErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	fakeClock := &FakeClock{now: time.Now()}

	cb, err := NewZeroTolerance(
		WithClock(fakeClock),
	)
	if err != nil {
		t.Fatalf("Failed to create circuit breaker: %v", err)
	}

	req, _ := http.NewRequest("GET", server.URL, nil)

	var resp *http.Response
	var httpErr error
	ztcb := cb.(*circuitBreaker)

	_, err = cb.Execute(req.Context(), func(ctx context.Context) error {
		client := &http.Client{Timeout: 10 * time.Second}
		resp, httpErr = client.Do(req)
		if httpErr != nil {
			return httpErr
		}

		if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
			return fmt.Errorf("HTTP error: status %d", resp.StatusCode)
		}
		return nil
	})

	if err != nil || httpErr != nil {
		t.Errorf("Expected no error with custom func that ignores 4xx, got err=%v, httpErr=%v", err, httpErr)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("Expected 404, got %d", resp.StatusCode)
	}

	if State(ztcb.state.Load()) != Closed {
		t.Errorf("Circuit should stay closed with custom error func that ignores 4xx, got %v", State(ztcb.state.Load()))
	}
}

func TestHttpRequest_3xxRedirect(t *testing.T) {
	redirectCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if redirectCount == 0 {
			redirectCount++
			http.Redirect(w, r, "/redirected", http.StatusMovedPermanently)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("redirected"))
	}))
	defer server.Close()

	fakeClock := &FakeClock{now: time.Now()}
	cb, err := NewZeroTolerance(WithClock(fakeClock))
	if err != nil {
		t.Fatalf("Failed to create circuit breaker: %v", err)
	}

	req, _ := http.NewRequest("GET", server.URL, nil)

	var resp *http.Response
	var httpErr error
	ztcb := cb.(*circuitBreaker)

	_, err = cb.Execute(req.Context(), func(ctx context.Context) error {
		client := &http.Client{Timeout: 10 * time.Second}
		resp, httpErr = client.Do(req)
		if httpErr != nil {
			return httpErr
		}

		if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
			return fmt.Errorf("HTTP error: status %d", resp.StatusCode)
		}
		return nil
	})

	if err != nil || httpErr != nil {
		t.Errorf("Expected no error, got err=%v, httpErr=%v", err, httpErr)
	}
	if resp == nil {
		t.Fatal("Expected response, got nil")
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200 after redirect, got %d", resp.StatusCode)
	}

	if State(ztcb.state.Load()) != Closed {
		t.Errorf("Circuit should remain closed after successful redirect, got %v", State(ztcb.state.Load()))
	}
}

func TestHttpRequest_2xxSuccessCodes(t *testing.T) {
	testCases := []struct {
		name       string
		statusCode int
	}{
		{"200 OK", http.StatusOK},
		{"201 Created", http.StatusCreated},
		{"202 Accepted", http.StatusAccepted},
		{"204 No Content", http.StatusNoContent},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
			}))
			defer server.Close()

			fakeClock := &FakeClock{now: time.Now()}
			cb, err := NewZeroTolerance(WithClock(fakeClock))
			if err != nil {
				t.Fatalf("Failed to create circuit breaker: %v", err)
			}

			req, _ := http.NewRequest("GET", server.URL, nil)

			var resp *http.Response
			var httpErr error
			ztcb := cb.(*circuitBreaker)

			_, err = cb.Execute(req.Context(), func(ctx context.Context) error {
				client := &http.Client{Timeout: 10 * time.Second}
				resp, httpErr = client.Do(req)
				if httpErr != nil {
					return httpErr
				}

				if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
					return fmt.Errorf("HTTP error: status %d", resp.StatusCode)
				}
				return nil
			})

			if err != nil || httpErr != nil {
				t.Errorf("Expected no error for %d status, got err=%v, httpErr=%v", tc.statusCode, err, httpErr)
			}
			if resp == nil {
				t.Fatal("Expected response, got nil")
			}
			if resp.StatusCode != tc.statusCode {
				t.Errorf("Expected status %d, got %d", tc.statusCode, resp.StatusCode)
			}

			if State(ztcb.state.Load()) != Closed {
				t.Errorf("Circuit should remain closed after %d success, got %v", tc.statusCode, State(ztcb.state.Load()))
			}
		})
	}
}
func TestExecuteHTTPBlocking_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	}))
	defer server.Close()

	cb, err := NewZeroTolerance()
	if err != nil {
		t.Fatalf("Failed to create circuit breaker: %v", err)
	}

	client := &http.Client{}
	requestFactory := func() (*http.Request, error) {
		return http.NewRequest("GET", server.URL, nil)
	}

	ctx := context.Background()
	resp, err := cb.ExecuteHTTPBlocking(ctx, client, requestFactory)

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if resp == nil {
		t.Fatal("Expected response, got nil")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "success" {
		t.Errorf("Expected body 'success', got %s", string(body))
	}
}

func TestExecuteHTTPBlocking_Retryable5xx(t *testing.T) {
	attempt := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt++
		if attempt == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("server error"))
		} else {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("success"))
		}
	}))
	defer server.Close()

	// Use real time with short cooldown for integration test
	cb, err := NewZeroTolerance(WithCooldownTimer(100 * time.Millisecond))
	if err != nil {
		t.Fatalf("Failed to create circuit breaker: %v", err)
	}

	client := &http.Client{}
	requestFactory := func() (*http.Request, error) {
		return http.NewRequest("GET", server.URL, nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	resp, err := cb.ExecuteHTTPBlocking(ctx, client, requestFactory)

	if err != nil {
		t.Errorf("Expected no error after retry, got %v", err)
	}
	if resp == nil {
		t.Fatal("Expected response, got nil")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200 after retry, got %d", resp.StatusCode)
	}

	if attempt != 2 {
		t.Errorf("Expected 2 attempts, got %d", attempt)
	}
}

func TestExecuteHTTPBlocking_Retryable429(t *testing.T) {
	attempt := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt++
		if attempt == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte("rate limited"))
		} else {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("success"))
		}
	}))
	defer server.Close()

	cb, err := NewZeroTolerance(WithCooldownTimer(100 * time.Millisecond))
	if err != nil {
		t.Fatalf("Failed to create circuit breaker: %v", err)
	}

	client := &http.Client{}
	requestFactory := func() (*http.Request, error) {
		return http.NewRequest("GET", server.URL, nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	resp, err := cb.ExecuteHTTPBlocking(ctx, client, requestFactory)

	if err != nil {
		t.Errorf("Expected no error after retry, got %v", err)
	}
	if resp == nil {
		t.Fatal("Expected response, got nil")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200 after retry, got %d", resp.StatusCode)
	}
}

func TestExecuteHTTPBlocking_Retryable408(t *testing.T) {
	attempt := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt++
		if attempt == 1 {
			w.WriteHeader(http.StatusRequestTimeout)
			w.Write([]byte("timeout"))
		} else {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("success"))
		}
	}))
	defer server.Close()

	cb, err := NewZeroTolerance(WithCooldownTimer(100 * time.Millisecond))
	if err != nil {
		t.Fatalf("Failed to create circuit breaker: %v", err)
	}

	client := &http.Client{}
	requestFactory := func() (*http.Request, error) {
		return http.NewRequest("GET", server.URL, nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	resp, err := cb.ExecuteHTTPBlocking(ctx, client, requestFactory)

	if err != nil {
		t.Errorf("Expected no error after retry, got %v", err)
	}
	if resp == nil {
		t.Fatal("Expected response, got nil")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200 after retry, got %d", resp.StatusCode)
	}
}

func TestExecuteHTTPBlocking_NonRetryable4xx(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("not found"))
	}))
	defer server.Close()

	cb, err := NewZeroTolerance()
	if err != nil {
		t.Fatalf("Failed to create circuit breaker: %v", err)
	}

	client := &http.Client{}
	requestFactory := func() (*http.Request, error) {
		return http.NewRequest("GET", server.URL, nil)
	}

	ctx := context.Background()
	resp, err := cb.ExecuteHTTPBlocking(ctx, client, requestFactory)

	if err == nil {
		t.Error("Expected non-retryable error, got nil")
	} else if !errors.Is(err, fmt.Errorf("non-retryable HTTP error: status 404")) &&
		err.Error() != "non-retryable HTTP error: status 404" {
		t.Errorf("Expected non-retryable error, got %v", err)
	}

	if resp == nil {
		t.Error("Expected response even for 4xx, got nil")
	} else {
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("Expected status 404, got %d", resp.StatusCode)
		}
	}

	if callCount != 1 {
		t.Errorf("Expected 1 call for non-retryable error, got %d", callCount)
	}
}

func TestExecuteHTTPBlocking_OverallContextTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	// Long cooldown means circuit will be open longer than context timeout
	cb, err := NewZeroTolerance(WithCooldownTimer(5 * time.Second))
	if err != nil {
		t.Fatalf("Failed to create circuit breaker: %v", err)
	}

	client := &http.Client{}
	requestFactory := func() (*http.Request, error) {
		return http.NewRequest("GET", server.URL, nil)
	}

	// Short context timeout - will expire while waiting for circuit to close
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	resp, err := cb.ExecuteHTTPBlocking(ctx, client, requestFactory)

	if err == nil {
		t.Error("Expected context deadline exceeded error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Expected context.DeadlineExceeded, got %v", err)
	}
	if resp != nil {
		resp.Body.Close()
		t.Error("Expected nil response on context timeout")
	}
}

func TestExecuteHTTPBlocking_RequestFactoryError(t *testing.T) {
	cb, err := NewZeroTolerance()
	if err != nil {
		t.Fatalf("Failed to create circuit breaker: %v", err)
	}

	client := &http.Client{}
	expectedErr := fmt.Errorf("factory error")
	requestFactory := func() (*http.Request, error) {
		return nil, expectedErr
	}

	ctx := context.Background()
	resp, err := cb.ExecuteHTTPBlocking(ctx, client, requestFactory)

	if err == nil {
		t.Fatal("Expected error from factory, got nil")
	}
	if !errors.Is(err, expectedErr) && err.Error() != "failed to create request: factory error" {
		t.Errorf("Expected factory error, got %v", err)
	}
	if resp != nil {
		t.Error("Expected nil response on factory error")
	}
}

func TestExecuteHTTPBlocking_PostWithBody(t *testing.T) {
	receivedBodies := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedBodies = append(receivedBodies, string(body))

		if len(receivedBodies) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("success"))
		}
	}))
	defer server.Close()

	cb, err := NewZeroTolerance(WithCooldownTimer(100 * time.Millisecond))
	if err != nil {
		t.Fatalf("Failed to create circuit breaker: %v", err)
	}

	client := &http.Client{}
	expectedBody := `{"key":"value"}`
	requestFactory := func() (*http.Request, error) {
		body := strings.NewReader(expectedBody)
		req, err := http.NewRequest("POST", server.URL, body)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	resp, err := cb.ExecuteHTTPBlocking(ctx, client, requestFactory)

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if resp == nil {
		t.Fatal("Expected response, got nil")
	}
	defer resp.Body.Close()

	if len(receivedBodies) != 2 {
		t.Fatalf("Expected 2 requests, got %d", len(receivedBodies))
	}
	for i, body := range receivedBodies {
		if body != expectedBody {
			t.Errorf("Request %d: expected body %s, got %s", i+1, expectedBody, body)
		}
	}
}
