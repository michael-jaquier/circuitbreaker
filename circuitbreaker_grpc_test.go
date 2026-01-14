package circuitbreaker

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// Mock gRPC response types
type MockGRPCResponse struct {
	Message string
	Code    int
}

func TestExecuteGRPCBlocking_Success(t *testing.T) {
	cb, err := NewZeroTolerance()
	if err != nil {
		t.Fatalf("Failed to create circuit breaker: %v", err)
	}

	expectedResp := &MockGRPCResponse{Message: "success", Code: 200}

	ctx := context.Background()
	resp, err := cb.ExecuteGRPCBlocking(ctx, func(ctx context.Context) (interface{}, error) {
		return expectedResp, nil
	})

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if resp == nil {
		t.Fatal("Expected response, got nil")
	}

	typedResp := resp.(*MockGRPCResponse)
	if typedResp.Message != "success" || typedResp.Code != 200 {
		t.Errorf("Expected success response, got %+v", typedResp)
	}
}

func TestExecuteGRPCBlocking_RetryOnFailure(t *testing.T) {
	attempt := 0
	cb, err := NewZeroTolerance(WithCooldownTimer(100 * time.Millisecond))
	if err != nil {
		t.Fatalf("Failed to create circuit breaker: %v", err)
	}

	expectedResp := &MockGRPCResponse{Message: "success", Code: 200}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	resp, err := cb.ExecuteGRPCBlocking(ctx, func(ctx context.Context) (interface{}, error) {
		attempt++
		if attempt == 1 {
			return nil, fmt.Errorf("temporary gRPC error")
		}
		return expectedResp, nil
	})

	if err != nil {
		t.Errorf("Expected no error after retry, got %v", err)
	}
	if resp == nil {
		t.Fatal("Expected response, got nil")
	}

	typedResp := resp.(*MockGRPCResponse)
	if typedResp.Message != "success" {
		t.Errorf("Expected success after retry, got %+v", typedResp)
	}

	if attempt != 2 {
		t.Errorf("Expected 2 attempts, got %d", attempt)
	}
}

func TestExecuteGRPCBlocking_Error(t *testing.T) {
	cb, err := NewZeroTolerance(WithCooldownTimer(50 * time.Millisecond))
	if err != nil {
		t.Fatalf("Failed to create circuit breaker: %v", err)
	}

	expectedErr := errors.New("permanent gRPC error")

	// Use context with timeout to avoid infinite retry
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	resp, err := cb.ExecuteGRPCBlocking(ctx, func(ctx context.Context) (interface{}, error) {
		return nil, expectedErr
	})

	if err == nil {
		t.Fatal("Expected error, got nil")
	}
	// Should get context deadline exceeded after retries exhausted
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Expected context.DeadlineExceeded after retries, got %v", err)
	}
	if resp != nil {
		t.Error("Expected nil response on error")
	}
}

func TestExecuteGRPCBlocking_ContextTimeout(t *testing.T) {
	cb, err := NewZeroTolerance(WithCooldownTimer(5 * time.Second))
	if err != nil {
		t.Fatalf("Failed to create circuit breaker: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	resp, err := cb.ExecuteGRPCBlocking(ctx, func(ctx context.Context) (interface{}, error) {
		return nil, fmt.Errorf("error that opens circuit")
	})

	if err == nil {
		t.Error("Expected context deadline exceeded error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Expected context.DeadlineExceeded, got %v", err)
	}
	if resp != nil {
		t.Error("Expected nil response on context timeout")
	}
}

func TestExecuteGRPCBlocking_CircuitOpens(t *testing.T) {
	callCount := 0
	cb, err := NewZeroTolerance(WithCooldownTimer(50 * time.Millisecond))
	if err != nil {
		t.Fatalf("Failed to create circuit breaker: %v", err)
	}

	// Use context with timeout to avoid infinite retry
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	_, err = cb.ExecuteGRPCBlocking(ctx, func(ctx context.Context) (interface{}, error) {
		callCount++
		return nil, fmt.Errorf("error that opens circuit")
	})

	if err == nil {
		t.Error("Expected error, got nil")
	}

	// Should get context deadline exceeded after retries
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Expected context.DeadlineExceeded, got %v", err)
	}

	// Should have tried multiple times before context timeout
	if callCount < 2 {
		t.Errorf("Expected at least 2 calls before context timeout, got %d", callCount)
	}
}

func TestExecuteGRPCBlocking_ResponseWithError(t *testing.T) {
	cb, err := NewZeroTolerance(WithCooldownTimer(50 * time.Millisecond))
	if err != nil {
		t.Fatalf("Failed to create circuit breaker: %v", err)
	}

	partialResp := &MockGRPCResponse{Message: "partial", Code: 500}
	expectedErr := fmt.Errorf("gRPC returned error but has partial response")

	// Use context with timeout to avoid infinite retry
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	resp, err := cb.ExecuteGRPCBlocking(ctx, func(ctx context.Context) (interface{}, error) {
		return partialResp, expectedErr
	})

	if err == nil {
		t.Fatal("Expected error, got nil")
	}
	// Should get context deadline exceeded after retries exhausted
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Expected context.DeadlineExceeded after retries, got %v", err)
	}

	// Response can be nil on context timeout
	if resp != nil {
		typedResp := resp.(*MockGRPCResponse)
		if typedResp.Message != "partial" {
			t.Errorf("Expected partial response, got %+v", typedResp)
		}
	}
}
