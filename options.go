package circuitbreaker

import (
	"fmt"
	"time"
)

type config struct {
	resetTimer       int64
	cooldownTimer    int64
	successToClose   int64
	windowSize       int64
	maximumProbes    int64
	failureThreshold int64
	clock            Clock
}

func defaultConfig() config {
	return config{
		resetTimer:       int64(60 * time.Second),
		cooldownTimer:    int64(120 * time.Second),
		successToClose:   5,
		windowSize:       int64(240 * time.Second),
		maximumProbes:    1,
		failureThreshold: 3,
		clock:            realClock{},
	}
}

// Option configures a circuit breaker.
type Option func(*config) error

// WithClock sets a custom clock for the circuit breaker.
func WithClock(clock Clock) Option {
	return func(c *config) error {
		c.clock = clock
		return nil
	}
}

// WithWindowSize sets the time window for tracking failures.
func WithWindowSize(windowSize time.Duration) Option {
	return func(c *config) error {
		if windowSize <= 0 {
			return fmt.Errorf("windowSize must be >0")
		}
		c.windowSize = windowSize.Nanoseconds()
		return nil
	}
}

// WithMaximumProbes sets the maximum number of concurrent probes in half-open state.
func WithMaximumProbes(probes int64) Option {
	return func(c *config) error {
		if probes <= 0 {
			return fmt.Errorf("maximum probes must be >0")
		}
		c.maximumProbes = probes
		return nil
	}
}

// WithSuccessToClose sets the number of successes required to close the circuit.
func WithSuccessToClose(successToClose int64) Option {
	return func(c *config) error {
		if successToClose <= 0 {
			return fmt.Errorf("successToClose must be >0")
		}
		c.successToClose = successToClose
		return nil
	}
}

// WithCooldownTimer sets the duration before transitioning from open to half-open.
func WithCooldownTimer(timer time.Duration) Option {
	return func(c *config) error {
		if timer <= 0 {
			return fmt.Errorf("timer must be >0")
		}
		c.cooldownTimer = int64(timer.Nanoseconds())
		return nil
	}
}

// WithResetTimer sets the duration before resetting the failure window.
func WithResetTimer(timer time.Duration) Option {
	return func(c *config) error {
		if timer <= 0 {
			return fmt.Errorf("timer must be >0")
		}
		c.resetTimer = int64(timer.Nanoseconds())
		return nil
	}
}

// WithFailureThreshold sets the number of failures required to open the circuit.
func WithFailureThreshold(threshold int64) Option {
	return func(c *config) error {
		if threshold <= 0 {
			return fmt.Errorf("threshold must be >0")
		}
		c.failureThreshold = threshold
		return nil
	}
}
