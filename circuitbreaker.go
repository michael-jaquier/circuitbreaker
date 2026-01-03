package circuitbreaker

import (
	"fmt"
	"net/http"
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

// Clock provides time operations for testing and production use.
type Clock interface {
	Now() time.Time
	Sleep(time.Duration)
	After(time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) Sleep(t time.Duration)                  { time.Sleep(t) }
func (realClock) After(t time.Duration) <-chan time.Time { return time.After(t) }

type zeroToleranceConfig struct {
	resetTimer     int64
	cooldownTimer  int64
	successToClose int64
	windowSize     int64
	maximumProbes  int64
	clock          Clock
	httpErrors     func(int) bool
}

func defaultZeroToleranceConfig() zeroToleranceConfig {
	return zeroToleranceConfig{
		resetTimer:     int64(60 * time.Second),
		cooldownTimer:  int64(120 * time.Second),
		successToClose: 5,
		windowSize:     int64(240 * time.Second),
		maximumProbes:  1,
		clock:          realClock{},
		httpErrors:     func(sc int) bool { return (sc >= 400 && sc <= 599) },
	}
}

// CircuitBreaker manages request flow and failure handling.
type CircuitBreaker interface {
	Request() (bool, time.Duration)
	ReportSuccess()
	ReportFailure()
	HTTPRequest(*http.Request) (*http.Response, error)
}

// ZeroToleranceOption configures a zero tolerance circuit breaker.
type ZeroToleranceOption func(*zeroToleranceConfig) error

// WithClock sets a custom clock for the circuit breaker.
func WithClock(clock Clock) ZeroToleranceOption {
	return func(c *zeroToleranceConfig) error {
		c.clock = clock
		return nil
	}
}

// WithCustomHTTPErrors sets a custom function to determine which HTTP status codes are errors.
func WithCustomHTTPErrors(httpErrors func(int) bool) ZeroToleranceOption {
	return func(c *zeroToleranceConfig) error {
		c.httpErrors = httpErrors
		return nil
	}
}

// WithWindowSize sets the time window for tracking failures.
func WithWindowSize(windowSize time.Duration) ZeroToleranceOption {
	return func(c *zeroToleranceConfig) error {
		if windowSize <= 0 {
			return fmt.Errorf("windowSize must be >0")
		}
		c.windowSize = windowSize.Nanoseconds()
		return nil
	}
}

// WithMaximumProbes sets the maximum number of concurrent probes in half-open state.
func WithMaximumProbes(probes int64) ZeroToleranceOption {
	return func(c *zeroToleranceConfig) error {
		if probes <= 0 {
			return fmt.Errorf("maximum probes must be >0")
		}
		c.maximumProbes = probes
		return nil
	}
}

// WithSuccessToClose sets the number of successes required to close the circuit.
func WithSuccessToClose(successToClose int64) ZeroToleranceOption {
	return func(c *zeroToleranceConfig) error {
		if successToClose <= 0 {
			return fmt.Errorf("successToClose must be >0")
		}
		c.successToClose = successToClose
		return nil
	}
}

// WithCooldownTimer sets the duration before transitioning from open to half-open.
func WithCooldownTimer(timer time.Duration) ZeroToleranceOption {
	return func(c *zeroToleranceConfig) error {
		if timer <= 0 {
			return fmt.Errorf("timer must be >0")
		}
		c.cooldownTimer = int64(timer.Nanoseconds())
		return nil
	}
}

// WithResetTimer sets the duration before resetting the failure window.
func WithResetTimer(timer time.Duration) ZeroToleranceOption {
	return func(c *zeroToleranceConfig) error {
		if timer <= 0 {
			return fmt.Errorf("timer must be >0")
		}
		c.resetTimer = int64(timer.Nanoseconds())
		return nil
	}
}

// NewZeroTolerance creates a new zero tolerance circuit breaker with the given options.
func NewZeroTolerance(opts ...ZeroToleranceOption) (CircuitBreaker, error) {
	c := defaultZeroToleranceConfig()
	for _, opt := range opts {
		if err := opt(&c); err != nil {
			return nil, fmt.Errorf("unable to apply configuration: %w", err)
		}
	}
	return newZeroToleranceCircuitBreaker(c), nil
}
