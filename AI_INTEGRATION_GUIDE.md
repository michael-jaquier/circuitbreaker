# AI Integration Guide for Circuit Breaker

This guide is specifically designed for AI code generation tools to integrate the circuit breaker library into Go codebases. It provides decision trees, complete working examples, templates, and best practices.

## Table of Contents

1. [Quick Decision Tree](#quick-decision-tree)
2. [Configuration Decision Matrix](#configuration-decision-matrix)
3. [Complete Working Examples](#complete-working-examples)
4. [Code Generation Templates](#code-generation-templates)
5. [Anti-Patterns](#anti-patterns)
6. [Testing Patterns](#testing-patterns)
7. [Integration Checklist](#integration-checklist)

## Quick Decision Tree

```
Q: What are you protecting?
│
├─ HTTP API Client
│  ├─ Single endpoint → Use Example 1 (HTTP Client Wrapper)
│  └─ Multiple endpoints → Use Example 5 (Service Client)
│
├─ Database Calls
│  ├─ Direct SQL → Use Example 2 (Database Wrapper)
│  └─ Repository pattern → Use Example 3 (Repository Pattern)
│
├─ gRPC Service
│  └─ Use Example 4 (gRPC Interceptor)
│
├─ Background Jobs/Workers
│  └─ Use Example 6 (Worker Pattern)
│
└─ HTTP Middleware (protecting downstream)
   └─ Use Example 7 (HTTP Middleware)

Q: How critical is the service?
│
├─ Critical (any failure is severe)
│  ├─ CooldownTimer: 30-60 seconds
│  ├─ SuccessToClose: 3
│  └─ MaximumProbes: 1
│
├─ Normal (some failures acceptable)
│  ├─ CooldownTimer: 120 seconds (default)
│  ├─ SuccessToClose: 5 (default)
│  └─ MaximumProbes: 1-2
│
└─ Non-Critical (high tolerance)
   ├─ CooldownTimer: 180-300 seconds
   ├─ SuccessToClose: 5-10
   └─ MaximumProbes: 2-3

Q: How should errors be classified (HTTP only)?
│
├─ Only 5xx errors → Check status code >= 500 in Execute callback
├─ Both 4xx and 5xx → Check status code >= 400 in Execute callback
└─ Custom logic → Implement custom status code check in Execute callback
```

## Configuration Decision Matrix

| Scenario | WindowSize | CooldownTimer | SuccessToClose | MaxProbes | Status Code Check |
|----------|------------|---------------|----------------|-----------|------------------|
| Critical Payment Service | 60s | 30s | 3 | 1 | 5xx only |
| User Profile API | 240s (default) | 120s (default) | 5 (default) | 1 | default (4xx, 5xx) |
| Analytics Service | 300s | 180s | 10 | 3 | 5xx only |
| Internal Admin API | 120s | 60s | 5 | 2 | default |
| Background Data Sync | 600s | 300s | 5 | 1 | 5xx only |
| Health Check Endpoint | 60s | 30s | 3 | 1 | default |

## Complete Working Examples

### Example 1: HTTP API Client Wrapper

Complete, production-ready HTTP client with circuit breaker protection.

```go
package httpclient

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "time"

    "github.com/michael-jaquier/circuitbreaker"
)

// APIClient wraps an HTTP client with circuit breaker protection
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

    timer, err := a.breaker.Execute(ctx, func(ctx context.Context) error {
        resp, httpErr = a.client.Do(req)
        if httpErr != nil {
            return httpErr
        }

        if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
            return fmt.Errorf("server error: %d", resp.StatusCode)
        }
        return nil
    })

    if timer != nil {
        return nil, fmt.Errorf("circuit breaker is open, service unavailable")
    }
    if err != nil {
        return nil, fmt.Errorf("request failed: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
    }

    body, err := io.ReadAll(resp.Body)
    if err != nil {
        return nil, fmt.Errorf("failed to read response: %w", err)
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

    timer, err := a.breaker.Execute(ctx, func(ctx context.Context) error {
        resp, httpErr = a.client.Do(req)
        if httpErr != nil {
            return httpErr
        }

        if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
            return fmt.Errorf("server error: %d", resp.StatusCode)
        }
        return nil
    })

    if timer != nil {
        return nil, fmt.Errorf("circuit breaker is open")
    }
    if err != nil {
        return nil, fmt.Errorf("request failed: %w", err)
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

// Example usage
func ExampleUsage() {
    client, err := NewAPIClient(APIClientConfig{
        BaseURL:        "https://api.example.com",
        Timeout:        10 * time.Second,
        CooldownTimer:  60 * time.Second,
        SuccessToClose: 3,
    })
    if err != nil {
        panic(err)
    }

    ctx := context.Background()
    data, err := client.Get(ctx, "/users/123")
    if err != nil {
        // Check if error is due to circuit breaker being open
        if strings.Contains(err.Error(), "circuit breaker is open") {
            // Circuit is open, use fallback or return error
            fmt.Println("Service unavailable, using cached data")
            return
        }
        fmt.Printf("Error: %v\n", err)
        return
    }

    fmt.Printf("Response: %s\n", data)
}
```

#### HTTP Client Best Practices

**1. Status Code Classification**

The most critical decision for HTTP clients is classifying which status codes should open the circuit. Check status codes inside the Execute callback:

```go
// Option A: Only 5xx errors open circuit (RECOMMENDED for most APIs)
// Rationale: 4xx errors are client mistakes, not server health issues
timer, err := breaker.Execute(ctx, func(ctx context.Context) error {
    resp, httpErr := client.Do(req)
    if httpErr != nil {
        return httpErr
    }

    // Only treat 5xx as errors - circuit breaker will count this as a failure
    if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
        return fmt.Errorf("server error: %d", resp.StatusCode)
    }
    return nil
})

// Option B: Both 4xx and 5xx open circuit (use sparingly)
// Only use if 4xx indicates server misconfiguration or degradation
timer, err := breaker.Execute(ctx, func(ctx context.Context) error {
    resp, httpErr := client.Do(req)
    if httpErr != nil {
        return httpErr
    }

    if resp.StatusCode >= 400 {
        return fmt.Errorf("HTTP error: %d", resp.StatusCode)
    }
    return nil
})

// Option C: Specific status codes
// Useful when only certain errors indicate service health issues
timer, err := breaker.Execute(ctx, func(ctx context.Context) error {
    resp, httpErr := client.Do(req)
    if httpErr != nil {
        return httpErr
    }

    if resp.StatusCode == 503 || resp.StatusCode == 504 {
        return fmt.Errorf("service unavailable: %d", resp.StatusCode)
    }
    return nil
})
```

**2. Timer Return Pattern**

The circuit breaker provides two execution methods:

**Execute()** - Manual timer handling:
```go
timer, err := breaker.Execute(ctx, func(ctx context.Context) error {
    // Make HTTP request
    return httpErr
})

// Check timer FIRST - it indicates circuit is open
if timer != nil {
    // Circuit is OPEN - function was NOT executed
    // The timer indicates when to retry (but don't wait on it in user-facing code)
    return fmt.Errorf("circuit breaker is open, service unavailable")
}

// Circuit is CLOSED or HALF-OPEN - function WAS executed
if err != nil {
    // Function executed but returned error (network error, 5xx, etc.)
    return fmt.Errorf("request failed: %w", err)
}

// Success! Function executed and succeeded
```

**ExecuteBlocking()** - Automatic retry on circuit open (simplified):
```go
err := breaker.ExecuteBlocking(ctx, func(ctx context.Context) error {
    // Make HTTP request
    return httpErr
})

// Automatically waits when circuit is open
// Returns immediately on success or function error
if err != nil {
    // Either function error or context cancelled
    return fmt.Errorf("request failed: %w", err)
}

// Success!
```

Use `ExecuteBlocking()` when:
- You want automatic waiting when circuit is open
- The calling code can tolerate latency from waiting
- You don't need custom fallback logic when circuit opens

Use `Execute()` when:
- You need immediate feedback that circuit is open
- You have fallback strategies (cache, default values)
- You want to avoid blocking the caller

**3. Timeout Configuration**

HTTP clients should have BOTH circuit breaker cooldown AND HTTP client timeout:

```go
client := &http.Client{
    Timeout: 10 * time.Second,  // HTTP request timeout
}

cb, _ := circuitbreaker.NewZeroTolerance(
    circuitbreaker.WithCooldownTimer(60 * time.Second),  // Circuit breaker cooldown
)

// The HTTP timeout (10s) should be SHORTER than the cooldown (60s)
// This ensures failed requests timeout quickly before circuit opens
```

**4. Context Propagation**

Always use `http.NewRequestWithContext` to respect context cancellation:

```go
req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
if err != nil {
    return nil, err
}

timer, err := breaker.Execute(ctx, func(ctx context.Context) error {
    // The context from Execute() is the same as outer ctx
    // It will be canceled if circuit breaker needs to stop
    resp, err := client.Do(req)
    return err
})
```

**5. Response Body Handling**

```go
timer, err := breaker.Execute(ctx, func(ctx context.Context) error {
    resp, httpErr = client.Do(req)
    if httpErr != nil {
        return httpErr
    }

    // Check status inside Execute() so circuit knows about errors
    if resp.StatusCode >= 500 {
        return fmt.Errorf("server error: %d", resp.StatusCode)
    }
    return nil
})

if timer != nil {
    return nil, errors.New("circuit breaker is open")
}
if err != nil {
    return nil, err
}

// IMPORTANT: resp.Body.Close() happens OUTSIDE Execute()
// The circuit breaker records the result before we read the body
defer resp.Body.Close()
body, err := io.ReadAll(resp.Body)
```

### Example 2: Database Connection Wrapper

Protecting database queries with circuit breaker.

```go
package database

import (
    "context"
    "database/sql"
    "fmt"
    "time"

    "github.com/michael-jaquier/circuitbreaker"
)

// DB wraps a database connection with circuit breaker protection
type DB struct {
    db      *sql.DB
    breaker circuitbreaker.CircuitBreaker
}

// NewDB creates a new circuit-breaker protected database connection
func NewDB(db *sql.DB) (*DB, error) {
    // Configure circuit breaker for database calls
    cb, err := circuitbreaker.NewZeroTolerance(
        circuitbreaker.WithCooldownTimer(30 * time.Second),
        circuitbreaker.WithSuccessToClose(5),
        circuitbreaker.WithWindowSize(120 * time.Second),
    )
    if err != nil {
        return nil, fmt.Errorf("failed to create circuit breaker: %w", err)
    }

    return &DB{
        db:      db,
        breaker: cb,
    }, nil
}

// QueryRow executes a query with circuit breaker protection
func (d *DB) QueryRow(ctx context.Context, query string, args ...interface{}) (*sql.Row, error) {
    var row *sql.Row
    var queryErr error

    timer, err := d.breaker.Execute(ctx, func(ctx context.Context) error {
        row = d.db.QueryRowContext(ctx, query, args...)

        // Check for immediate errors (connection issues)
        var testScan interface{}
        queryErr = row.Scan(&testScan)
        if queryErr != nil && queryErr != sql.ErrNoRows {
            return queryErr
        }
        return nil
    })

    if timer != nil {
        return nil, fmt.Errorf("circuit breaker is open")
    }
    if err != nil {
        return nil, err
    }

    // Return a fresh query since we consumed the first one
    return d.db.QueryRowContext(ctx, query, args...), nil
}

// Query executes a query that returns multiple rows
func (d *DB) Query(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
    var rows *sql.Rows
    var queryErr error

    timer, err := d.breaker.Execute(ctx, func(ctx context.Context) error {
        rows, queryErr = d.db.QueryContext(ctx, query, args...)
        return queryErr
    })

    if timer != nil {
        return nil, fmt.Errorf("circuit breaker is open")
    }
    if err != nil {
        return nil, err
    }

    return rows, nil
}

// Exec executes a query without returning rows
func (d *DB) Exec(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
    var result sql.Result
    var execErr error

    timer, err := d.breaker.Execute(ctx, func(ctx context.Context) error {
        result, execErr = d.db.ExecContext(ctx, query, args...)
        return execErr
    })

    if timer != nil {
        return nil, fmt.Errorf("circuit breaker is open")
    }
    if err != nil {
        return nil, err
    }

    return result, nil
}

// Example usage
func ExampleDatabaseUsage() {
    rawDB, err := sql.Open("postgres", "connection_string")
    if err != nil {
        panic(err)
    }
    defer rawDB.Close()

    db, err := NewDB(rawDB)
    if err != nil {
        panic(err)
    }

    ctx := context.Background()
    var username string
    err = db.QueryRow(ctx, "SELECT username FROM users WHERE id = $1", 123).Scan(&username)
    if err != nil {
        if timer != nil {
            fmt.Println("Database circuit breaker open, using cache")
            return
        }
        fmt.Printf("Query failed: %v\n", err)
        return
    }

    fmt.Printf("Username: %s\n", username)
}
```

### Example 3: Repository Pattern with Circuit Breaker

Domain-driven design repository with circuit breaker.

```go
package repository

import (
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "time"

    "github.com/michael-jaquier/circuitbreaker"
)

// User represents a domain user entity
type User struct {
    ID       string `json:"id"`
    Username string `json:"username"`
    Email    string `json:"email"`
}

// UserRepository defines the interface for user data access
type UserRepository interface {
    GetByID(ctx context.Context, id string) (*User, error)
    Create(ctx context.Context, user *User) error
    Update(ctx context.Context, user *User) error
}

// httpUserRepository implements UserRepository using HTTP API
type httpUserRepository struct {
    apiURL  string
    breaker circuitbreaker.CircuitBreaker
    client  *http.Client
}

// NewHTTPUserRepository creates a new HTTP-based user repository
func NewHTTPUserRepository(apiURL string) (UserRepository, error) {
    cb, err := circuitbreaker.NewZeroTolerance(
        circuitbreaker.WithCooldownTimer(60 * time.Second),
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

func (r *httpUserRepository) GetByID(ctx context.Context, id string) (*User, error) {
    url := fmt.Sprintf("%s/users/%s", r.apiURL, id)
    req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
    if err != nil {
        return nil, fmt.Errorf("failed to create request: %w", err)
    }

    var resp *http.Response
    var httpErr error

    timer, err := r.breaker.Execute(ctx, func(ctx context.Context) error {
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
        return nil, fmt.Errorf("user service unavailable, circuit breaker is open")
    }
    if err != nil {
        return nil, fmt.Errorf("request failed: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode == http.StatusNotFound {
        return nil, fmt.Errorf("user not found: %s", id)
    }

    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
    }

    var user User
    if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
        return nil, fmt.Errorf("failed to decode response: %w", err)
    }

    return &user, nil
}

func (r *httpUserRepository) Create(ctx context.Context, user *User) error {
    jsonData, err := json.Marshal(user)
    if err != nil {
        return fmt.Errorf("failed to marshal user: %w", err)
    }

    req, err := http.NewRequestWithContext(ctx, "POST", r.apiURL+"/users", bytes.NewBuffer(jsonData))
    if err != nil {
        return fmt.Errorf("failed to create request: %w", err)
    }
    req.Header.Set("Content-Type", "application/json")

    var resp *http.Response
    var httpErr error

    timer, err := r.breaker.Execute(ctx, func(ctx context.Context) error {
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
        return fmt.Errorf("user service unavailable, circuit breaker is open")
    }
    if err != nil {
        return fmt.Errorf("request failed: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusCreated {
        return fmt.Errorf("unexpected status: %d", resp.StatusCode)
    }

    return nil
}

func (r *httpUserRepository) Update(ctx context.Context, user *User) error {
    jsonData, err := json.Marshal(user)
    if err != nil {
        return fmt.Errorf("failed to marshal user: %w", err)
    }

    url := fmt.Sprintf("%s/users/%s", r.apiURL, user.ID)
    req, err := http.NewRequestWithContext(ctx, "PUT", url, bytes.NewBuffer(jsonData))
    if err != nil {
        return fmt.Errorf("failed to create request: %w", err)
    }
    req.Header.Set("Content-Type", "application/json")

    var resp *http.Response
    var httpErr error

    timer, err := r.breaker.Execute(ctx, func(ctx context.Context) error {
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
        return fmt.Errorf("user service unavailable, circuit breaker is open")
    }
    if err != nil {
        return fmt.Errorf("request failed: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return fmt.Errorf("unexpected status: %d", resp.StatusCode)
    }

    return nil
}

// Example usage
func ExampleRepositoryUsage() {
    repo, err := NewHTTPUserRepository("https://api.example.com")
    if err != nil {
        panic(err)
    }

    ctx := context.Background()
    user, err := repo.GetByID(ctx, "user-123")
    if err != nil {
        if timer != nil {
            fmt.Println("User service circuit breaker open")
            return
        }
        fmt.Printf("Failed to get user: %v\n", err)
        return
    }

    fmt.Printf("User: %+v\n", user)
}
```

### Example 4: gRPC Service Protection

Circuit breaker for gRPC client calls using interceptor.

```go
package grpcclient

import (
    "context"
    "fmt"
    "time"

    "github.com/michael-jaquier/circuitbreaker"
    "google.golang.org/grpc"
)

// CircuitBreakerInterceptor creates a gRPC unary client interceptor with circuit breaker
func CircuitBreakerInterceptor(cb circuitbreaker.CircuitBreaker) grpc.UnaryClientInterceptor {
    return func(
        ctx context.Context,
        method string,
        req, reply interface{},
        cc *grpc.ClientConn,
        invoker grpc.UnaryInvoker,
        opts ...grpc.CallOption,
    ) error {
        var invokeErr error

        timer, err := cb.Execute(ctx, func(ctx context.Context) error {
            invokeErr = invoker(ctx, method, req, reply, cc, opts...)
            return invokeErr
        })

        if timer != nil {
            return fmt.Errorf("circuit breaker open for %s: %w",
                method, errors.New("circuit breaker is open"))
        }

        return err
    }
}

// NewGRPCClientWithCircuitBreaker creates a gRPC client connection with circuit breaker
func NewGRPCClientWithCircuitBreaker(target string) (*grpc.ClientConn, error) {
    // Create circuit breaker
    cb, err := circuitbreaker.NewZeroTolerance(
        circuitbreaker.WithCooldownTimer(30 * time.Second),
        circuitbreaker.WithSuccessToClose(5),
    )
    if err != nil {
        return nil, fmt.Errorf("failed to create circuit breaker: %w", err)
    }

    // Create gRPC connection with circuit breaker interceptor
    conn, err := grpc.Dial(
        target,
        grpc.WithUnaryInterceptor(CircuitBreakerInterceptor(cb)),
        grpc.WithInsecure(), // Use WithTransportCredentials in production
    )
    if err != nil {
        return nil, fmt.Errorf("failed to connect: %w", err)
    }

    return conn, nil
}

// Example usage
func ExampleGRPCUsage() {
    conn, err := NewGRPCClientWithCircuitBreaker("localhost:50051")
    if err != nil {
        panic(err)
    }
    defer conn.Close()

    // Use the connection with your gRPC client
    // client := pb.NewYourServiceClient(conn)
    // resp, err := client.YourMethod(context.Background(), &pb.YourRequest{})
}
```

### Example 5: Service-to-Service Client

Multi-method service client with circuit breaker.

```go
package serviceclient

import (
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "time"

    "github.com/michael-jaquier/circuitbreaker"
)

// OrderServiceClient handles communication with the order service
type OrderServiceClient struct {
    baseURL string
    breaker circuitbreaker.CircuitBreaker
    client  *http.Client
    cache   OrderCache // Optional: for fallback when circuit is open
}

type Order struct {
    ID     string  `json:"id"`
    Total  float64 `json:"total"`
    Status string  `json:"status"`
}

type OrderCache interface {
    Get(id string) (*Order, error)
    Set(id string, order *Order) error
}

// NewOrderServiceClient creates a new order service client with circuit breaker
func NewOrderServiceClient(baseURL string, cache OrderCache) (*OrderServiceClient, error) {
    cb, err := circuitbreaker.NewZeroTolerance(
        circuitbreaker.WithCooldownTimer(45 * time.Second),
        circuitbreaker.WithSuccessToClose(3),
        circuitbreaker.WithMaximumProbes(1),
    )
    if err != nil {
        return nil, fmt.Errorf("failed to create circuit breaker: %w", err)
    }

    return &OrderServiceClient{
        baseURL: baseURL,
        breaker: cb,
        client:  &http.Client{Timeout: 10 * time.Second},
        cache:   cache,
    }, nil
}

func (o *OrderServiceClient) GetOrder(ctx context.Context, orderID string) (*Order, error) {
    url := fmt.Sprintf("%s/orders/%s", o.baseURL, orderID)
    req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
    if err != nil {
        return nil, fmt.Errorf("failed to create request: %w", err)
    }

    var resp *http.Response
    var httpErr error

    timer, err := o.breaker.Execute(ctx, func(ctx context.Context) error {
        resp, httpErr = o.client.Do(req)
        if httpErr != nil {
            return httpErr
        }

        if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
            return fmt.Errorf("server error: %d", resp.StatusCode)
        }
        return nil
    })

    if timer != nil {
        // Circuit is open, try cache fallback
        if o.cache != nil {
            cached, cacheErr := o.cache.Get(orderID)
            if cacheErr == nil {
                return cached, nil
            }
        }
        return nil, fmt.Errorf("order service unavailable: %w", errors.New("circuit breaker is open"))
    }
    if err != nil {
        return nil, fmt.Errorf("request failed: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
    }

    var order Order
    if err := json.NewDecoder(resp.Body).Decode(&order); err != nil {
        return nil, fmt.Errorf("failed to decode: %w", err)
    }

    // Update cache on success
    if o.cache != nil {
        _ = o.cache.Set(orderID, &order)
    }

    return &order, nil
}

func (o *OrderServiceClient) CreateOrder(ctx context.Context, order *Order) error {
    jsonData, err := json.Marshal(order)
    if err != nil {
        return fmt.Errorf("failed to marshal order: %w", err)
    }

    req, err := http.NewRequestWithContext(ctx, "POST", o.baseURL+"/orders", bytes.NewBuffer(jsonData))
    if err != nil {
        return fmt.Errorf("failed to create request: %w", err)
    }
    req.Header.Set("Content-Type", "application/json")

    var resp *http.Response
    var httpErr error

    timer, err := o.breaker.Execute(ctx, func(ctx context.Context) error {
        resp, httpErr = o.client.Do(req)
        if httpErr != nil {
            return httpErr
        }

        if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
            return fmt.Errorf("server error: %d", resp.StatusCode)
        }
        return nil
    })

    if timer != nil {
        return fmt.Errorf("order service unavailable: %w", errors.New("circuit breaker is open"))
    }
    if err != nil {
        return fmt.Errorf("request failed: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusCreated {
        return fmt.Errorf("unexpected status: %d", resp.StatusCode)
    }

    return nil
}

func (o *OrderServiceClient) UpdateOrderStatus(ctx context.Context, orderID, status string) error {
    payload := map[string]string{"status": status}
    jsonData, err := json.Marshal(payload)
    if err != nil {
        return fmt.Errorf("failed to marshal payload: %w", err)
    }

    url := fmt.Sprintf("%s/orders/%s/status", o.baseURL, orderID)
    req, err := http.NewRequestWithContext(ctx, "PATCH", url, bytes.NewBuffer(jsonData))
    if err != nil {
        return fmt.Errorf("failed to create request: %w", err)
    }
    req.Header.Set("Content-Type", "application/json")

    var resp *http.Response
    var httpErr error

    timer, err := o.breaker.Execute(ctx, func(ctx context.Context) error {
        resp, httpErr = o.client.Do(req)
        if httpErr != nil {
            return httpErr
        }

        if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
            return fmt.Errorf("server error: %d", resp.StatusCode)
        }
        return nil
    })

    if timer != nil {
        return fmt.Errorf("order service unavailable: %w", errors.New("circuit breaker is open"))
    }
    if err != nil {
        return fmt.Errorf("request failed: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return fmt.Errorf("unexpected status: %d", resp.StatusCode)
    }

    return nil
}
```

### Example 6: Background Worker Pattern

Circuit breaker for background job processing.

```go
package worker

import (
    "context"
    "fmt"
    "log"
    "time"

    "github.com/michael-jaquier/circuitbreaker"
)

// Worker processes background jobs with circuit breaker protection
type Worker struct {
    breaker circuitbreaker.CircuitBreaker
    process func(context.Context, Job) error
}

type Job struct {
    ID      string
    Payload interface{}
}

// NewWorker creates a new worker with circuit breaker
func NewWorker(processFunc func(context.Context, Job) error) (*Worker, error) {
    cb, err := circuitbreaker.NewZeroTolerance(
        circuitbreaker.WithCooldownTimer(120 * time.Second),
        circuitbreaker.WithSuccessToClose(5),
        circuitbreaker.WithWindowSize(300 * time.Second),
    )
    if err != nil {
        return nil, fmt.Errorf("failed to create circuit breaker: %w", err)
    }

    return &Worker{
        breaker: cb,
        process: processFunc,
    }, nil
}

// ProcessJob processes a single job with circuit breaker protection
func (w *Worker) ProcessJob(ctx context.Context, job Job) error {
    var processErr error

    timer, err := w.breaker.Execute(ctx, func(ctx context.Context) error {
        processErr = w.process(ctx, job)
        return processErr
    })

    if timer != nil {
        return fmt.Errorf("circuit breaker is open")
    }
    if err != nil {
        return fmt.Errorf("job processing failed: %w", err)
    }

    return nil
}

// Run starts the worker loop
func (w *Worker) Run(ctx context.Context, jobChan <-chan Job) {
    for {
        select {
        case <-ctx.Done():
            return
        case job := <-jobChan:
            err := w.ProcessJob(ctx, job)
            if err != nil {
                if timer != nil {
                    log.Printf("Circuit breaker open, requeueing job %s", job.ID)
                    // Requeue job or send to dead letter queue
                } else {
                    log.Printf("Job %s failed: %v", job.ID, err)
                }
            } else {
                log.Printf("Job %s completed successfully", job.ID)
            }
        }
    }
}

// Example usage
func ExampleWorkerUsage() {
    processFunc := func(ctx context.Context, job Job) error {
        // Your actual job processing logic
        fmt.Printf("Processing job %s\n", job.ID)
        return nil
    }

    worker, err := NewWorker(processFunc)
    if err != nil {
        panic(err)
    }

    jobChan := make(chan Job, 100)
    ctx := context.Background()

    go worker.Run(ctx, jobChan)

    // Send jobs to the worker
    jobChan <- Job{ID: "job-1", Payload: "data"}
}
```

### Example 7: HTTP Middleware Pattern

Protecting downstream services in HTTP handlers.

```go
package middleware

import (
    "fmt"
    "net/http"
    "time"

    "github.com/michael-jaquier/circuitbreaker"
)

// CircuitBreakerMiddleware creates HTTP middleware with circuit breaker
func CircuitBreakerMiddleware(cb circuitbreaker.CircuitBreaker) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            var handlerErr error
            wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

            timer, err := cb.Execute(r.Context(), func(ctx context.Context) error {
                next.ServeHTTP(wrapped, r)

                // Report based on response status
                if wrapped.statusCode >= 500 {
                    handlerErr = fmt.Errorf("server error: %d", wrapped.statusCode)
                    return handlerErr
                }
                return nil
            })

            if timer != nil {
                http.Error(w, "Service temporarily unavailable", http.StatusServiceUnavailable)
                return
            }
            if err != nil {
                // Error already written by handler
                return
            }
        })
    }
}

type responseWriter struct {
    http.ResponseWriter
    statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
    rw.statusCode = code
    rw.ResponseWriter.WriteHeader(code)
}

// Example usage
func ExampleMiddlewareUsage() {
    cb, err := circuitbreaker.NewZeroTolerance(
        circuitbreaker.WithCooldownTimer(60 * time.Second),
        circuitbreaker.WithSuccessToClose(3),
    )
    if err != nil {
        panic(err)
    }

    handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Your handler logic
        w.WriteHeader(http.StatusOK)
        w.Write([]byte("OK"))
    })

    // Wrap with circuit breaker middleware
    protectedHandler := CircuitBreakerMiddleware(cb)(handler)

    http.Handle("/api", protectedHandler)
    http.ListenAndServe(":8080", nil)
}
```

## Code Generation Templates

Use these templates with placeholder substitution for AI code generation.

### Template 1: Basic HTTP Client Template (with Execute)

```go
type {{ServiceName}}Client struct {
    baseURL string
    breaker circuitbreaker.CircuitBreaker
    client  *http.Client
}

func New{{ServiceName}}Client(baseURL string) (*{{ServiceName}}Client, error) {
    cb, err := circuitbreaker.NewZeroTolerance(
        circuitbreaker.WithCooldownTimer({{COOLDOWN_SECONDS}} * time.Second),
        circuitbreaker.WithSuccessToClose({{SUCCESS_COUNT}}),
        circuitbreaker.WithMaximumProbes({{MAX_PROBES}}),
    )
    if err != nil {
        return nil, fmt.Errorf("failed to create circuit breaker: %w", err)
    }

    return &{{ServiceName}}Client{
        baseURL: baseURL,
        breaker: cb,
        client:  &http.Client{Timeout: {{TIMEOUT_SECONDS}} * time.Second},
    }, nil
}

func (c *{{ServiceName}}Client) {{MethodName}}(ctx context.Context{{PARAMS}}) ({{RETURN_TYPE}}, error) {
    req, err := http.NewRequestWithContext(ctx, "{{HTTP_METHOD}}", c.baseURL+"{{PATH}}", {{BODY}})
    if err != nil {
        return {{ZERO_VALUE}}, fmt.Errorf("failed to create request: %w", err)
    }

    var resp *http.Response
    var httpErr error

    timer, err := c.breaker.Execute(ctx, func(ctx context.Context) error {
        resp, httpErr = c.client.Do(req)
        if httpErr != nil {
            return httpErr
        }

        if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
            return fmt.Errorf("server error: %d", resp.StatusCode)
        }
        return nil
    })

    if timer != nil {
        return {{ZERO_VALUE}}, fmt.Errorf("{{SERVICE_NAME}} unavailable: %w", errors.New("circuit breaker is open"))
    }
    if err != nil {
        return {{ZERO_VALUE}}, fmt.Errorf("request failed: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return {{ZERO_VALUE}}, fmt.Errorf("unexpected status: %d", resp.StatusCode)
    }

    {{DECODE_LOGIC}}
    return {{RESULT}}, nil
}
```

### Template 1B: Basic HTTP Client Template (with ExecuteBlocking)

Use this when you want automatic retry on circuit open:

```go
func (c *{{ServiceName}}Client) {{MethodName}}Blocking(ctx context.Context{{PARAMS}}) ({{RETURN_TYPE}}, error) {
    req, err := http.NewRequestWithContext(ctx, "{{HTTP_METHOD}}", c.baseURL+"{{PATH}}", {{BODY}})
    if err != nil {
        return {{ZERO_VALUE}}, fmt.Errorf("failed to create request: %w", err)
    }

    var resp *http.Response
    var httpErr error

    err = c.breaker.ExecuteBlocking(ctx, func(ctx context.Context) error {
        resp, httpErr = c.client.Do(req)
        if httpErr != nil {
            return httpErr
        }

        if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
            return fmt.Errorf("server error: %d", resp.StatusCode)
        }
        return nil
    })

    if err != nil {
        return {{ZERO_VALUE}}, fmt.Errorf("request failed: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return {{ZERO_VALUE}}, fmt.Errorf("unexpected status: %d", resp.StatusCode)
    }

    {{DECODE_LOGIC}}
    return {{RESULT}}, nil
}
```

### Template 2: Repository Pattern Template

```go
type {{EntityName}}Repository interface {
    GetByID(ctx context.Context, id string) (*{{EntityName}}, error)
    Create(ctx context.Context, entity *{{EntityName}}) error
    Update(ctx context.Context, entity *{{EntityName}}) error
    Delete(ctx context.Context, id string) error
}

type http{{EntityName}}Repository struct {
    apiURL  string
    breaker circuitbreaker.CircuitBreaker
    client  *http.Client
}

func NewHTTP{{EntityName}}Repository(apiURL string) ({{EntityName}}Repository, error) {
    cb, err := circuitbreaker.NewZeroTolerance(
        circuitbreaker.WithCooldownTimer({{COOLDOWN_SECONDS}} * time.Second),
        circuitbreaker.WithSuccessToClose({{SUCCESS_COUNT}}),
    )
    if err != nil {
        return nil, fmt.Errorf("failed to create circuit breaker: %w", err)
    }

    return &http{{EntityName}}Repository{
        apiURL:  apiURL,
        breaker: cb,
        client:  &http.Client{Timeout: {{TIMEOUT_SECONDS}} * time.Second},
    }, nil
}

func (r *http{{EntityName}}Repository) GetByID(ctx context.Context, id string) (*{{EntityName}}, error) {
    url := fmt.Sprintf("%s/{{API_PATH}}/%s", r.apiURL, id)
    req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
    if err != nil {
        return nil, fmt.Errorf("failed to create request: %w", err)
    }

    var resp *http.Response
    var httpErr error

    timer, err := r.breaker.Execute(ctx, func(ctx context.Context) error {
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
        return nil, fmt.Errorf("{{SERVICE_NAME}} unavailable: %w", errors.New("circuit breaker is open"))
    }
    if err != nil {
        return nil, fmt.Errorf("request failed: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode == http.StatusNotFound {
        return nil, fmt.Errorf("{{ENTITY_NAME}} not found: %s", id)
    }

    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
    }

    var entity {{EntityName}}
    if err := json.NewDecoder(resp.Body).Decode(&entity); err != nil {
        return nil, fmt.Errorf("failed to decode: %w", err)
    }

    return &entity, nil
}
```

### Template 3: Database Wrapper Template

```go
type {{WrapperName}} struct {
    db      *sql.DB
    breaker circuitbreaker.CircuitBreaker
}

func New{{WrapperName}}(db *sql.DB) (*{{WrapperName}}, error) {
    cb, err := circuitbreaker.NewZeroTolerance(
        circuitbreaker.WithCooldownTimer({{COOLDOWN_SECONDS}} * time.Second),
        circuitbreaker.WithSuccessToClose({{SUCCESS_COUNT}}),
        circuitbreaker.WithWindowSize({{WINDOW_SIZE_SECONDS}} * time.Second),
    )
    if err != nil {
        return nil, fmt.Errorf("failed to create circuit breaker: %w", err)
    }

    return &{{WrapperName}}{
        db:      db,
        breaker: cb,
    }, nil
}

func (w *{{WrapperName}}) Query(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
    var rows *sql.Rows
    var queryErr error

    timer, err := w.breaker.Execute(ctx, func(ctx context.Context) error {
        rows, queryErr = w.db.QueryContext(ctx, query, args...)
        return queryErr
    })

    if timer != nil {
        return nil, fmt.Errorf("circuit breaker is open")
    }
    if err != nil {
        return nil, err
    }

    return rows, nil
}
```

## Anti-Patterns

### 1. DO NOT wrap every function call

WRONG:
```go
func calculateTax(ctx context.Context, amount float64, cb circuitbreaker.CircuitBreaker) float64 {
    // This is an in-memory calculation, no circuit breaker needed!
    var result float64
    timer, err := cb.Execute(ctx, func(ctx context.Context) error {
        result = amount * 0.15
        return nil
    })
    if timer != nil || err != nil {
        return 0
    }
    return result
}
```

RIGHT:
```go
func calculateTax(amount float64) float64 {
    // Pure function, no external dependency
    return amount * 0.15
}

func fetchTaxRate(ctx context.Context, cb circuitbreaker.CircuitBreaker) (float64, error) {
    // External API call - circuit breaker appropriate
    req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.example.com/tax-rate", nil)

    var resp *http.Response
    var httpErr error

    timer, err := cb.Execute(ctx, func(ctx context.Context) error {
        client := &http.Client{Timeout: 10 * time.Second}
        resp, httpErr = client.Do(req)
        if httpErr != nil {
            return httpErr
        }

        if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
            return fmt.Errorf("server error: %d", resp.StatusCode)
        }
        return nil
    })

    if timer != nil {
        return 0, fmt.Errorf("circuit breaker is open")
    }
    if err != nil {
        return 0, err
    }
    defer resp.Body.Close()
    // ... decode and return tax rate
    return 0.15, nil
}
```

### 2. DO NOT share breaker across unrelated services

WRONG:
```go
var globalCircuitBreaker circuitbreaker.CircuitBreaker

func callServiceA(ctx context.Context) error {
    // Using global breaker
    req, _ := http.NewRequestWithContext(ctx, "GET", "https://service-a.com", nil)
    var resp *http.Response
    var httpErr error

    timer, err := globalCircuitBreaker.Execute(ctx, func(ctx context.Context) error {
        client := &http.Client{Timeout: 10 * time.Second}
        resp, httpErr = client.Do(req)
        return httpErr
    })
    // ... handle timer and err
    return err
}

func callServiceB(ctx context.Context) error {
    // Same breaker! Service B failures will affect Service A
    req, _ := http.NewRequestWithContext(ctx, "GET", "https://service-b.com", nil)
    var resp *http.Response
    var httpErr error

    timer, err := globalCircuitBreaker.Execute(ctx, func(ctx context.Context) error {
        client := &http.Client{Timeout: 10 * time.Second}
        resp, httpErr = client.Do(req)
        return httpErr
    })
    // ... handle timer and err
    return err
}
```

RIGHT:
```go
type Clients struct {
    serviceABreaker circuitbreaker.CircuitBreaker
    serviceBBreaker circuitbreaker.CircuitBreaker
}

func (c *Clients) callServiceA(ctx context.Context) error {
    req, _ := http.NewRequestWithContext(ctx, "GET", "https://service-a.com", nil)
    var resp *http.Response
    var httpErr error

    timer, err := c.serviceABreaker.Execute(ctx, func(ctx context.Context) error {
        client := &http.Client{Timeout: 10 * time.Second}
        resp, httpErr = client.Do(req)
        return httpErr
    })
    // ... handle timer and err
    return err
}

func (c *Clients) callServiceB(ctx context.Context) error {
    req, _ := http.NewRequestWithContext(ctx, "GET", "https://service-b.com", nil)
    var resp *http.Response
    var httpErr error

    timer, err := c.serviceBBreaker.Execute(ctx, func(ctx context.Context) error {
        client := &http.Client{Timeout: 10 * time.Second}
        resp, httpErr = client.Do(req)
        return httpErr
    })
    // ... handle timer and err
    return err
}
```

### 3. DO NOT ignore ErrCircuitOpen

WRONG:
```go
var resp *http.Response
var httpErr error

timer, err := cb.Execute(ctx, func(ctx context.Context) error {
    client := &http.Client{Timeout: 10 * time.Second}
    resp, httpErr = client.Do(req)
    return httpErr
})

if timer != nil {
    // Just retry immediately!
    timer, err = cb.Execute(ctx, func(ctx context.Context) error {
        resp, httpErr = client.Do(req)
        return httpErr
    }) // Still open, waste of resources
}
```

RIGHT:
```go
var resp *http.Response
var httpErr error

timer, err := cb.Execute(ctx, func(ctx context.Context) error {
    client := &http.Client{Timeout: 10 * time.Second}
    resp, httpErr = client.Do(req)
    return httpErr
})

if timer != nil {
    // Use fallback strategy
    return getCachedData(), nil
}
if err != nil {
    return nil, err
}
```

### 4. DO NOT use without monitoring

WRONG:
```go
func NewClient() *Client {
    cb, _ := circuitbreaker.NewZeroTolerance()
    return &Client{breaker: cb}
}
```

RIGHT:
```go
func NewClient(metrics MetricsCollector) *Client {
    cb, _ := circuitbreaker.NewZeroTolerance()

    // Monitor circuit breaker state
    go func() {
        ticker := time.NewTicker(10 * time.Second)
        for range ticker.C {
            // Log state changes, emit metrics
            metrics.RecordCircuitBreakerState(cb.State())
        }
    }()

    return &Client{breaker: cb}
}
```

### 5. DO NOT classify errors incorrectly

WRONG:
```go
// Not checking status codes - all responses treated as success
var resp *http.Response
var httpErr error

timer, err := cb.Execute(ctx, func(ctx context.Context) error {
    client := &http.Client{Timeout: 10 * time.Second}
    resp, httpErr = client.Do(req)
    // Always returning nil - treats 500 errors as success!
    return nil
})
```

RIGHT:
```go
// Properly classify errors based on status codes
var resp *http.Response
var httpErr error

timer, err := cb.Execute(ctx, func(ctx context.Context) error {
    client := &http.Client{Timeout: 10 * time.Second}
    resp, httpErr = client.Do(req)
    if httpErr != nil {
        return httpErr
    }

    // Classify 5xx as failures for circuit breaker
    if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
        return fmt.Errorf("server error: %d", resp.StatusCode)
    }
    return nil
})
```

## Testing Patterns

### Pattern 1: Testing State Transitions

```go
func TestCircuitBreakerOpens(t *testing.T) {
    fakeClock := &FakeClock{now: time.Now()}
    cb, err := circuitbreaker.NewZeroTolerance(
        circuitbreaker.WithClock(fakeClock),
    )
    if err != nil {
        t.Fatal(err)
    }

    ctx := context.Background()

    // Circuit should start closed - first execution should succeed
    timer, err := cb.Execute(ctx, func(ctx context.Context) error {
        return nil
    })
    if timer != nil {
        t.Error("Circuit should start closed")
    }
    if err != nil {
        t.Error("First execution should succeed")
    }

    // Cause a failure - should open immediately (zero tolerance)
    timer, err = cb.Execute(ctx, func(ctx context.Context) error {
        return fmt.Errorf("test failure")
    })
    if err == nil {
        t.Error("Expected failure to be returned")
    }

    // Verify circuit is now open
    timer, err = cb.Execute(ctx, func(ctx context.Context) error {
        return nil
    })
    if timer == nil {
        t.Error("Circuit should be open after failure")
    }
}
```

**Note on implementation**: State transitions from Closed→Open happen synchronously within the `Execute()` call. When a failure increments the counter past the threshold, the circuit immediately transitions to Open state inline - there is no polling delay.

### Pattern 2: Testing Recovery

```go
func TestCircuitBreakerRecovery(t *testing.T) {
    fakeClock := &FakeClock{now: time.Now()}
    cb, err := circuitbreaker.NewZeroTolerance(
        circuitbreaker.WithClock(fakeClock),
        circuitbreaker.WithCooldownTimer(60 * time.Second),
        circuitbreaker.WithSuccessToClose(3),
    )
    if err != nil {
        t.Fatal(err)
    }

    ctx := context.Background()

    // Open the circuit by causing a failure
    timer, err := cb.Execute(ctx, func(ctx context.Context) error {
        return fmt.Errorf("test failure")
    })

    // Advance time past cooldown
    fakeClock.Advance(61 * time.Second)

    // Should now be in half-open, allowing probes
    timer, err = cb.Execute(ctx, func(ctx context.Context) error {
        return nil
    })
    if timer != nil {
        t.Error("Circuit should allow probe requests in half-open")
    }

    // Execute 2 more successes to close circuit (3 total)
    timer, err = cb.Execute(ctx, func(ctx context.Context) error {
        return nil
    })
    timer, err = cb.Execute(ctx, func(ctx context.Context) error {
        return nil
    })

    // Circuit should be closed - verify by executing another request
    timer, err = cb.Execute(ctx, func(ctx context.Context) error {
        return nil
    })
    if timer != nil {
        t.Error("Circuit should be closed after successful recovery")
    }
    if err != nil {
        t.Error("Execution should succeed when circuit is closed")
    }
}
```

**Note on implementation**: The Open→HalfOpen transition happens lazily. When you advance the clock past the cooldown period, the actual state transition occurs in the next `allow()` call using Compare-and-Swap (CAS). This lazy evaluation eliminates background goroutines while maintaining correct timing semantics.

### Circuit Breaker Architecture: Event-Driven State Transitions

The circuit breaker uses an event-driven architecture for state transitions:

**Immediate Transitions (inline in Execute())**:
- Closed→Open: Triggered when failure count >= threshold
- HalfOpen→Open: Triggered on any failure
- HalfOpen→Closed: Triggered when success count >= successToClose

These transitions happen synchronously during the `Execute()` call with zero latency.

**Lazy Transition (in allow())**:
- Open→HalfOpen: Checked lazily using `CompareAndSwap` when `halfOpenAt` time is reached

**Background Monitoring**:
- `monitorStateTransitions()` only handles sliding window counter resets
- No polling ticker for state transitions

### Pattern 3: Testing with HTTP Server

```go
func TestHTTPClientWithCircuitBreaker(t *testing.T) {
    failureCount := 0
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if failureCount > 0 {
            w.WriteHeader(http.StatusInternalServerError)
            failureCount--
            return
        }
        w.WriteHeader(http.StatusOK)
        w.Write([]byte("success"))
    }))
    defer server.Close()

    fakeClock := &FakeClock{now: time.Now()}
    cb, err := circuitbreaker.NewZeroTolerance(circuitbreaker.WithClock(fakeClock))
    if err != nil {
        t.Fatal(err)
    }

    ctx := context.Background()

    // First request fails - should open circuit
    failureCount = 1
    req, _ := http.NewRequestWithContext(ctx, "GET", server.URL, nil)

    var resp *http.Response
    var httpErr error

    timer, err := cb.Execute(ctx, func(ctx context.Context) error {
        client := &http.Client{Timeout: 10 * time.Second}
        resp, httpErr = client.Do(req)
        if httpErr != nil {
            return httpErr
        }

        if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
            return fmt.Errorf("server error: %d", resp.StatusCode)
        }
        return nil
    })

    if err == nil {
        t.Error("Expected error from server")
    }

    // Second request should be blocked by circuit breaker
    req, _ = http.NewRequestWithContext(ctx, "GET", server.URL, nil)
    timer, err = cb.Execute(ctx, func(ctx context.Context) error {
        client := &http.Client{Timeout: 10 * time.Second}
        resp, httpErr = client.Do(req)
        return httpErr
    })

    if timer == nil {
        t.Error("Expected circuit breaker to be open")
    }
}
```

### Pattern 4: Table-Driven Tests

```go
func TestCircuitBreakerScenarios(t *testing.T) {
    tests := []struct {
        name          string
        setup         func(cb circuitbreaker.CircuitBreaker, clock *FakeClock, ctx context.Context)
        expectedOpen  bool
    }{
        {
            name:         "closed state allows requests",
            setup:        func(cb circuitbreaker.CircuitBreaker, clock *FakeClock, ctx context.Context) {},
            expectedOpen: false,
        },
        {
            name: "single failure opens circuit",
            setup: func(cb circuitbreaker.CircuitBreaker, clock *FakeClock, ctx context.Context) {
                cb.Execute(ctx, func(ctx context.Context) error {
                    return fmt.Errorf("test failure")
                })
            },
            expectedOpen: true,
        },
        {
            name: "half-open allows probes",
            setup: func(cb circuitbreaker.CircuitBreaker, clock *FakeClock, ctx context.Context) {
                cb.Execute(ctx, func(ctx context.Context) error {
                    return fmt.Errorf("test failure")
                })
                // Advance past cooldown - this sets up the condition for lazy transition
                // The actual Open→HalfOpen transition happens in allow() via CAS
                clock.Advance(121 * time.Second)
            },
            expectedOpen: false,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            fakeClock := &FakeClock{now: time.Now()}
            cb, err := circuitbreaker.NewZeroTolerance(circuitbreaker.WithClock(fakeClock))
            if err != nil {
                t.Fatal(err)
            }

            ctx := context.Background()
            tt.setup(cb, fakeClock, ctx)

            timer, err := cb.Execute(ctx, func(ctx context.Context) error {
                return nil
            })

            isOpen := (timer != nil)
            if isOpen != tt.expectedOpen {
                t.Errorf("Expected open=%v, got %v", tt.expectedOpen, isOpen)
            }
        })
    }
}
```

### FakeClock Implementation

Always use this FakeClock for testing:

```go
type FakeClock struct {
    now time.Time
}

func (f *FakeClock) Now() time.Time        { return f.now }
func (f *FakeClock) Sleep(d time.Duration) { f.Advance(d) }
func (f *FakeClock) After(d time.Duration) <-chan time.Time {
    f.Advance(d)
    ch := make(chan time.Time, 1)
    ch <- f.now
    return ch
}
func (f *FakeClock) Advance(d time.Duration) { f.now = f.now.Add(d) }
```

## Integration Checklist

When integrating circuit breaker into a codebase, verify:

- [ ] Circuit breaker initialized with appropriate configuration for service criticality
- [ ] Circuit open condition is handled with proper fallback strategy (cache, default value, or meaningful error)
- [ ] Error classification is correct (return error from Execute function to mark as failure)
- [ ] Circuit breaker is NOT shared across unrelated services
- [ ] Tests include `FakeClock` usage for time-dependent behavior
- [ ] Monitoring/logging added to track circuit breaker state transitions
- [ ] Documentation updated to explain circuit breaker behavior
- [ ] Only wrapping external dependencies (HTTP, database, RPC), not pure functions
- [ ] Error classification is appropriate (5xx status codes classified as errors for HTTP)
- [ ] Timeouts are set on underlying clients (HTTP client timeout, database query timeout)

## Configuration Recommendation Algorithm

Use this algorithm to choose configuration:

```
IF service_is_critical THEN
    CooldownTimer = 30-60 seconds
    SuccessToClose = 3
    MaximumProbes = 1
ELSE IF service_is_normal THEN
    CooldownTimer = 120 seconds (default)
    SuccessToClose = 5 (default)
    MaximumProbes = 1-2
ELSE (service is non-critical)
    CooldownTimer = 180-300 seconds
    SuccessToClose = 5-10
    MaximumProbes = 2-3
END IF

IF HTTP_service THEN
    IF client_errors_acceptable (4xx) THEN
            return code >= 500 && code <= 599
        })
    ELSE
        Use default (4xx and 5xx are errors)
    END IF
END IF

WindowSize = 2 * CooldownTimer (recommended)
ResetTimer = CooldownTimer / 2 (recommended)
```

## Summary

This guide provides:

1. Decision trees for selecting the right pattern
2. Seven complete, production-ready examples
3. Code generation templates with placeholders
4. Anti-patterns to avoid
5. Comprehensive testing patterns
6. Integration checklist

AI tools should use the decision tree to select the appropriate example or template, substitute placeholders with actual values, and follow the testing patterns to generate complete, tested integrations.
