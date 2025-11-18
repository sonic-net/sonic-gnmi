package interceptors

import (
	"context"

	"google.golang.org/grpc"
)

// Interceptor defines the interface for gRPC interceptors.
// Implementations can provide both unary and streaming interceptor logic.
type Interceptor interface {
	// UnaryInterceptor returns a grpc.UnaryServerInterceptor for unary RPCs.
	UnaryInterceptor() grpc.UnaryServerInterceptor

	// StreamInterceptor returns a grpc.StreamServerInterceptor for streaming RPCs.
	StreamInterceptor() grpc.StreamServerInterceptor
}

// Chain manages a sequence of interceptors that are executed in order.
type Chain struct {
	interceptors []Interceptor
}

// NewChain creates a new interceptor chain with the given interceptors.
// Interceptors are executed in the order they are provided.
func NewChain(interceptors ...Interceptor) *Chain {
	return &Chain{
		interceptors: interceptors,
	}
}

// UnaryInterceptor returns a single grpc.UnaryServerInterceptor that chains
// all registered interceptors together. Each interceptor is called in order,
// and can choose to pass control to the next interceptor or short-circuit.
func (c *Chain) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		// Build a chain of handlers, starting from the end
		chainedHandler := handler

		// Iterate interceptors in reverse order to build the chain
		for i := len(c.interceptors) - 1; i >= 0; i-- {
			// Capture variables by value using immediate function invocation
			chainedHandler = func(interceptor Interceptor, currentHandler grpc.UnaryHandler) grpc.UnaryHandler {
				return func(ctx context.Context, req interface{}) (interface{}, error) {
					return interceptor.UnaryInterceptor()(ctx, req, info, currentHandler)
				}
			}(c.interceptors[i], chainedHandler)
		}

		// Execute the chained handler
		return chainedHandler(ctx, req)
	}
}

// StreamInterceptor returns a single grpc.StreamServerInterceptor that chains
// all registered interceptors together. Each interceptor is called in order,
// and can choose to pass control to the next interceptor or short-circuit.
func (c *Chain) StreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		// Build a chain of handlers, starting from the end
		chainedHandler := handler

		// Iterate interceptors in reverse order to build the chain
		for i := len(c.interceptors) - 1; i >= 0; i-- {
			// Capture variables by value using immediate function invocation
			chainedHandler = func(interceptor Interceptor, currentHandler grpc.StreamHandler) grpc.StreamHandler {
				return func(srv interface{}, ss grpc.ServerStream) error {
					return interceptor.StreamInterceptor()(srv, ss, info, currentHandler)
				}
			}(c.interceptors[i], chainedHandler)
		}

		// Execute the chained handler
		return chainedHandler(srv, ss)
	}
}
