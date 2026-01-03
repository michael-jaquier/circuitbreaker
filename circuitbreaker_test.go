package circuitbreaker

import (
	"testing"
	"time"
)

var AllOptions = []string{
	"Cooldown",
	"FailureCount",
	"FailToOpen",
	"ResetTimer",
}

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

func TestZeroToleranceMode(t *testing.T) {
	tests := []struct {
		name          string
		setupActions  func(cb CircuitBreaker, clock *FakeClock)
		expectedState State
		expectedAllow bool
		description   string
	}{
		{
			name: "closed_state_allows_requests",
			setupActions: func(cb CircuitBreaker, clock *FakeClock) {
				// No actions - circuit starts closed
			},
			expectedState: Closed,
			expectedAllow: true,
			description:   "Circuit breaker should allow requests in closed state",
		},
		{
			name: "opens_after_single_failure",
			setupActions: func(cb CircuitBreaker, clock *FakeClock) {
				// Zero tolerance: 1 failure = open
				cb.ReportFailure()
			},
			expectedState: Open,
			expectedAllow: false,
			description:   "Circuit should open after single failure",
		},
		{
			name: "blocks_requests_when_open",
			setupActions: func(cb CircuitBreaker, clock *FakeClock) {
				cb.ReportFailure()
			},
			expectedState: Open,
			expectedAllow: false,
			description:   "Circuit should block requests when open",
		},
		{
			name: "transitions_to_halfopen_after_cooldown",
			setupActions: func(cb CircuitBreaker, clock *FakeClock) {
				cb.ReportFailure()
				clock.Advance(121 * time.Second) // Cooldown is 120s
			},
			expectedState: HalfOpen,
			expectedAllow: true,
			description:   "Circuit should transition to half-open after cooldown expires",
		},
		{
			name: "halfopen_allows_limited_probes",
			setupActions: func(cb CircuitBreaker, clock *FakeClock) {
				cb.ReportFailure()
				clock.Advance(121 * time.Second)
			},
			expectedState: HalfOpen,
			expectedAllow: true,
			description:   "Half-open state should allow probe requests",
		},
		{
			name: "closes_after_sufficient_successes",
			setupActions: func(cb CircuitBreaker, clock *FakeClock) {
				cb.ReportFailure()
				clock.Advance(121 * time.Second)
				cb.Request() // Move to half-open
				for range 5 {
					cb.ReportSuccess()
				}
			},
			expectedState: Closed,
			expectedAllow: true,
			description:   "Circuit should close after sufficient successes in half-open",
		},
		{
			name: "stays_open_during_cooldown",
			setupActions: func(cb CircuitBreaker, clock *FakeClock) {
				cb.ReportFailure()
				clock.Advance(60 * time.Second) // Half of cooldown period
				cb.Request()
			},
			expectedState: Open,
			expectedAllow: false,
			description:   "Circuit should stay open if cooldown hasn't expired",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClock := &FakeClock{now: time.Now()}
			cb, err := NewZeroTolerance(WithClock(fakeClock))
			if err != nil {
				t.Fatalf("Failed to create circuit breaker: %v", err)
			}

			tt.setupActions(cb, fakeClock)

			allowed, waitTime := cb.Request()

			if allowed != tt.expectedAllow {
				t.Errorf("%s: expected allowed=%v, got %v (wait=%v)",
					tt.description, tt.expectedAllow, allowed, waitTime)
			}

			ztcb := cb.(*zeroToleranceCircuitBreaker)
			actualState := State(ztcb.state.Load())

			if actualState != tt.expectedState {
				t.Errorf("%s: expected state=%v, got %v",
					tt.description, tt.expectedState, actualState)
			}
		})
	}
}

func TestZeroToleranceProbeExhaustion(t *testing.T) {
	fakeClock := &FakeClock{now: time.Now()}
	cb, err := NewZeroTolerance(WithClock(fakeClock))
	if err != nil {
		t.Fatalf("Failed to create circuit breaker: %v", err)
	}

	// Open the circuit
	cb.ReportFailure()

	// Advance past cooldown to half-open
	fakeClock.Advance(121 * time.Second)

	ztcb := cb.(*zeroToleranceCircuitBreaker)

	initialProbes := int64(1)
	allowedCount := 0

	// Try to acquire more probes than available
	for i := int64(0); i < initialProbes+1; i++ {
		allowed, _ := cb.Request()
		if allowed {
			allowedCount++
		}
	}

	if State(ztcb.state.Load()) != HalfOpen {
		t.Errorf("Circuit should still be half-open, got %v", State(ztcb.state.Load()))
	}

	if allowedCount != int(initialProbes) {
		t.Errorf("Expected %d probes to be allowed, got %d", initialProbes, allowedCount)
	}

	remainingProbes := ztcb.probes.Load()
	if remainingProbes != 0 {
		t.Errorf("Expected 0 probes remaining, got %d", remainingProbes)
	}
}

func TestZeroToleranceProbeReleaseOnSuccess(t *testing.T) {
	fakeClock := &FakeClock{now: time.Now()}
	cb, err := NewZeroTolerance(WithClock(fakeClock))
	if err != nil {
		t.Fatalf("Failed to create circuit breaker: %v", err)
	}

	// Open the circuit
	cb.ReportFailure()

	// Advance past cooldown to half-open
	fakeClock.Advance(121 * time.Second)

	ztcb := cb.(*zeroToleranceCircuitBreaker)

	// With maximumProbes=1 and successToClose=5, we need probe release to work
	successCount := 0
	for successCount < int(ztcb.config.successToClose) {
		allowed, _ := cb.Request()
		if !allowed {
			t.Fatalf("Request %d was blocked, probes not being released properly", successCount+1)
		}
		cb.ReportSuccess()
		successCount++

		// Verify still in half-open until we hit successToClose
		if successCount < int(ztcb.config.successToClose) {
			if State(ztcb.state.Load()) != HalfOpen {
				t.Errorf("After %d successes, should still be half-open, got %v", successCount, State(ztcb.state.Load()))
			}
		}
	}

	// After successToClose successes, should be closed
	if State(ztcb.state.Load()) != Closed {
		t.Errorf("After %d successes, should be closed, got %v", successCount, State(ztcb.state.Load()))
	}

	// Probes should be reset to maximum
	if ztcb.probes.Load() != ztcb.config.maximumProbes {
		t.Errorf("Expected probes to be reset to %d, got %d", ztcb.config.maximumProbes, ztcb.probes.Load())
	}
}

func TestZeroToleranceHalfOpenSingleFailureReopens(t *testing.T) {
	fakeClock := &FakeClock{now: time.Now()}
	cb, err := NewZeroTolerance(WithClock(fakeClock))
	if err != nil {
		t.Fatalf("Failed to create circuit breaker: %v", err)
	}

	// Open the circuit
	cb.ReportFailure()

	// Advance past cooldown to half-open
	fakeClock.Advance(121 * time.Second)

	ztcb := cb.(*zeroToleranceCircuitBreaker)

	// First request in half-open
	allowed, _ := cb.Request()
	if !allowed {
		t.Fatal("First request should be allowed in half-open")
	}

	// Zero tolerance: single failure in half-open immediately reopens
	cb.ReportFailure()

	if State(ztcb.state.Load()) != Open {
		t.Errorf("Should be open after 1 failure in half-open (zero tolerance), got %v", State(ztcb.state.Load()))
	}
}
