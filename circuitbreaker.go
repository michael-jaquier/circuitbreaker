package circuitbreaker

import (
	"context"
	"fmt"
	"math/rand"
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
