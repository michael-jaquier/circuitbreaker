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

Q: Should you use blocking or non-blocking execution?
│
├─ User-facing requests (APIs, web handlers)
│  └─ Use Execute() - fail fast, return errors immediately
│
├─ Background workers, batch jobs
│  └─ Use ExecuteBlocking() or specific blocking methods
│
├─ HTTP Client implementation
│  ├─ Need automatic retry and intelligent status classification
│  │  └─ Use ExecuteHTTPBlocking()
│  └─ Need custom fallback logic or immediate circuit open feedback
│     └─ Use Execute()
│
└─ gRPC Client implementation
   ├─ Need automatic retry and clean interface
   │  └─ Use ExecuteGRPCBlocking()
   └─ Need custom fallback or immediate circuit open feedback
      └─ Use Execute()
```

## Configuration Decision Matrix

| Scenario | WindowSize | CooldownTimer | SuccessToClose | MaxProbes | Status Code Check | Execution Method |
|----------|------------|---------------|----------------|-----------|------------------|------------------|
| Critical Payment Service | 60s | 30s | 3 | 1 | 5xx only | Execute() |
| User Profile API | 240s (default) | 120s (default) | 5 (default) | 1 | default (4xx, 5xx) | Execute() |
| Analytics Service | 300s | 180s | 10 | 3 | 5xx only | ExecuteHTTPBlocking() |
| Internal Admin API | 120s | 60s | 5 | 2 | default | Execute() |
| Background Data Sync | 600s | 300s | 5 | 1 | 5xx only | ExecuteHTTPBlocking() |
| Health Check Endpoint | 60s | 30s | 3 | 1 | default | Execute() |

## Complete Working Examples

### Example 1A: HTTP API Client with Execute (Manual Timer Handling)

Complete, production-ready HTTP client with circuit breaker protection using Execute() for manual control.

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

### Example 1B: HTTP API Client with ExecuteHTTPBlocking (Automatic Retry)

Simplified HTTP client using ExecuteHTTPBlocking for automatic retry and intelligent status code classification. Best for background workers and batch jobs.

```go
package httpclient

import (
    "bytes"
    "context"
    "fmt"
    "io"
    "net/http"
    "time"

    "github.com/michael-jaquier/circuitbreaker"
)

// BlockingAPIClient wraps an HTTP client with ExecuteHTTPBlocking
type BlockingAPIClient struct {
    baseURL string
    breaker circuitbreaker.CircuitBreaker
    client  *http.Client
}

// NewBlockingAPIClient creates a new HTTP client with ExecuteHTTPBlocking
func NewBlockingAPIClient(baseURL string, timeout, cooldown time.Duration) (*BlockingAPIClient, error) {
    cb, err := circuitbreaker.NewZeroTolerance(
        circuitbreaker.WithCooldownTimer(cooldown),
        circuitbreaker.WithSuccessToClose(3),
    )
    if err != nil {
        return nil, fmt.Errorf("failed to create circuit breaker: %w", err)
    }

    return &BlockingAPIClient{
        baseURL: baseURL,
        breaker: cb,
        client:  &http.Client{Timeout: timeout},
    }, nil
}

// Get performs a GET request with automatic retry
func (c *BlockingAPIClient) Get(ctx context.Context, path string) ([]byte, error) {
    // Request factory creates fresh request for each retry
    requestFactory := func() (*http.Request, error) {
        return http.NewRequest("GET", c.baseURL+path, nil)
    }

    // ExecuteHTTPBlocking handles:
    // - Automatic retry when circuit is open
    // - Status code classification (408, 429, 5xx retryable)
    // - Request recreation for each retry
    resp, err := c.breaker.ExecuteHTTPBlocking(ctx, c.client, requestFactory)
    if err != nil {
        return nil, fmt.Errorf("request failed: %w", err)
    }
    defer resp.Body.Close()

    // Read response body
    body, err := io.ReadAll(resp.Body)
    if err != nil {
        return nil, fmt.Errorf("failed to read response: %w", err)
    }

    // Check for non-retryable client errors (4xx except 408, 429)
    if resp.StatusCode >= 400 && resp.StatusCode < 500 &&
        resp.StatusCode != 408 && resp.StatusCode != 429 {
        return nil, fmt.Errorf("client error: %d, body: %s", resp.StatusCode, string(body))
    }

    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        return nil, fmt.Errorf("unexpected status: %d, body: %s", resp.StatusCode, string(body))
    }

    return body, nil
}

// Post performs a POST request with automatic retry and body replay
func (c *BlockingAPIClient) Post(ctx context.Context, path string, jsonBody []byte) ([]byte, error) {
    // Request factory creates fresh request with fresh body for each retry
    requestFactory := func() (*http.Request, error) {
        req, err := http.NewRequest("POST", c.baseURL+path, bytes.NewBuffer(jsonBody))
        if err != nil {
            return nil, err
        }
        req.Header.Set("Content-Type", "application/json")
        return req, nil
    }

    resp, err := c.breaker.ExecuteHTTPBlocking(ctx, c.client, requestFactory)
    if err != nil {
        return nil, fmt.Errorf("request failed: %w", err)
    }
    defer resp.Body.Close()

    body, err := io.ReadAll(resp.Body)
    if err != nil {
        return nil, fmt.Errorf("failed to read response: %w", err)
    }

    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        return nil, fmt.Errorf("unexpected status: %d, body: %s", resp.StatusCode, string(body))
    }

    return body, nil
}

// Example usage
func ExampleBlockingUsage() {
    client, err := NewBlockingAPIClient(
        "https://api.example.com",
        10*time.Second,  // HTTP timeout
        60*time.Second,  // Circuit breaker cooldown
    )
    if err != nil {
        panic(err)
    }

    // Context with timeout provides natural retry boundary
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
    defer cancel()

    // Automatically retries when circuit is open
    // No need to check for timer or handle circuit open state
    data, err := client.Get(ctx, "/users/123")
    if err != nil {
        // Either request failed or context timeout exceeded
        fmt.Printf("Error: %v\n", err)
        return
    }

    fmt.Printf("Response: %s\n", data)
}
```

**Key advantages of ExecuteHTTPBlocking:**

1. **Reduced boilerplate**: 3 lines vs 15+ lines with Execute()
2. **Request factory pattern**: Fresh request for each retry enables body replay
3. **Built-in classification**: 408, 429, 5xx automatically retried; other 4xx fail fast
4. **Automatic waiting**: No manual timer handling needed
5. **Context-aware**: Respects context timeout as retry boundary

**When NOT to use:**
- User-facing request handlers (use Execute() for immediate response)
- Need custom fallback logic when circuit opens
- Want fine-grained control over retry behavior

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

## Blocking vs Non-Blocking Execution Methods

The circuit breaker provides four execution methods, each suited for different scenarios:

### Method Comparison

| Method | Returns on Circuit Open | Response Type | Code Complexity | Best Use Case |
|--------|------------------------|---------------|-----------------|---------------|
| **Execute()** | Timer object (non-blocking) | (timer, error) | 10-15 lines per call | User-facing APIs, custom fallback logic |
| **ExecuteBlocking()** | Blocks and waits | error | 3-5 lines per call | Background jobs, generic operations |
| **ExecuteHTTPBlocking()** | Blocks and waits | (*http.Response, error) | 3-5 lines per call | HTTP clients in background/batch contexts |
| **ExecuteGRPCBlocking()** | Blocks and waits | (interface{}, error) | 3 lines per call | gRPC clients in background/batch contexts |

### Execute() - Non-Blocking with Manual Timer Handling

**Signature:**
```go
Execute(ctx context.Context, fn func(context.Context) error) (<-chan time.Time, error)
```

**Use when:**
- Handling user-facing requests that need immediate responses
- Implementing custom fallback logic when circuit is open
- Need fine-grained control over retry behavior
- Want to avoid blocking the caller

**Pattern:**
```go
timer, err := breaker.Execute(ctx, func(ctx context.Context) error {
    return someOperation(ctx)
})

if timer != nil {
    // Circuit is OPEN - function was NOT executed
    // Return cached data, default value, or error immediately
    return cachedData, nil
}

if err != nil {
    // Function executed but returned error
    return nil, err
}

// Success!
return result, nil
```

### ExecuteBlocking() - Generic Blocking with Automatic Retry

**Signature:**
```go
ExecuteBlocking(ctx context.Context, fn func(context.Context) error) error
```

**Use when:**
- Background workers or batch jobs where latency is acceptable
- No need for protocol-specific features (HTTP/gRPC)
- Want automatic retry without manual timer handling
- Database calls, message queue operations, generic external services

**Pattern:**
```go
err := breaker.ExecuteBlocking(ctx, func(ctx context.Context) error {
    return someOperation(ctx)
})

// Automatically waits when circuit is open
// Returns immediately on success or function error
if err != nil {
    return fmt.Errorf("operation failed: %w", err)
}

// Success!
return nil
```

**Key differences from Execute():**
- No timer returned - blocks automatically when circuit is open
- Continues retrying until context timeout or success
- Reduces boilerplate from 15+ lines to 3-5 lines

### ExecuteHTTPBlocking() - HTTP-Specific Blocking with Intelligent Classification

**Signature:**
```go
ExecuteHTTPBlocking(ctx context.Context, client *http.Client,
    requestFactory func() (*http.Request, error)) (*http.Response, error)
```

**Use when:**
- HTTP client in background workers or batch jobs
- Want automatic retry with built-in HTTP status code classification
- Need request body replay for POST/PUT/PATCH
- Don't need custom fallback logic

**Pattern:**
```go
// Request factory creates fresh request for each retry
requestFactory := func() (*http.Request, error) {
    return http.NewRequest("GET", "https://api.example.com/data", nil)
}

resp, err := breaker.ExecuteHTTPBlocking(ctx, client, requestFactory)
if err != nil {
    return nil, fmt.Errorf("HTTP request failed: %w", err)
}
defer resp.Body.Close()

body, _ := io.ReadAll(resp.Body)
return body, nil
```

**Built-in features:**
- **Status code classification**: 408, 429, 5xx treated as retryable; other 4xx non-retryable
- **Request factory pattern**: Creates fresh request for each retry (enables body replay)
- **Response management**: Returns full http.Response with body intact
- **Automatic retry**: Blocks and waits when circuit is open

### ExecuteGRPCBlocking() - gRPC-Specific Blocking with Clean Interface

**Signature:**
```go
ExecuteGRPCBlocking(ctx context.Context,
    fn func(context.Context) (interface{}, error)) (interface{}, error)
```

**Use when:**
- gRPC client in background workers or batch jobs
- Want to eliminate 30+ lines of retry boilerplate
- Context timeout provides natural retry boundary
- Don't need custom fallback logic

**Pattern:**
```go
resp, err := breaker.ExecuteGRPCBlocking(ctx, func(ctx context.Context) (interface{}, error) {
    return grpcClient.SomeMethod(ctx, req)
})
if err != nil {
    return nil, fmt.Errorf("gRPC call failed: %w", err)
}

// Type assert the response
typedResp := resp.(*pb.SomeMethodResponse)
return typedResp, nil
```

**Key differences from HTTP:**
- Returns `interface{}` requiring type assertion (vs concrete `*http.Response`)
- Retries on any error (vs selective HTTP status codes)
- No response body management needed

### Decision Framework

```
START
  │
  ├─ Is this a user-facing request? (API handler, web endpoint)
  │  └─ YES → Use Execute() with immediate fallback
  │
  ├─ Is this HTTP?
  │  ├─ YES → Use ExecuteHTTPBlocking()
  │  │        Benefits: Request factory, status code classification
  │  │
  │  └─ NO → Is this gRPC?
  │           ├─ YES → Use ExecuteGRPCBlocking()
  │           │        Benefits: Clean interface, automatic retry
  │           │
  │           └─ NO → Use ExecuteBlocking()
  │                    Benefits: Generic, works with any operation
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

### Example 8: gRPC Client with ExecuteGRPCBlocking

Complete gRPC client using ExecuteGRPCBlocking for clean, boilerplate-free implementation. Best for background workers and service-to-service calls.

```go
package grpcclient

import (
    "context"
    "fmt"
    "time"

    "github.com/michael-jaquier/circuitbreaker"
    "google.golang.org/grpc"
    pb "your/proto/package"  // Replace with your protobuf package
)

// UserServiceClient wraps a gRPC client with circuit breaker protection
type UserServiceClient struct {
    client  pb.UserServiceClient
    breaker circuitbreaker.CircuitBreaker
}

// NewUserServiceClient creates a new gRPC client with circuit breaker
func NewUserServiceClient(conn *grpc.ClientConn) (*UserServiceClient, error) {
    cb, err := circuitbreaker.NewZeroTolerance(
        circuitbreaker.WithCooldownTimer(30 * time.Second),
        circuitbreaker.WithSuccessToClose(3),
    )
    if err != nil {
        return nil, fmt.Errorf("failed to create circuit breaker: %w", err)
    }

    return &UserServiceClient{
        client:  pb.NewUserServiceClient(conn),
        breaker: cb,
    }, nil
}

// GetUser retrieves a user by ID with automatic retry
func (c *UserServiceClient) GetUser(ctx context.Context, userID string) (*pb.User, error) {
    // ExecuteGRPCBlocking eliminates 30+ lines of boilerplate
    resp, err := c.breaker.ExecuteGRPCBlocking(ctx, func(ctx context.Context) (interface{}, error) {
        return c.client.GetUser(ctx, &pb.GetUserRequest{UserId: userID})
    })
    if err != nil {
        return nil, fmt.Errorf("GetUser failed: %w", err)
    }

    // Type assert the response
    user := resp.(*pb.User)
    return user, nil
}

// ListUsers retrieves all users with pagination
func (c *UserServiceClient) ListUsers(ctx context.Context, pageSize int32) ([]*pb.User, error) {
    resp, err := c.breaker.ExecuteGRPCBlocking(ctx, func(ctx context.Context) (interface{}, error) {
        return c.client.ListUsers(ctx, &pb.ListUsersRequest{
            PageSize: pageSize,
        })
    })
    if err != nil {
        return nil, fmt.Errorf("ListUsers failed: %w", err)
    }

    listResp := resp.(*pb.ListUsersResponse)
    return listResp.Users, nil
}

// CreateUser creates a new user
func (c *UserServiceClient) CreateUser(ctx context.Context, user *pb.User) (*pb.User, error) {
    resp, err := c.breaker.ExecuteGRPCBlocking(ctx, func(ctx context.Context) (interface{}, error) {
        return c.client.CreateUser(ctx, &pb.CreateUserRequest{User: user})
    })
    if err != nil {
        return nil, fmt.Errorf("CreateUser failed: %w", err)
    }

    created := resp.(*pb.User)
    return created, nil
}

// UpdateUser updates an existing user
func (c *UserServiceClient) UpdateUser(ctx context.Context, user *pb.User) (*pb.User, error) {
    resp, err := c.breaker.ExecuteGRPCBlocking(ctx, func(ctx context.Context) (interface{}, error) {
        return c.client.UpdateUser(ctx, &pb.UpdateUserRequest{User: user})
    })
    if err != nil {
        return nil, fmt.Errorf("UpdateUser failed: %w", err)
    }

    updated := resp.(*pb.User)
    return updated, nil
}

// Example usage
func ExampleGRPCUsage() {
    // Create gRPC connection
    conn, err := grpc.Dial(
        "localhost:50051",
        grpc.WithInsecure(), // Use WithTransportCredentials in production
    )
    if err != nil {
        panic(fmt.Sprintf("Failed to connect: %v", err))
    }
    defer conn.Close()

    // Create client with circuit breaker
    client, err := NewUserServiceClient(conn)
    if err != nil {
        panic(err)
    }

    // Context with timeout provides natural retry boundary
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
    defer cancel()

    // Get user - automatically retries when circuit is open
    user, err := client.GetUser(ctx, "user-123")
    if err != nil {
        fmt.Printf("Error: %v\n", err)
        return
    }
    fmt.Printf("User: %+v\n", user)

    // List users
    users, err := client.ListUsers(ctx, 100)
    if err != nil {
        fmt.Printf("Error: %v\n", err)
        return
    }
    fmt.Printf("Found %d users\n", len(users))

    // Create user
    newUser := &pb.User{
        Id:    "user-456",
        Name:  "John Doe",
        Email: "john@example.com",
    }
    created, err := client.CreateUser(ctx, newUser)
    if err != nil {
        fmt.Printf("Error: %v\n", err)
        return
    }
    fmt.Printf("Created user: %+v\n", created)
}
```

#### Comparison: Old vs New Pattern

**OLD PATTERN (30+ lines per method with Execute()):**
```go
func (c *UserServiceClient) GetUser(ctx context.Context, userID string) (*pb.User, error) {
    var resp *pb.User
    var grpcErr error

    // Retry loop with manual timer handling
    retryCtx, retryCancel := context.WithTimeout(context.Background(), 5*time.Minute)
    defer retryCancel()

    for {
        timer, err := c.breaker.Execute(ctx, func(ctx context.Context) error {
            resp, grpcErr = c.client.GetUser(ctx, &pb.GetUserRequest{UserId: userID})
            return grpcErr
        })

        if timer != nil {
            // Circuit is open, wait for timer
            slog.Warn("circuit breaker open - waiting for retry")
            select {
            case <-timer.C:
                continue  // Retry
            case <-retryCtx.Done():
                return nil, fmt.Errorf("retry timeout exceeded")
            }
        }

        if err != nil {
            slog.Error("request failed", "error", err)
            return nil, err
        }

        // Success
        return resp, nil
    }
}
```

**NEW PATTERN (3 lines with ExecuteGRPCBlocking):**
```go
func (c *UserServiceClient) GetUser(ctx context.Context, userID string) (*pb.User, error) {
    resp, err := c.breaker.ExecuteGRPCBlocking(ctx, func(ctx context.Context) (interface{}, error) {
        return c.client.GetUser(ctx, &pb.GetUserRequest{UserId: userID})
    })
    if err != nil {
        return nil, err
    }
    return resp.(*pb.User), nil
}
```

**Benefits:**
- **90% less code**: 3 lines vs 30+ lines
- **No manual timer handling**: Automatic retry when circuit opens
- **Context-aware**: Respects context timeout as natural retry boundary
- **Same protection**: Zero-tolerance circuit breaker with all features
- **Type-safe**: Returns interface{} requiring type assertion

**When to use ExecuteGRPCBlocking:**
- Background workers, batch jobs, cron tasks
- Service-to-service calls in non-user-facing contexts
- When latency from retries is acceptable
- Context timeout provides sufficient retry control

**When NOT to use:**
- User-facing request handlers (use Execute() for immediate response)
- Need custom fallback logic when circuit opens
- Want fine-grained control over retry behavior per call

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

### Template 1B: HTTP Client Template (with ExecuteHTTPBlocking)

Use this for HTTP clients in background/batch contexts with automatic retry and intelligent status classification:

```go
// GET request template
func (c *{{ServiceName}}Client) {{MethodName}}(ctx context.Context{{PARAMS}}) ({{RETURN_TYPE}}, error) {
    // Request factory creates fresh request for each retry
    requestFactory := func() (*http.Request, error) {
        return http.NewRequest("{{HTTP_METHOD}}", c.baseURL+"{{PATH}}", nil)
    }

    // ExecuteHTTPBlocking provides:
    // - Automatic retry when circuit is open
    // - Built-in status code classification (408, 429, 5xx retryable)
    // - Request recreation for each retry
    resp, err := c.breaker.ExecuteHTTPBlocking(ctx, c.client, requestFactory)
    if err != nil {
        return {{ZERO_VALUE}}, fmt.Errorf("request failed: %w", err)
    }
    defer resp.Body.Close()

    {{DECODE_LOGIC}}
    return {{RESULT}}, nil
}

// POST/PUT/PATCH request template with body
func (c *{{ServiceName}}Client) {{MethodName}}(ctx context.Context, payload {{PAYLOAD_TYPE}}) ({{RETURN_TYPE}}, error) {
    // Serialize payload once
    jsonData, err := json.Marshal(payload)
    if err != nil {
        return {{ZERO_VALUE}}, fmt.Errorf("failed to marshal payload: %w", err)
    }

    // Request factory creates fresh request with fresh body for each retry
    requestFactory := func() (*http.Request, error) {
        req, err := http.NewRequest("{{HTTP_METHOD}}", c.baseURL+"{{PATH}}", bytes.NewBuffer(jsonData))
        if err != nil {
            return nil, err
        }
        req.Header.Set("Content-Type", "application/json")
        return req, nil
    }

    resp, err := c.breaker.ExecuteHTTPBlocking(ctx, c.client, requestFactory)
    if err != nil {
        return {{ZERO_VALUE}}, fmt.Errorf("request failed: %w", err)
    }
    defer resp.Body.Close()

    {{DECODE_LOGIC}}
    return {{RESULT}}, nil
}
```

**Key features:**
- **Request factory pattern**: Creates fresh request (and body) for each retry
- **Automatic status classification**: 408, 429, 5xx retryable; other 4xx non-retryable
- **Response management**: Returns full http.Response, caller closes body
- **Simplified error handling**: No timer checking needed

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

### Template 4: gRPC Client with ExecuteGRPCBlocking

Use this for gRPC clients in background/batch contexts:

```go
type {{ServiceName}}Client struct {
    client  pb.{{ServiceName}}Client
    breaker circuitbreaker.CircuitBreaker
}

func New{{ServiceName}}Client(conn *grpc.ClientConn) (*{{ServiceName}}Client, error) {
    cb, err := circuitbreaker.NewZeroTolerance(
        circuitbreaker.WithCooldownTimer({{COOLDOWN_SECONDS}} * time.Second),
        circuitbreaker.WithSuccessToClose({{SUCCESS_COUNT}}),
    )
    if err != nil {
        return nil, fmt.Errorf("failed to create circuit breaker: %w", err)
    }

    return &{{ServiceName}}Client{
        client:  pb.New{{ServiceName}}Client(conn),
        breaker: cb,
    }, nil
}

// Unary RPC method template
func (c *{{ServiceName}}Client) {{MethodName}}(ctx context.Context, {{PARAMS}}) (*pb.{{ResponseType}}, error) {
    resp, err := c.breaker.ExecuteGRPCBlocking(ctx, func(ctx context.Context) (interface{}, error) {
        return c.client.{{MethodName}}(ctx, &pb.{{RequestType}}{
            {{REQUEST_FIELDS}}
        })
    })
    if err != nil {
        return nil, fmt.Errorf("{{MethodName}} failed: %w", err)
    }

    // Type assert the response
    typedResp := resp.(*pb.{{ResponseType}})
    return typedResp, nil
}

// Example with extracted fields
func (c *{{ServiceName}}Client) GetResource(ctx context.Context, id string) (*pb.Resource, error) {
    resp, err := c.breaker.ExecuteGRPCBlocking(ctx, func(ctx context.Context) (interface{}, error) {
        return c.client.GetResource(ctx, &pb.GetResourceRequest{
            Id: id,
        })
    })
    if err != nil {
        return nil, fmt.Errorf("GetResource failed: %w", err)
    }
    return resp.(*pb.Resource), nil
}

// Example with list method
func (c *{{ServiceName}}Client) ListResources(ctx context.Context, pageSize int32) ([]*pb.Resource, error) {
    resp, err := c.breaker.ExecuteGRPCBlocking(ctx, func(ctx context.Context) (interface{}, error) {
        return c.client.ListResources(ctx, &pb.ListResourcesRequest{
            PageSize: pageSize,
        })
    })
    if err != nil {
        return nil, fmt.Errorf("ListResources failed: %w", err)
    }
    listResp := resp.(*pb.ListResourcesResponse)
    return listResp.Resources, nil
}

// Example with create method
func (c *{{ServiceName}}Client) CreateResource(ctx context.Context, resource *pb.Resource) (*pb.Resource, error) {
    resp, err := c.breaker.ExecuteGRPCBlocking(ctx, func(ctx context.Context) (interface{}, error) {
        return c.client.CreateResource(ctx, &pb.CreateResourceRequest{
            Resource: resource,
        })
    })
    if err != nil {
        return nil, fmt.Errorf("CreateResource failed: %w", err)
    }
    return resp.(*pb.Resource), nil
}
```

**Template substitutions:**
- `{{ServiceName}}`: The name of the gRPC service (e.g., "User", "Order", "Product")
- `{{MethodName}}`: The name of the gRPC method (e.g., "GetUser", "ListOrders")
- `{{PARAMS}}`: Method parameters (e.g., "userID string", "pageSize int32")
- `{{RequestType}}`: The protobuf request message type
- `{{ResponseType}}`: The protobuf response message type
- `{{REQUEST_FIELDS}}`: Field assignments for the request (e.g., "UserId: userID,")
- `{{COOLDOWN_SECONDS}}`: Circuit breaker cooldown duration in seconds
- `{{SUCCESS_COUNT}}`: Number of successes required to close circuit

**Key features:**
- **Clean interface**: 3 lines per method vs 30+ with Execute()
- **Type assertion required**: Returns interface{}, must type assert result
- **Automatic retry**: Handles circuit open state transparently
- **Context-aware**: Respects context timeout as retry boundary

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

### 6. DO NOT use blocking methods in user-facing request handlers

WRONG:
```go
// HTTP handler using ExecuteHTTPBlocking - BLOCKS the request handler!
func (s *Server) GetUserHandler(w http.ResponseWriter, r *http.Request) {
    userID := r.URL.Query().Get("id")

    // Request factory for user service call
    requestFactory := func() (*http.Request, error) {
        return http.NewRequest("GET", s.userServiceURL+"/users/"+userID, nil)
    }

    // WRONG: This blocks the HTTP handler thread while waiting for circuit to close
    // User experiences long delays instead of fast failure
    resp, err := s.breaker.ExecuteHTTPBlocking(r.Context(), s.client, requestFactory)
    if err != nil {
        http.Error(w, "User service unavailable", http.StatusServiceUnavailable)
        return
    }
    defer resp.Body.Close()

    // Copy response to user
    io.Copy(w, resp.Body)
}
```

**Why this is wrong:**
- Blocks the request handler for up to several minutes while circuit is open
- User sees hanging requests instead of fast failure
- Wastes server resources keeping connections open
- Poor user experience - no immediate feedback

RIGHT:
```go
// HTTP handler using Execute() - FAILS FAST
func (s *Server) GetUserHandler(w http.ResponseWriter, r *http.Request) {
    userID := r.URL.Query().Get("id")

    req, err := http.NewRequest("GET", s.userServiceURL+"/users/"+userID, nil)
    if err != nil {
        http.Error(w, "Invalid request", http.StatusBadRequest)
        return
    }

    var resp *http.Response
    var httpErr error

    // RIGHT: Check timer immediately and fail fast
    timer, err := s.breaker.Execute(r.Context(), func(ctx context.Context) error {
        resp, httpErr = s.client.Do(req)
        if httpErr != nil {
            return httpErr
        }
        if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
            return fmt.Errorf("server error: %d", resp.StatusCode)
        }
        return nil
    })

    if timer != nil {
        // Circuit is open - return cached data or error immediately
        cached, err := s.cache.Get("user:" + userID)
        if err == nil {
            w.Header().Set("Content-Type", "application/json")
            w.Header().Set("X-Cache", "HIT")
            w.Write(cached)
            return
        }
        http.Error(w, "User service unavailable", http.StatusServiceUnavailable)
        return
    }

    if err != nil {
        http.Error(w, "Request failed", http.StatusBadGateway)
        return
    }
    defer resp.Body.Close()

    // Copy response to user
    io.Copy(w, resp.Body)
}
```

**Why this is right:**
- Returns immediately when circuit is open (fast failure)
- Provides opportunity for fallback logic (cached data)
- Good user experience - fast feedback
- Conserves server resources

**Key principle:**
- **User-facing contexts**: Use Execute() for immediate response
- **Background contexts**: Use blocking methods for automatic retry

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

### Pattern 5: Testing ExecuteHTTPBlocking with httptest

```go
func TestExecuteHTTPBlocking(t *testing.T) {
    // Track request attempts
    attempts := 0
    failuresRemaining := 2

    // Create test server that fails N times then succeeds
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        attempts++

        if failuresRemaining > 0 {
            failuresRemaining--
            w.WriteHeader(http.StatusServiceUnavailable)
            w.Write([]byte("Service temporarily unavailable"))
            return
        }

        w.WriteHeader(http.StatusOK)
        w.Write([]byte("Success"))
    }))
    defer server.Close()

    // Create circuit breaker with fake clock
    fakeClock := &FakeClock{now: time.Now()}
    cb, err := circuitbreaker.NewZeroTolerance(
        circuitbreaker.WithClock(fakeClock),
        circuitbreaker.WithCooldownTimer(5 * time.Second),
        circuitbreaker.WithSuccessToClose(1),
    )
    if err != nil {
        t.Fatal(err)
    }

    client := &http.Client{Timeout: 2 * time.Second}
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    // Request factory
    requestFactory := func() (*http.Request, error) {
        return http.NewRequest("GET", server.URL, nil)
    }

    // Start blocking call in goroutine
    resultChan := make(chan error, 1)
    go func() {
        resp, err := cb.ExecuteHTTPBlocking(ctx, client, requestFactory)
        if err != nil {
            resultChan <- err
            return
        }
        defer resp.Body.Close()

        body, _ := io.ReadAll(resp.Body)
        if string(body) != "Success" {
            resultChan <- fmt.Errorf("unexpected body: %s", string(body))
            return
        }
        resultChan <- nil
    }()

    // Advance time to allow retries when circuit opens
    go func() {
        for i := 0; i < 5; i++ {
            time.Sleep(100 * time.Millisecond)
            fakeClock.Advance(6 * time.Second)
        }
    }()

    // Wait for result
    select {
    case err := <-resultChan:
        if err != nil {
            t.Errorf("ExecuteHTTPBlocking failed: %v", err)
        }
        if attempts < 3 {
            t.Errorf("Expected at least 3 attempts, got %d", attempts)
        }
    case <-time.After(5 * time.Second):
        t.Error("Test timeout - ExecuteHTTPBlocking blocked indefinitely")
    }
}
```

### Pattern 6: Testing ExecuteGRPCBlocking with Mock

```go
// Mock gRPC client for testing
type MockUserServiceClient struct {
    attempts          int
    failuresRemaining int
}

func (m *MockUserServiceClient) GetUser(ctx context.Context, req *pb.GetUserRequest) (*pb.User, error) {
    m.attempts++

    if m.failuresRemaining > 0 {
        m.failuresRemaining--
        return nil, fmt.Errorf("gRPC service unavailable")
    }

    return &pb.User{
        Id:    req.UserId,
        Name:  "Test User",
        Email: "test@example.com",
    }, nil
}

func TestExecuteGRPCBlocking(t *testing.T) {
    // Create circuit breaker with fake clock
    fakeClock := &FakeClock{now: time.Now()}
    cb, err := circuitbreaker.NewZeroTolerance(
        circuitbreaker.WithClock(fakeClock),
        circuitbreaker.WithCooldownTimer(5 * time.Second),
        circuitbreaker.WithSuccessToClose(1),
    )
    if err != nil {
        t.Fatal(err)
    }

    // Create mock client that fails 2 times then succeeds
    mockClient := &MockUserServiceClient{
        failuresRemaining: 2,
    }

    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    // Start blocking call in goroutine
    resultChan := make(chan error, 1)
    go func() {
        resp, err := cb.ExecuteGRPCBlocking(ctx, func(ctx context.Context) (interface{}, error) {
            return mockClient.GetUser(ctx, &pb.GetUserRequest{UserId: "user-123"})
        })
        if err != nil {
            resultChan <- err
            return
        }

        // Type assert the response
        user := resp.(*pb.User)
        if user.Id != "user-123" {
            resultChan <- fmt.Errorf("unexpected user ID: %s", user.Id)
            return
        }
        if user.Name != "Test User" {
            resultChan <- fmt.Errorf("unexpected user name: %s", user.Name)
            return
        }
        resultChan <- nil
    }()

    // Advance time to allow retries when circuit opens
    go func() {
        for i := 0; i < 5; i++ {
            time.Sleep(100 * time.Millisecond)
            fakeClock.Advance(6 * time.Second)
        }
    }()

    // Wait for result
    select {
    case err := <-resultChan:
        if err != nil {
            t.Errorf("ExecuteGRPCBlocking failed: %v", err)
        }
        if mockClient.attempts < 3 {
            t.Errorf("Expected at least 3 attempts, got %d", mockClient.attempts)
        }
    case <-time.After(5 * time.Second):
        t.Error("Test timeout - ExecuteGRPCBlocking blocked indefinitely")
    }
}

// Test context timeout with blocking methods
func TestExecuteGRPCBlockingContextTimeout(t *testing.T) {
    fakeClock := &FakeClock{now: time.Now()}
    cb, err := circuitbreaker.NewZeroTolerance(
        circuitbreaker.WithClock(fakeClock),
        circuitbreaker.WithCooldownTimer(5 * time.Second),
    )
    if err != nil {
        t.Fatal(err)
    }

    // Mock client that always fails
    mockClient := &MockUserServiceClient{
        failuresRemaining: 1000, // Never succeeds
    }

    // Short context timeout
    ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
    defer cancel()

    // Should return error due to context timeout
    _, err = cb.ExecuteGRPCBlocking(ctx, func(ctx context.Context) (interface{}, error) {
        return mockClient.GetUser(ctx, &pb.GetUserRequest{UserId: "user-123"})
    })

    if err == nil {
        t.Error("Expected context timeout error, got nil")
    }

    if !strings.Contains(err.Error(), "context") {
        t.Errorf("Expected context error, got: %v", err)
    }
}
```

**Key testing principles for blocking methods:**

1. **Use goroutines**: Launch blocking calls in goroutines to avoid blocking test execution
2. **Advance FakeClock**: Manually advance time to trigger circuit transitions
3. **Context timeout**: Always use context timeout as safety net
4. **Verify attempts**: Check that retries actually occurred
5. **Test timeout boundary**: Verify blocking respects context deadline

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
- [ ] Appropriate execution method chosen (Execute vs ExecuteBlocking vs ExecuteHTTPBlocking vs ExecuteGRPCBlocking)
- [ ] Execute() used for user-facing requests (APIs, web handlers) with immediate fallback
- [ ] ExecuteHTTPBlocking() used for HTTP clients in background/batch contexts
- [ ] ExecuteGRPCBlocking() used for gRPC clients in background/batch contexts
- [ ] ExecuteBlocking() used for generic operations in background/batch contexts
- [ ] Blocking methods NOT used in user-facing request handlers
- [ ] Circuit open condition is handled with proper fallback strategy (cache, default value, or meaningful error)
- [ ] Error classification is correct (return error from Execute function to mark as failure)
- [ ] Circuit breaker is NOT shared across unrelated services
- [ ] Request factory functions create fresh requests for each retry (when using ExecuteHTTPBlocking)
- [ ] Type assertions applied correctly to gRPC responses (when using ExecuteGRPCBlocking)
- [ ] Context timeout set appropriately to limit retry duration (when using blocking methods)
- [ ] Tests include `FakeClock` usage for time-dependent behavior
- [ ] Tests for blocking methods run in goroutines with timeout safety
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
