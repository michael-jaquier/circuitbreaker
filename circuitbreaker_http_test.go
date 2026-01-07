package circuitbreaker

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHttpRequest_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	}))
	defer server.Close()

	fakeClock := &FakeClock{now: time.Now()}
	cb, err := NewZeroTolerance(WithClock(fakeClock))
	if err != nil {
		t.Fatalf("Failed to create circuit breaker: %v", err)
	}

	req, _ := http.NewRequest("GET", server.URL, nil)
	resp, err := cb.HTTPRequest(req)

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if resp == nil {
		t.Fatal("Expected response, got nil")
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	ztcb := cb.(*zeroToleranceCircuitBreaker)
	if State(ztcb.state.Load()) != Closed {
		t.Errorf("Circuit should remain closed after success, got %v", State(ztcb.state.Load()))
	}
}

func TestHttpRequest_4xxError(t *testing.T) {
	testCases := []struct {
		name       string
		statusCode int
	}{
		{"400 Bad Request", http.StatusBadRequest},
		{"401 Unauthorized", http.StatusUnauthorized},
		{"403 Forbidden", http.StatusForbidden},
		{"404 Not Found", http.StatusNotFound},
		{"429 Too Many Requests", http.StatusTooManyRequests},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
			}))
			defer server.Close()

			fakeClock := &FakeClock{now: time.Now()}
			cb, err := NewZeroTolerance(WithClock(fakeClock))
			if err != nil {
				t.Fatalf("Failed to create circuit breaker: %v", err)
			}

			req, _ := http.NewRequest("GET", server.URL, nil)
			resp, err := cb.HTTPRequest(req)

			if err != nil {
				t.Errorf("Expected no error for %d status, got %v", tc.statusCode, err)
			}
			if resp == nil {
				t.Fatal("Expected response, got nil")
			}
			if resp.StatusCode != tc.statusCode {
				t.Errorf("Expected status %d, got %d", tc.statusCode, resp.StatusCode)
			}

			ztcb := cb.(*zeroToleranceCircuitBreaker)
			if State(ztcb.state.Load()) != Open {
				t.Errorf("Circuit should open after %d error (zero tolerance), got %v", tc.statusCode, State(ztcb.state.Load()))
			}
		})
	}
}

func TestHttpRequest_5xxError(t *testing.T) {
	testCases := []struct {
		name       string
		statusCode int
	}{
		{"500 Internal Server Error", http.StatusInternalServerError},
		{"502 Bad Gateway", http.StatusBadGateway},
		{"503 Service Unavailable", http.StatusServiceUnavailable},
		{"504 Gateway Timeout", http.StatusGatewayTimeout},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
			}))
			defer server.Close()

			fakeClock := &FakeClock{now: time.Now()}
			cb, err := NewZeroTolerance(WithClock(fakeClock))
			if err != nil {
				t.Fatalf("Failed to create circuit breaker: %v", err)
			}

			req, _ := http.NewRequest("GET", server.URL, nil)
			resp, err := cb.HTTPRequest(req)

			if err != nil {
				t.Errorf("Expected no error for %d status, got %v", tc.statusCode, err)
			}
			if resp == nil {
				t.Fatal("Expected response, got nil")
			}
			if resp.StatusCode != tc.statusCode {
				t.Errorf("Expected status %d, got %d", tc.statusCode, resp.StatusCode)
			}

			ztcb := cb.(*zeroToleranceCircuitBreaker)
			if State(ztcb.state.Load()) != Open {
				t.Errorf("Circuit should open after %d error (zero tolerance), got %v", tc.statusCode, State(ztcb.state.Load()))
			}
		})
	}
}

func TestHttpRequest_NetworkError(t *testing.T) {
	fakeClock := &FakeClock{now: time.Now()}
	cb, err := NewZeroTolerance(WithClock(fakeClock))
	if err != nil {
		t.Fatalf("Failed to create circuit breaker: %v", err)
	}

	req, _ := http.NewRequest("GET", "http://localhost:99999", nil)
	resp, err := cb.HTTPRequest(req)

	if err == nil {
		t.Error("Expected network error, got nil")
	}
	if resp != nil {
		t.Errorf("Expected nil response on network error, got %v", resp)
	}

	ztcb := cb.(*zeroToleranceCircuitBreaker)
	if State(ztcb.state.Load()) != Open {
		t.Errorf("Circuit should open after network error, got %v", State(ztcb.state.Load()))
	}
}

func TestHttpRequest_CircuitOpenBlocksRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	fakeClock := &FakeClock{now: time.Now()}
	cb, err := NewZeroTolerance(WithClock(fakeClock))
	if err != nil {
		t.Fatalf("Failed to create circuit breaker: %v", err)
	}

	cb.ReportFailure()

	ztcb := cb.(*zeroToleranceCircuitBreaker)
	if State(ztcb.state.Load()) != Open {
		t.Fatal("Circuit should be open")
	}

	req, _ := http.NewRequest("GET", server.URL, nil)
	resp, err := cb.HTTPRequest(req)

	if err == nil {
		t.Error("Expected error when circuit is open")
	}
	if resp != nil {
		t.Errorf("Expected nil response when circuit is open, got %v", resp)
	}
	if !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("Expected ErrCircuitOpen, got %v", err)
	}
}

func TestHttpRequest_HalfOpenProbeSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	fakeClock := &FakeClock{now: time.Now()}
	cb, err := NewZeroTolerance(WithClock(fakeClock))
	if err != nil {
		t.Fatalf("Failed to create circuit breaker: %v", err)
	}

	cb.ReportFailure()
	fakeClock.Advance(121 * time.Second)

	ztcb := cb.(*zeroToleranceCircuitBreaker)

	req, _ := http.NewRequest("GET", server.URL, nil)

	for i := 0; i < int(ztcb.config.successToClose); i++ {
		resp, err := cb.HTTPRequest(req)
		if err != nil {
			t.Fatalf("Request %d failed: %v", i+1, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Request %d: expected 200, got %d", i+1, resp.StatusCode)
		}
	}

	if State(ztcb.state.Load()) != Closed {
		t.Errorf("Circuit should be closed after %d successes, got %v", ztcb.config.successToClose, State(ztcb.state.Load()))
	}
}

func TestHttpRequest_HalfOpenProbeFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	fakeClock := &FakeClock{now: time.Now()}
	cb, err := NewZeroTolerance(WithClock(fakeClock))
	if err != nil {
		t.Fatalf("Failed to create circuit breaker: %v", err)
	}

	cb.ReportFailure()
	fakeClock.Advance(121 * time.Second)

	ztcb := cb.(*zeroToleranceCircuitBreaker)

	req, _ := http.NewRequest("GET", server.URL, nil)
	resp, err := cb.HTTPRequest(req)

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("Expected 500, got %d", resp.StatusCode)
	}

	if State(ztcb.state.Load()) != Open {
		t.Errorf("Circuit should reopen after failure in half-open, got %v", State(ztcb.state.Load()))
	}
}

func TestHttpRequest_CustomHttpErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	fakeClock := &FakeClock{now: time.Now()}

	customErrorFunc := func(statusCode int) bool {
		return statusCode >= 500 && statusCode <= 599
	}

	cb, err := NewZeroTolerance(
		WithClock(fakeClock),
		WithCustomHTTPErrors(customErrorFunc),
	)
	if err != nil {
		t.Fatalf("Failed to create circuit breaker: %v", err)
	}

	req, _ := http.NewRequest("GET", server.URL, nil)
	resp, err := cb.HTTPRequest(req)

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("Expected 404, got %d", resp.StatusCode)
	}

	ztcb := cb.(*zeroToleranceCircuitBreaker)
	if State(ztcb.state.Load()) != Closed {
		t.Errorf("Circuit should stay closed with custom error func that ignores 4xx, got %v", State(ztcb.state.Load()))
	}
}

func TestHttpRequest_3xxRedirect(t *testing.T) {
	redirectCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if redirectCount == 0 {
			redirectCount++
			http.Redirect(w, r, "/redirected", http.StatusMovedPermanently)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("redirected"))
	}))
	defer server.Close()

	fakeClock := &FakeClock{now: time.Now()}
	cb, err := NewZeroTolerance(WithClock(fakeClock))
	if err != nil {
		t.Fatalf("Failed to create circuit breaker: %v", err)
	}

	req, _ := http.NewRequest("GET", server.URL, nil)
	resp, err := cb.HTTPRequest(req)

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if resp == nil {
		t.Fatal("Expected response, got nil")
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200 after redirect, got %d", resp.StatusCode)
	}

	ztcb := cb.(*zeroToleranceCircuitBreaker)
	if State(ztcb.state.Load()) != Closed {
		t.Errorf("Circuit should remain closed after successful redirect, got %v", State(ztcb.state.Load()))
	}
}

func TestHttpRequest_2xxSuccessCodes(t *testing.T) {
	testCases := []struct {
		name       string
		statusCode int
	}{
		{"200 OK", http.StatusOK},
		{"201 Created", http.StatusCreated},
		{"202 Accepted", http.StatusAccepted},
		{"204 No Content", http.StatusNoContent},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
			}))
			defer server.Close()

			fakeClock := &FakeClock{now: time.Now()}
			cb, err := NewZeroTolerance(WithClock(fakeClock))
			if err != nil {
				t.Fatalf("Failed to create circuit breaker: %v", err)
			}

			req, _ := http.NewRequest("GET", server.URL, nil)
			resp, err := cb.HTTPRequest(req)

			if err != nil {
				t.Errorf("Expected no error for %d status, got %v", tc.statusCode, err)
			}
			if resp == nil {
				t.Fatal("Expected response, got nil")
			}
			if resp.StatusCode != tc.statusCode {
				t.Errorf("Expected status %d, got %d", tc.statusCode, resp.StatusCode)
			}

			ztcb := cb.(*zeroToleranceCircuitBreaker)
			if State(ztcb.state.Load()) != Closed {
				t.Errorf("Circuit should remain closed after %d success, got %v", tc.statusCode, State(ztcb.state.Load()))
			}
		})
	}
}
