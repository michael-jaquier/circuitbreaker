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

// User represents a domain user entity
type User struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email"`
	Name     string `json:"name"`
}

// UserRepository defines the interface for user data access
// This abstraction allows for multiple implementations (HTTP, database, cache)
// and makes testing easier with mock implementations
type UserRepository interface {
	GetByID(ctx context.Context, id int) (*User, error)
	GetAll(ctx context.Context) ([]User, error)
	Create(ctx context.Context, user *User) (*User, error)
	Update(ctx context.Context, user *User) error
	Delete(ctx context.Context, id int) error
}

// httpUserRepository implements UserRepository using HTTP API
type httpUserRepository struct {
	apiURL  string
	breaker circuitbreaker.CircuitBreaker
	client  *http.Client
}

// NewHTTPUserRepository creates a new HTTP-based user repository with circuit breaker
// The circuit breaker protects against:
// - Slow or unresponsive API endpoints
// - Server errors (5xx)
// - Network failures
func NewHTTPUserRepository(apiURL string) (UserRepository, error) {
	cb, err := circuitbreaker.NewZeroTolerance(
		circuitbreaker.WithCooldownTimer(60*time.Second),
		circuitbreaker.WithSuccessToClose(3),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create circuit breaker: %w", err)
	}

	return &httpUserRepository{
		apiURL:  apiURL,
		breaker: cb,
		client:  &http.Client{Timeout: 10 * time.Second},
	}, nil
}

func (r *httpUserRepository) GetByID(ctx context.Context, id int) (*User, error) {
	url := fmt.Sprintf("%s/users/%d", r.apiURL, id)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	var resp *http.Response
	var httpErr error

	timer, execErr := r.breaker.Execute(ctx, func(ctx context.Context) error {
		resp, httpErr = r.client.Do(req)
		if httpErr != nil {
			return httpErr
		}

		if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
			return fmt.Errorf("server error: %d", resp.StatusCode)
		}
		return nil
	})

	if timer != nil {
		return nil, fmt.Errorf("user service unavailable")
	}
	if execErr != nil {
		return nil, fmt.Errorf("request failed: %w", execErr)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("user not found: %d", id)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var user User
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &user, nil
}

func (r *httpUserRepository) GetAll(ctx context.Context) ([]User, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", r.apiURL+"/users", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	var resp *http.Response
	var httpErr error

	timer, execErr := r.breaker.Execute(ctx, func(ctx context.Context) error {
		resp, httpErr = r.client.Do(req)
		if httpErr != nil {
			return httpErr
		}

		if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
			return fmt.Errorf("server error: %d", resp.StatusCode)
		}
		return nil
	})

	if timer != nil {
		return nil, fmt.Errorf("user service unavailable")
	}
	if execErr != nil {
		return nil, fmt.Errorf("request failed: %w", execErr)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var users []User
	if err := json.NewDecoder(resp.Body).Decode(&users); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return users, nil
}

func (r *httpUserRepository) Create(ctx context.Context, user *User) (*User, error) {
	jsonData, err := json.Marshal(user)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal user: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", r.apiURL+"/users", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	var resp *http.Response
	var httpErr error

	timer, execErr := r.breaker.Execute(ctx, func(ctx context.Context) error {
		resp, httpErr = r.client.Do(req)
		if httpErr != nil {
			return httpErr
		}

		if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
			return fmt.Errorf("server error: %d", resp.StatusCode)
		}
		return nil
	})

	if timer != nil {
		return nil, fmt.Errorf("user service unavailable")
	}
	if execErr != nil {
		return nil, fmt.Errorf("request failed: %w", execErr)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var created User
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &created, nil
}

func (r *httpUserRepository) Update(ctx context.Context, user *User) error {
	jsonData, err := json.Marshal(user)
	if err != nil {
		return fmt.Errorf("failed to marshal user: %w", err)
	}

	url := fmt.Sprintf("%s/users/%d", r.apiURL, user.ID)
	req, err := http.NewRequestWithContext(ctx, "PUT", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	var resp *http.Response
	var httpErr error

	timer, execErr := r.breaker.Execute(ctx, func(ctx context.Context) error {
		resp, httpErr = r.client.Do(req)
		if httpErr != nil {
			return httpErr
		}

		if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
			return fmt.Errorf("server error: %d", resp.StatusCode)
		}
		return nil
	})

	if timer != nil {
		return fmt.Errorf("user service unavailable")
	}
	if execErr != nil {
		return fmt.Errorf("request failed: %w", execErr)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

func (r *httpUserRepository) Delete(ctx context.Context, id int) error {
	url := fmt.Sprintf("%s/users/%d", r.apiURL, id)
	req, err := http.NewRequestWithContext(ctx, "DELETE", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	var resp *http.Response
	var httpErr error

	timer, execErr := r.breaker.Execute(ctx, func(ctx context.Context) error {
		resp, httpErr = r.client.Do(req)
		if httpErr != nil {
			return httpErr
		}

		if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
			return fmt.Errorf("server error: %d", resp.StatusCode)
		}
		return nil
	})

	if timer != nil {
		return fmt.Errorf("user service unavailable")
	}
	if execErr != nil {
		return fmt.Errorf("request failed: %w", execErr)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// UserService demonstrates business logic layer using the repository
type UserService struct {
	repo UserRepository
}

// NewUserService creates a new user service with the given repository
func NewUserService(repo UserRepository) *UserService {
	return &UserService{repo: repo}
}

// GetUserProfile retrieves a user profile by ID with circuit breaker protection
func (s *UserService) GetUserProfile(ctx context.Context, userID int) (*User, error) {
	user, err := s.repo.GetByID(ctx, userID)
	if err != nil {
		if strings.Contains(err.Error(), "circuit breaker is open") {
			// Circuit is open, could return cached data or default profile
			return nil, fmt.Errorf("user service temporarily unavailable, please try again later")
		}
		return nil, err
	}
	return user, nil
}

// ListUsers retrieves all users with circuit breaker protection
func (s *UserService) ListUsers(ctx context.Context) ([]User, error) {
	users, err := s.repo.GetAll(ctx)
	if err != nil {
		if strings.Contains(err.Error(), "circuit breaker is open") {
			return nil, fmt.Errorf("user service temporarily unavailable, please try again later")
		}
		return nil, err
	}
	return users, nil
}

func main() {
	// Using JSONPlaceholder as a free fake API for demonstration
	repo, err := NewHTTPUserRepository("https://jsonplaceholder.typicode.com")
	if err != nil {
		panic(fmt.Sprintf("Failed to create repository: %v", err))
	}

	service := NewUserService(repo)
	ctx := context.Background()

	// Example 1: Get single user
	fmt.Println("=== Example 1: Get user by ID ===")
	user, err := service.GetUserProfile(ctx, 1)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	} else {
		fmt.Printf("User: %+v\n", user)
	}
	fmt.Println()

	// Example 2: Get all users (limited output)
	fmt.Println("=== Example 2: List all users (first 3) ===")
	users, err := service.ListUsers(ctx)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	} else {
		for i, u := range users {
			if i >= 3 {
				fmt.Printf("... and %d more users\n", len(users)-3)
				break
			}
			fmt.Printf("User %d: %s (%s)\n", u.ID, u.Username, u.Email)
		}
	}
	fmt.Println()

	// Example 3: Create new user
	fmt.Println("=== Example 3: Create new user ===")
	newUser := &User{
		Username: "testuser",
		Email:    "test@example.com",
		Name:     "Test User",
	}
	created, err := repo.Create(ctx, newUser)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	} else {
		fmt.Printf("Created user with ID: %d\n", created.ID)
	}
	fmt.Println()

	// Example 4: Update user
	fmt.Println("=== Example 4: Update user ===")
	if created != nil {
		created.Email = "updated@example.com"
		err = repo.Update(ctx, created)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
		} else {
			fmt.Println("User updated successfully")
		}
	}
	fmt.Println()

	// Example 5: Handle 404 (not found)
	fmt.Println("=== Example 5: Handle not found (404) ===")
	_, err = repo.GetByID(ctx, 99999)
	if err != nil {
		fmt.Printf("Expected error: %v\n", err)
		fmt.Println("Note: 404 doesn't open circuit - it's a valid response")
	}
	fmt.Println()

	// Example 6: Demonstrate circuit breaker with bad endpoint
	fmt.Println("=== Example 6: Demonstrate circuit breaker ===")
	badRepo, _ := NewHTTPUserRepository("http://localhost:99999")
	badService := NewUserService(badRepo)

	_, err = badService.GetUserProfile(ctx, 1)
	if err != nil {
		fmt.Printf("First request error: %v\n", err)
	}

	// Circuit should now be open
	_, err = badService.GetUserProfile(ctx, 1)
	if err != nil {
		if strings.Contains(err.Error(), "circuit breaker is open") {
			fmt.Println("Circuit breaker successfully protecting the service!")
		} else {
			fmt.Printf("Second request error: %v\n", err)
		}
	}
	fmt.Println()

	fmt.Println("=== Repository Pattern Integration Complete ===")
	fmt.Println("Key takeaways:")
	fmt.Println("1. Repository pattern abstracts data access layer")
	fmt.Println("2. Circuit breaker protects the HTTP implementation")
	fmt.Println("3. Business logic (service layer) handles circuit open gracefully")
	fmt.Println("4. 404 errors don't open circuit - they're valid responses")
	fmt.Println("5. Pattern allows for multiple repository implementations")
}
