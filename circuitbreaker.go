package circuitbreaker

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"sync/atomic"
	"time"
)

// State represents the current state of a circuit breaker.
type State int64

// Circuit breaker states.
const (
	Closed State = iota
	Open
	HalfOpen
)

// CircuitBreaker manages request flow and failure handling.
type CircuitBreaker interface {
	Execute(context.Context, func(context.Context) error) (*time.Timer, error)
	ExecuteBlocking(context.Context, func(context.Context) error) error
	ExecuteHTTPBlocking(context.Context, *http.Client, func() (*http.Request, error)) (*http.Response, error)
	ExecuteGRPCBlocking(context.Context, func(context.Context) (interface{}, error)) (interface{}, error)
	Close()
}

type circuitBreaker struct {
	config config
	//lint:ignore U1000 padding prevents false sharing
	prepadding [64]byte
	state      atomic.Int64
	//lint:ignore U1000 padding prevents false sharing
	postpadding      [56]byte
	clock            Clock
	probeSem         chan struct{}
	failureCount     atomic.Int64
	successCount     atomic.Int64
	cooldown         int64
	halfOpenWhen     atomic.Int64
	cancelTransition context.CancelFunc
}

func (cb *circuitBreaker) monitorStateTransitions(ctx context.Context) {
	windowTicker := time.NewTicker(time.Duration(cb.config.windowSize))
	defer windowTicker.Stop()

	for {
		select {
		case <-windowTicker.C:
			if State(cb.state.Load()) == Closed {
				cb.failureCount.Store(0)
				cb.successCount.Store(0)
			}
		case <-ctx.Done():
			return
		}
	}
}

// New creates a new circuit breaker with the given options.
func New(opts ...Option) (CircuitBreaker, error) {
	c := defaultConfig()
	for _, opt := range opts {
		if err := opt(&c); err != nil {
			return nil, fmt.Errorf("unable to apply configuration: %w", err)
		}
	}
	return newCircuitBreaker(c), nil
}

// NewZeroTolerance creates a new zero tolerance circuit breaker with the given options.
// Zero tolerance means any single failure will open the circuit.
func NewZeroTolerance(opts ...Option) (CircuitBreaker, error) {
	opts = append([]Option{WithFailureThreshold(1)}, opts...)
	return New(opts...)
}

func newCircuitBreaker(c config) *circuitBreaker {
	ctx, cancel := context.WithCancel(context.Background())
	r := &circuitBreaker{
		config:           c,
		clock:            c.clock,
		probeSem:         make(chan struct{}, c.maximumProbes),
		cooldown:         c.cooldownTimer,
		cancelTransition: cancel,
	}
	r.state.Store(int64(Closed))
	go r.monitorStateTransitions(ctx)
	return r
}

type allowResult struct {
	allowed  bool
	hasProbe bool
	timer    *time.Timer
}

func (cb *circuitBreaker) allow() allowResult {
	state := State(cb.state.Load())
	switch state {
	case Closed:
		return allowResult{allowed: true}
	case HalfOpen:
		select {
		case cb.probeSem <- struct{}{}:
			return allowResult{allowed: true, hasProbe: true}
		default:
			return allowResult{allowed: false,
				timer: time.NewTimer(time.Duration(rand.Intn(90)) * time.Millisecond)} // #nosec G404
		}
	case Open:
		halfOpenAt := cb.halfOpenWhen.Load()
		now := cb.clock.Now().UnixNano()
		if now >= halfOpenAt {
			if cb.state.CompareAndSwap(int64(Open), int64(HalfOpen)) {
				select {
				case cb.probeSem <- struct{}{}:
					return allowResult{allowed: true, hasProbe: true}
				default:
					// Shouldn't happen since we just transitioned
					return allowResult{allowed: false, timer: time.NewTimer(time.Duration(rand.Intn(90)) * time.Millisecond)} // #nosec G404
				}
			}
			// Someone else transitioned, retry
			return cb.allow()
		}
		waitDuration := time.Duration(halfOpenAt - now)
		return allowResult{allowed: false, timer: time.NewTimer(waitDuration)}
	default:
		return allowResult{allowed: true}
	}
}

func (cb *circuitBreaker) ExecuteBlocking(
	ctx context.Context, fn func(context.Context) error) error {
	for {
		timer, err := cb.Execute(ctx, fn)

		// Handle success/error immediately
		if timer == nil {
			return err
		}

		// Wait for circuit to potentially allow retry
		select {
		case <-timer.C:
			continue
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
}

// ExecuteHTTPBlocking executes HTTP requests with circuit breaker protection and automatic retry.
// It automatically classifies HTTP response codes and retries on retryable failures.
//
// Classification:
// - 2xx/3xx: Success
// - 408 (Request Timeout), 429 (Too Many Requests), 5xx: Retryable, opens circuit
// - Other 4xx: Non-retryable, returns immediately without opening circuit
// - Network errors: Retryable, opens circuit
//
// Parameters:
//   - ctx: Overall deadline context that cancels all retry attempts
//   - client: HTTP client to use for requests (uses client.Timeout for per-request timeout)
//   - requestFactory: Function that returns a new *http.Request for each attempt
//
// Returns:
//   - *http.Response: Response with open body (caller must close)
//   - error: nil on success, error on failure or context cancellation
//
// Response body handling:
//   - Success: Returns response with open body, caller must close
//   - Retryable failure: Drains and closes body before retry
//   - Non-retryable failure: Returns response with open body, caller must close
//   - No response (network error): Returns nil response
func (cb *circuitBreaker) ExecuteHTTPBlocking(
	ctx context.Context,
	client *http.Client,
	requestFactory func() (*http.Request, error),
) (*http.Response, error) {
	var lastResp *http.Response
	var lastErr error
	var wasRetryable bool

	for {
		// Create fresh request for this attempt
		req, err := requestFactory()
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		// Apply context to request
		req = req.WithContext(ctx)

		// Attempt execution through circuit breaker
		timer, execErr := cb.Execute(ctx, func(attemptCtx context.Context) error {
			resp, httpErr := client.Do(req)

			// Network error - retryable
			if httpErr != nil {
				lastResp = nil
				lastErr = httpErr
				wasRetryable = true
				return httpErr
			}

			statusCode := resp.StatusCode

			// Success: 2xx, 3xx
			if statusCode >= 200 && statusCode < 400 {
				lastResp = resp
				lastErr = nil
				wasRetryable = false
				return nil
			}

			// Retryable: 408, 429, 5xx
			if statusCode == 408 || statusCode == 429 ||
				(statusCode >= 500 && statusCode <= 599) {
				// Drain and close body to allow retry
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()

				lastResp = nil
				lastErr = fmt.Errorf("retryable HTTP error: status %d", statusCode)
				wasRetryable = true
				return lastErr // Opens circuit
			}

			// Non-retryable 4xx: return without opening circuit
			lastResp = resp
			lastErr = fmt.Errorf("non-retryable HTTP error: status %d", statusCode)
			wasRetryable = false
			return nil // Don't open circuit
		})

		// If Execute returned a timer, circuit is open - wait for it
		if timer != nil {
			select {
			case <-timer.C:
				continue // Circuit allowing retry
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			}
		}

		// No timer returned - operation completed
		// Success: return response
		if execErr == nil {
			return lastResp, lastErr
		}

		// Error occurred
		// If retryable, continue to next iteration to check circuit state
		// If non-retryable, return immediately
		if !wasRetryable {
			return lastResp, lastErr
		}
	}
}

// ExecuteGRPCBlocking executes gRPC calls with circuit breaker protection and automatic retry.
// It handles the retry loop and circuit breaker state, returning the response directly.
//
// Usage:
//
//	resp, err := cb.ExecuteGRPCBlocking(ctx, func(ctx context.Context) (interface{}, error) {
//	    return grpcClient.SomeMethod(ctx, req)
//	})
//	if err != nil {
//	    // Circuit breaker could not complete the request
//	    return nil, err
//	}
//	typedResp := resp.(*pb.SomeMethodResponse)
//
// Parameters:
//   - ctx: Overall deadline context that cancels all retry attempts
//   - fn: Function that executes the gRPC call and returns (response, error)
//
// Returns:
//   - interface{}: gRPC response (caller must type assert to specific response type)
//   - error: nil on success, error if circuit breaker exhausted retries or context cancelled
func (cb *circuitBreaker) ExecuteGRPCBlocking(
	ctx context.Context,
	fn func(context.Context) (interface{}, error),
) (interface{}, error) {
	var lastResp interface{}
	var lastErr error

	for {
		// Check context before attempting
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		// Attempt execution through circuit breaker
		timer, _ := cb.Execute(ctx, func(attemptCtx context.Context) error {
			resp, grpcErr := fn(attemptCtx)
			lastResp = resp
			lastErr = grpcErr
			return grpcErr
		})

		// Circuit is open - wait for cooldown or context cancellation
		if timer != nil {
			select {
			case <-timer.C:
				continue // Retry after cooldown
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			}
		}

		// Operation completed - return if success
		if lastErr == nil {
			return lastResp, nil
		}

		// Error - continue to retry (circuit breaker will enforce backoff)
		continue
	}
}

func (cb *circuitBreaker) Execute(
	ctx context.Context,
	fn func(context.Context) error) (*time.Timer, error) {
	ar := cb.allow()
	if !ar.allowed {
		return ar.timer, nil
	}

	err := fn(ctx)

	state := State(cb.state.Load())

	if err != nil {
		failures := cb.failureCount.Add(1)

		if state == Closed && failures >= cb.config.failureThreshold {
			cb.toState(Open)
		} else if state == HalfOpen {
			cb.toState(Open)
		}
	} else {
		successes := cb.successCount.Add(1)

		if state == HalfOpen && successes >= cb.config.successToClose {
			cb.toState(Closed)
		}
	}

	if ar.hasProbe {
		cb.releaseProbe()
	}

	return nil, err
}

func (cb *circuitBreaker) releaseProbe() {
	<-cb.probeSem
}

func (cb *circuitBreaker) toState(newState State) {
	cb.state.Store(int64(newState))
	cb.failureCount.Store(0)
	cb.successCount.Store(0)
	if newState == Open {
		halfOpenAt := cb.clock.Now().Add(time.Duration(cb.cooldown)).UnixNano()
		cb.halfOpenWhen.Store(halfOpenAt)
	}
}

// Close stops the background state monitoring goroutine.
func (cb *circuitBreaker) Close() {
	if cb.cancelTransition != nil {
		cb.cancelTransition()
	}
}
