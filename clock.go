package circuitbreaker

import "time"

// Clock provides time operations for testing and production use.
// This interface is exported to allow custom time implementations in tests.
type Clock interface {
	Now() time.Time
	Sleep(time.Duration)
	After(time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) Sleep(t time.Duration)                  { time.Sleep(t) }
func (realClock) After(t time.Duration) <-chan time.Time { return time.After(t) }
