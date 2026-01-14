package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/michael-jaquier/circuitbreaker"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// CircuitBreakerInterceptor creates a gRPC unary client interceptor with circuit breaker
// This interceptor wraps all gRPC client calls with circuit breaker protection
// It opens the circuit on:
// - Network errors
// - Server errors (Unavailable, Internal, etc.)
// - Timeout errors
func CircuitBreakerInterceptor(cb circuitbreaker.CircuitBreaker) grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		req, reply interface{},
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		var opErr error

		timer, err := cb.Execute(ctx, func(ctx context.Context) error {
			// Invoke the actual RPC
			opErr = invoker(ctx, method, req, reply, cc, opts...)

			// Determine if error should open the circuit
			if opErr != nil && shouldOpenCircuit(opErr) {
				return opErr
			}
			// Client errors don't open circuit, return nil to report success
			return nil
		})

		if timer != nil {
			// Circuit breaker is open, calculate wait time from timer
			// The timer will fire when the cooldown period expires
			return status.Errorf(codes.Unavailable,
				"circuit breaker open for %s", method)
		}

		if err != nil {
			// Execute returned an error (circuit breaker failure)
			return opErr
		}

		return opErr
	}
}

// shouldOpenCircuit determines if a gRPC error should open the circuit
// Only server-side failures should open the circuit, not client errors
func shouldOpenCircuit(err error) bool {
	if err == nil {
		return false
	}

	st, ok := status.FromError(err)
	if !ok {
		// Not a gRPC status error, treat as failure
		return true
	}

	switch st.Code() {
	case codes.Unavailable, // Service unavailable
		codes.Internal,          // Internal server error
		codes.Unknown,           // Unknown error
		codes.DataLoss,          // Data loss
		codes.DeadlineExceeded,  // Timeout
		codes.ResourceExhausted: // Resource exhausted (overload)
		return true
	case codes.Canceled, // Client canceled
		codes.InvalidArgument,    // Client error
		codes.NotFound,           // Resource not found
		codes.AlreadyExists,      // Resource already exists
		codes.PermissionDenied,   // Permission denied
		codes.Unauthenticated,    // Authentication required
		codes.FailedPrecondition, // Precondition failed
		codes.Aborted,            // Operation aborted
		codes.OutOfRange:         // Out of range
		return false
	default:
		return true
	}
}

// StreamCircuitBreakerInterceptor creates a gRPC stream client interceptor with circuit breaker
// This protects streaming RPC calls
func StreamCircuitBreakerInterceptor(cb circuitbreaker.CircuitBreaker) grpc.StreamClientInterceptor {
	return func(
		ctx context.Context,
		desc *grpc.StreamDesc,
		cc *grpc.ClientConn,
		method string,
		streamer grpc.Streamer,
		opts ...grpc.CallOption,
	) (grpc.ClientStream, error) {
		var stream grpc.ClientStream
		var opErr error

		timer, err := cb.Execute(ctx, func(ctx context.Context) error {
			stream, opErr = streamer(ctx, desc, cc, method, opts...)

			// Determine if error should open the circuit
			if opErr != nil && shouldOpenCircuit(opErr) {
				return opErr
			}
			// Client errors don't open circuit, return nil to report success
			return nil
		})

		if timer != nil {
			// Circuit breaker is open, calculate wait time from timer
			// The timer will fire when the cooldown period expires
			return nil, status.Errorf(codes.Unavailable,
				"circuit breaker open for stream %s", method)
		}

		if err != nil {
			// Execute returned an error (circuit breaker failure)
			return nil, opErr
		}

		return stream, opErr
	}
}

// NewGRPCClientConn creates a gRPC client connection with circuit breaker interceptors
func NewGRPCClientConn(target string, opts ...grpc.DialOption) (*grpc.ClientConn, circuitbreaker.CircuitBreaker, error) {
	// Create circuit breaker with appropriate configuration for gRPC
	cb, err := circuitbreaker.NewZeroTolerance(
		circuitbreaker.WithCooldownTimer(30*time.Second),
		circuitbreaker.WithSuccessToClose(5),
		circuitbreaker.WithWindowSize(120*time.Second),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create circuit breaker: %w", err)
	}

	// Add circuit breaker interceptors to dial options
	opts = append(opts,
		grpc.WithUnaryInterceptor(CircuitBreakerInterceptor(cb)),
		grpc.WithStreamInterceptor(StreamCircuitBreakerInterceptor(cb)),
	)

	conn, err := grpc.Dial(target, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to dial: %w", err)
	}

	return conn, cb, nil
}

// ServiceClient wraps a gRPC connection with circuit breaker protection
type ServiceClient struct {
	conn    *grpc.ClientConn
	breaker circuitbreaker.CircuitBreaker
	// In a real application, this would be your generated gRPC client:
	// client  pb.YourServiceClient
}

// NewServiceClient creates a new gRPC service client with circuit breaker protection
func NewServiceClient(target string) (*ServiceClient, error) {
	// In production, use grpc.WithTransportCredentials for TLS
	conn, cb, err := NewGRPCClientConn(target,
		grpc.WithInsecure(), // Only for demonstration
	)
	if err != nil {
		return nil, err
	}

	return &ServiceClient{
		conn:    conn,
		breaker: cb,
		// client: pb.NewYourServiceClient(conn),
	}, nil
}

// Close closes the underlying gRPC connection
func (c *ServiceClient) Close() error {
	return c.conn.Close()
}

// ExampleCall demonstrates circuit breaker behavior for gRPC calls
func (c *ServiceClient) ExampleCall(ctx context.Context) error {
	// In a real application:
	// resp, err := c.client.YourMethod(ctx, &pb.YourRequest{})
	// if err != nil {
	//     if status.Code(err) == codes.Unavailable {
	//         // Circuit breaker is open or service unavailable
	//         return handleUnavailable(err)
	//     }
	//     return err
	// }
	// return processResponse(resp)

	// For demonstration:
	fmt.Println("Making gRPC call (protected by circuit breaker)")
	return nil
}

// DemonstrateCircuitBreaker shows circuit breaker behavior without actual gRPC server
func DemonstrateCircuitBreaker() {
	fmt.Println("=== gRPC Circuit Breaker Demonstration ===")

	// Create circuit breaker for demonstration
	cb, err := circuitbreaker.NewZeroTolerance(
		circuitbreaker.WithCooldownTimer(5*time.Second),
		circuitbreaker.WithSuccessToClose(3),
	)
	if err != nil {
		panic(err)
	}

	// Simulate successful calls
	fmt.Println("1. Simulating successful gRPC calls:")
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		timer, err := cb.Execute(ctx, func(ctx context.Context) error {
			return nil // Success
		})
		if timer == nil && err == nil {
			fmt.Printf("   Call %d: Success\n", i+1)
		}
	}
	fmt.Println()

	// Simulate a failure (server unavailable)
	fmt.Println("2. Simulating server failure:")
	timer, err := cb.Execute(ctx, func(ctx context.Context) error {
		return errors.New("server unavailable")
	})
	if timer == nil && err != nil {
		fmt.Println("   Call: Failed (server unavailable)")
	}
	fmt.Println("   Circuit breaker: OPEN")
	fmt.Println()

	// Attempt to make calls while circuit is open
	fmt.Println("3. Attempting calls while circuit is open:")
	for i := 0; i < 3; i++ {
		timer, _ := cb.Execute(ctx, func(ctx context.Context) error {
			return nil
		})
		if timer != nil {
			fmt.Printf("   Call %d: Blocked (circuit open)\n", i+1)
		}
	}
	fmt.Println()

	// Wait for cooldown period
	fmt.Println("4. Waiting for cooldown period (5 seconds)...")
	time.Sleep(6 * time.Second)
	fmt.Println()

	// Circuit transitions to half-open, allows probe requests
	fmt.Println("5. Circuit in HALF-OPEN state (allowing probe requests):")
	timer, err = cb.Execute(ctx, func(ctx context.Context) error {
		return nil // Success
	})
	if timer == nil && err == nil {
		fmt.Println("   Probe call 1: Allowed")
	}

	timer, err = cb.Execute(ctx, func(ctx context.Context) error {
		return nil // Success
	})
	if timer == nil && err == nil {
		fmt.Println("   Probe call 2: Allowed")
	}

	timer, err = cb.Execute(ctx, func(ctx context.Context) error {
		return nil // Success
	})
	if timer == nil && err == nil {
		fmt.Println("   Probe call 3: Allowed")
	}
	fmt.Println("   Circuit breaker: CLOSED (recovered)")
	fmt.Println()

	// Back to normal operation
	fmt.Println("6. Normal operation resumed:")
	for i := 0; i < 3; i++ {
		timer, err := cb.Execute(ctx, func(ctx context.Context) error {
			return nil // Success
		})
		if timer == nil && err == nil {
			fmt.Printf("   Call %d: Success\n", i+1)
		}
	}
	fmt.Println()
}

func main() {
	fmt.Println("=== gRPC Circuit Breaker Example ===")

	// Demonstrate circuit breaker behavior
	DemonstrateCircuitBreaker()

	fmt.Println("=== gRPC Integration Patterns ===")

	fmt.Println("Integration Pattern 1: Client-side interceptor")
	fmt.Println("```go")
	fmt.Println(`conn, cb, err := NewGRPCClientConn("localhost:50051",
    grpc.WithInsecure(),
)
client := pb.NewYourServiceClient(conn)
resp, err := client.YourMethod(ctx, &pb.YourRequest{})
if err != nil {
    if status.Code(err) == codes.Unavailable {
        // Circuit breaker is open or service unavailable
        return handleCircuitOpen()
    }
}`)
	fmt.Println("```")

	fmt.Println("Integration Pattern 2: Per-method circuit breakers")
	fmt.Println("```go")
	fmt.Println(`type ServiceClient struct {
    listBreaker   circuitbreaker.CircuitBreaker
    createBreaker circuitbreaker.CircuitBreaker
    updateBreaker circuitbreaker.CircuitBreaker
}

func (c *ServiceClient) ListItems(ctx context.Context) error {
    var opErr error
    timer, err := c.listBreaker.Execute(ctx, func(ctx context.Context) error {
        // Make gRPC call
        opErr = c.client.List(ctx, req)
        if opErr != nil && shouldOpenCircuit(opErr) {
            return opErr
        }
        return nil
    })
    if timer != nil {
        return handleCircuitOpen()
    }
    return opErr
}`)
	fmt.Println("```")

	fmt.Println("Integration Pattern 3: Fallback strategies")
	fmt.Println("```go")
	fmt.Println(`resp, err := client.GetUser(ctx, req)
if err != nil {
    if status.Code(err) == codes.Unavailable {
        // Circuit is open, use cache
        return getCachedUser(req.UserId)
    }
    return nil, err
}`)
	fmt.Println("```")

	fmt.Println("=== Key Takeaways ===")
	fmt.Println("1. gRPC interceptors provide clean integration point")
	fmt.Println("2. Distinguish server errors (open circuit) from client errors (don't open)")
	fmt.Println("3. Use codes.Unavailable to detect circuit breaker open")
	fmt.Println("4. Consider per-method circuit breakers for fine-grained control")
	fmt.Println("5. Implement fallback strategies (cache, default responses)")
	fmt.Println("6. Monitor circuit breaker state for operational visibility")
	fmt.Println()

	fmt.Println("=== Circuit Breaker Error Classification ===")
	fmt.Println("Errors that OPEN circuit:")
	fmt.Println("  - Unavailable: Service unavailable")
	fmt.Println("  - Internal: Internal server error")
	fmt.Println("  - DeadlineExceeded: Request timeout")
	fmt.Println("  - ResourceExhausted: Server overloaded")
	fmt.Println("  - Unknown: Unknown error")
	fmt.Println()
	fmt.Println("Errors that DON'T open circuit:")
	fmt.Println("  - InvalidArgument: Bad request (client fault)")
	fmt.Println("  - NotFound: Resource not found")
	fmt.Println("  - PermissionDenied: Authorization failure")
	fmt.Println("  - Unauthenticated: Authentication required")
	fmt.Println("  - AlreadyExists: Resource conflict")
	fmt.Println()

	fmt.Println("To use this with a real gRPC service:")
	fmt.Println("1. Define your .proto file and generate Go code")
	fmt.Println("2. Use NewGRPCClientConn() to create connection with circuit breaker")
	fmt.Println("3. Create your gRPC client with the connection")
	fmt.Println("4. All calls automatically protected by circuit breaker")
	fmt.Println()

	// Example of creating a client (without actual gRPC server)
	fmt.Println("Example client creation:")
	fmt.Println("```go")
	fmt.Println(`client, err := NewServiceClient("localhost:50051")
if err != nil {
    panic(err)
}
defer client.Close()

err = client.ExampleCall(context.Background())
if err != nil {
    if status.Code(err) == codes.Unavailable {
        log.Println("Service unavailable, circuit breaker may be open")
    }
}`)
	fmt.Println("```")
}
