# circuitbreaker

Small Go library implementing the Circuit Breaker pattern, with a focus on simple integration and test coverage.

Repository layout (main branch):
- `circuitbreaker.go`: core circuit breaker implementation
- `options.go`: configuration options for circuit breakers
- `clock.go`: clock interface for testing
- `circuitbreaker_http_test.go`: HTTP-focused tests / examples
- `circuitbreaker_test.go`: core unit tests
- `Makefile`: common dev targets
- `go.mod` / `go.sum`: Go module metadata

## What this is for

Use a circuit breaker to stop hammering an unhealthy dependency (HTTP service, DB proxy, upstream API), reduce latency spikes, and prevent cascading failures. :contentReference[oaicite:1]{index=1}

## Install

If you are using Go modules:

```bash
go get github.com/michael-jaquier/circuitbreaker
```

## Basic usage

Import the package and wrap the call you want to protect (usually a remote call):

```go
package main

import (
	"context"
	"errors"
	"time"

	"github.com/michael-jaquier/circuitbreaker"
)

var ErrUpstreamDown = errors.New("upstream unavailable")

func main() {
	// Create breaker with default config
	cb, err := circuitbreaker.New()
	if err != nil {
		panic(err)
	}

	// Execute protected call
	timer, err := cb.Execute(context.Background(), func(ctx context.Context) error {
		return callUpstream()
	})

	if timer != nil {
		// Circuit is open, service unavailable
		panic("circuit breaker is open")
	}
	if err != nil {
		// Operation failed
		panic(err)
	}
}

func callUpstream() error {
	return ErrUpstreamDown
}
```

## Blocking execution

For simpler usage when you want to automatically wait when the circuit is open, use `ExecuteBlocking`:

```go
cb, _ := circuitbreaker.New()

// ExecuteBlocking automatically waits when circuit is open
// and returns when the function succeeds or the context is cancelled
err := cb.ExecuteBlocking(context.Background(), func(ctx context.Context) error {
	return callUpstream()
})

if err != nil {
	// Either the operation failed or context was cancelled
	panic(err)
}
```

The difference:
- `Execute()` returns `(*time.Timer, error)` - you handle the timer
- `ExecuteBlocking()` returns `error` - automatically waits on timer, respects context cancellation

## HTTP usage

See `examples/http_client/main.go` for a complete HTTP client example. The Execute method wraps HTTP requests:

```go
var resp *http.Response
var httpErr error

timer, err := cb.Execute(req.Context(), func(ctx context.Context) error {
    client := &http.Client{Timeout: 10 * time.Second}
    resp, httpErr = client.Do(req)
    if httpErr != nil {
        return httpErr
    }

    // Classify which status codes count as failures
    if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
        return fmt.Errorf("server error: %d", resp.StatusCode)
    }
    return nil
})

if timer != nil {
    return nil, circuitbreaker.ErrCircuitOpen
}
```

## Zero-tolerance mode

Use `NewZeroTolerance()` to create a circuit breaker where any single failure opens the circuit immediately:

```go
cb, err := circuitbreaker.NewZeroTolerance(
    circuitbreaker.WithCooldownTimer(60 * time.Second),
    circuitbreaker.WithSuccessToClose(5),
)
```

This is a convenience constructor that sets `failureThreshold=1`. Useful for hard dependencies during startup paths where any failure should immediately stop requests.

## Development

Run tests:

```bash
go test ./...
```

## Contributing

Keep behavior covered by unit tests (circuitbreaker_test.go, circuitbreaker_http_test.go). Prefer small changes with clear failure-mode tests.

## License

GPL-3.0. See LICENSE.
