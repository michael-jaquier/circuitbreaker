package circuitbreaker

import (
	"errors"
	"math/rand"
	"net/http"
	"sync/atomic"
	"time"
)

// ErrCircuitOpen is returned when a request is rejected because the circuit breaker is open.
var ErrCircuitOpen = errors.New("circuit breaker is open")

type zeroToleranceCircuitBreaker struct {
	config zeroToleranceConfig
	//lint:ignore U1000 padding prevents false sharing
	prepadding [64]byte
	state      atomic.Int64
	//lint:ignore U1000 padding prevents false sharing
	postpadding  [56]byte
	failureCount atomic.Int64
	successCount atomic.Int64
	windowStart  atomic.Int64
	openedAt     atomic.Int64
	probes       atomic.Int64
	clock        Clock
	client       *http.Client
}

func newZeroToleranceCircuitBreaker(c zeroToleranceConfig) *zeroToleranceCircuitBreaker {
	r := &zeroToleranceCircuitBreaker{config: c,
		clock:  c.clock,
		client: &http.Client{Timeout: 10 * time.Second},
	}
	r.state.Store(int64(Closed))
	r.probes.Store(c.maximumProbes)
	r.windowStart.Store(c.clock.Now().UnixNano())
	return r
}

func (cb *zeroToleranceCircuitBreaker) maybeHalfOpen() {
	cs := State(cb.state.Load())
	if cs == Open {
		if cb.clock.Now().UnixNano()-cb.openedAt.Load() > cb.config.cooldownTimer {
			cb.toState(HalfOpen)
		}
	}
}

func (cb *zeroToleranceCircuitBreaker) Request() (bool, time.Duration) {
	cb.maybeHalfOpen()

	switch State(cb.state.Load()) {
	case Open:
		now := cb.clock.Now().UnixNano()
		wait := cb.config.cooldownTimer - (now - cb.openedAt.Load())
		return false, time.Duration(wait)
	case HalfOpen:
		return cb.tryAcquireProbe()
	case Closed:
		return true, time.Duration(0)
	}
	return true, time.Duration(0)
}

func (cb *zeroToleranceCircuitBreaker) tryAcquireProbe() (bool, time.Duration) {
	probes := cb.probes.Load()
	if probes <= 0 {
		jitter := max(10, rand.Int63n(100)) // #nosec G404 -- non-cryptographic jitter for backoff timing
		return false, time.Duration(jitter) * time.Millisecond
	}

	if cb.probes.CompareAndSwap(probes, probes-1) {
		return true, time.Duration(0)
	}

	jitter := max(10, rand.Int63n(100)) // #nosec G404 -- non-cryptographic jitter for backoff timing
	return false, time.Duration(jitter) * time.Millisecond
}

func (cb *zeroToleranceCircuitBreaker) releaseProbe() {
	for {
		cp := cb.probes.Load()
		if cp >= cb.config.maximumProbes {
			break
		}
		if cb.probes.CompareAndSwap(cp, cp+1) {
			break
		}
	}

}

func (cb *zeroToleranceCircuitBreaker) toState(newState State) {
	cb.state.Store(int64(newState))

	switch newState {
	case Open:
		cb.openedAt.Store(cb.clock.Now().UnixNano())
		cb.probes.Store(cb.config.maximumProbes)
	case HalfOpen:
		cb.probes.Store(cb.config.maximumProbes)
		cb.failureCount.Store(0)
		cb.successCount.Store(0)
	case Closed:
		cb.probes.Store(cb.config.maximumProbes)
	}
}

func (cb *zeroToleranceCircuitBreaker) shouldClose() bool {
	return State(cb.state.Load()) == HalfOpen && cb.successCount.Load() >= cb.config.successToClose
}

func (cb *zeroToleranceCircuitBreaker) shouldOpen() bool {
	now := cb.clock.Now().UnixNano()
	if now-cb.windowStart.Load() > cb.config.windowSize {
		cb.successCount.Store(0)
		cb.failureCount.Store(0)
		cb.windowStart.Store(now)
	}
	// Zero tolerance: any failure opens the circuit
	return cb.failureCount.Load() >= 1
}

func (cb *zeroToleranceCircuitBreaker) ReportFailure() {
	cb.failureCount.Add(1)

	// In HalfOpen state, any failure immediately reopens
	if State(cb.state.Load()) == HalfOpen {
		cb.toState(Open)
		return
	}

	// In Closed state, check if we should open
	if cb.shouldOpen() {
		cb.toState(Open)
	}
}

func (cb *zeroToleranceCircuitBreaker) ReportSuccess() {
	cb.successCount.Add(1)
	if State(cb.state.Load()) == HalfOpen {
		cb.releaseProbe()
	}
	if cb.shouldClose() {
		cb.toState(Closed)
	}
}

func (cb *zeroToleranceCircuitBreaker) HTTPRequest(r *http.Request) (*http.Response, error) {
	allowed, _ := cb.Request()
	if !allowed {
		return nil, ErrCircuitOpen
	}

	resp, err := cb.client.Do(r)
	if err != nil {
		cb.ReportFailure()
		return resp, err
	}

	if cb.config.httpErrors(resp.StatusCode) {
		cb.ReportFailure()
	} else {
		cb.ReportSuccess()
	}

	return resp, nil
}
