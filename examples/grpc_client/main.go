package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/michael-jaquier/circuitbreaker"
)

// MockGRPCClient simulates a gRPC client for demonstration purposes.
type MockGRPCClient struct{}

// ListItemsRequest represents a request to list items.
type ListItemsRequest struct {
	PageSize int
}

// ListItemsResponse represents a response containing items.
type ListItemsResponse struct {
	Items []string
	Total int
}

// ListItems simulates a gRPC method call.
func (c *MockGRPCClient) ListItems(ctx context.Context, req *ListItemsRequest) (*ListItemsResponse, error) {
	// Simulate gRPC call
	return &ListItemsResponse{
		Items: []string{"item1", "item2", "item3"},
		Total: 3,
	}, nil
}

// Example: Using ExecuteGRPCBlocking for clean gRPC calls
func main() {
	// Create circuit breaker
	cb, err := circuitbreaker.NewZeroTolerance(
		circuitbreaker.WithCooldownTimer(30*time.Second),
		circuitbreaker.WithSuccessToClose(3),
	)
	if err != nil {
		log.Fatalf("Failed to create circuit breaker: %v", err)
	}

	client := &MockGRPCClient{}
	req := &ListItemsRequest{PageSize: 100}

	fmt.Println("=== Example 1: Simple gRPC call with circuit breaker ===")

	// Clean interface - single line with circuit breaker protection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	resp, err := cb.ExecuteGRPCBlocking(ctx, func(ctx context.Context) (interface{}, error) {
		return client.ListItems(ctx, req)
	})

	if err != nil {
		log.Fatalf("Circuit breaker could not complete request: %v", err)
	}

	// Type assert the response
	typedResp := resp.(*ListItemsResponse)
	fmt.Printf("Success! Got %d items (total: %d)\n", len(typedResp.Items), typedResp.Total)
	fmt.Println()

	fmt.Println("=== Comparison: Old vs New Pattern ===")
	fmt.Println()
	fmt.Println("OLD PATTERN (30+ lines of boilerplate per method):")
	fmt.Println(`
	var resp *pb.Response
	var grpcErr error
	retryCtx, retryCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer retryCancel()
	
	for {
		timer, err := cb.Execute(ctx, func(ctx context.Context) error {
			resp, grpcErr = client.SomeMethod(ctx, req)
			return grpcErr
		})
		
		if timer != nil {
			slog.Warn("circuit breaker open - waiting for retry")
			select {
			case <-timer.C:
				continue
			case <-retryCtx.Done():
				return nil, fmt.Errorf("retry timeout exceeded")
			}
		}
		
		if err != nil {
			slog.Error("request failed", "error", err)
			return nil, err
		}
		
		return resp, nil
	}
	`)
	fmt.Println()
	fmt.Println("NEW PATTERN (3 lines):")
	fmt.Println(`
	resp, err := cb.ExecuteGRPCBlocking(ctx, func(ctx context.Context) (interface{}, error) {
		return client.SomeMethod(ctx, req)
	})
	if err != nil {
		return nil, err // Circuit breaker couldn't handle it
	}
	`)
	fmt.Println()
	fmt.Println("Benefits:")
	fmt.Println("- Automatic retry with circuit breaker protection")
	fmt.Println("- No manual timer handling")
	fmt.Println("- No boilerplate retry loops")
	fmt.Println("- Context deadline honored automatically")
	fmt.Println("- Same pattern for HTTP (ExecuteHTTPBlocking)")
}
