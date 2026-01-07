# circuitbreaker

Small Go library implementing the Circuit Breaker pattern, with a focus on simple integration and test coverage.

Repository layout (main branch):
- `circuitbreaker.go`: core circuit breaker implementation
- `circuitbreaker_zerotolerance.go`: “zero tolerance” variant
- `circuitbreaker_http_test.go`: HTTP-focused tests / examples
- `circuitbreaker_test.go`: core unit tests
- `Makefile`: common dev targets
- `go.mod` / `go.sum`: Go module metadata :contentReference[oaicite:0]{index=0}

## What this is for

Use a circuit breaker to stop hammering an unhealthy dependency (HTTP service, DB proxy, upstream API), reduce latency spikes, and prevent cascading failures. :contentReference[oaicite:1]{index=1}

## Install

If you are using Go modules:

```bash
go get github.com/michael-jaquier/circuitbreaker
Basic usage
Import the package and wrap the call you want to protect (usually a remote call). The package is designed to be small and test-driven (see *_test.go). 
GitHub

Example shape (adapt names to the exported API in circuitbreaker.go):

go
Copy code
package main

import (
	"context"
	"errors"
	"time"

	"github.com/michael-jaquier/circuitbreaker"
)

var ErrUpstreamDown = errors.New("upstream unavailable")

func main() {
	_ = time.Second

	// cb := circuitbreaker.New(...)         // create breaker with config
	// result, err := cb.Do(func() error {   // execute protected call
	//     return callUpstream()
	// })
	// if err != nil { ... }
}

func callUpstream() error {
	return ErrUpstreamDown
}
HTTP usage
This repo includes HTTP-oriented tests (circuitbreaker_http_test.go). Use that as the reference for:

wrapping an http.RoundTripper, http.Client, or handler/middleware

classifying which status codes / errors should count as failures 
GitHub

Zero-tolerance mode
The repo contains a dedicated implementation file for a “zero tolerance” breaker (circuitbreaker_zerotolerance.go). This is typically useful when any failure should open the circuit immediately (for example, hard dependencies during startup paths). 
GitHub

Development
Common targets are in Makefile. Typical workflow:

bash
Copy code
go test ./...
If the Makefile defines test, lint, or fmt, prefer those for consistency. 
GitHub

Contributing
Keep behavior covered by unit tests (circuitbreaker_test.go, circuitbreaker_http_test.go).

Prefer small changes with clear failure-mode tests. 
GitHub

License
GPL-3.0. See LICENSE. 
GitHub
