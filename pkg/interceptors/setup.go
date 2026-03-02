package interceptors

import (
	"github.com/sonic-net/sonic-gnmi/pkg/interceptors/dpuproxy"
	"google.golang.org/grpc"
)

// ServerChain represents a configured interceptor chain with cleanup capabilities.
type ServerChain struct {
	chain   *Chain
	cleanup func() error
}

// NewServerChain creates a complete interceptor chain for the gNMI server.
// Currently includes DPU proxy interceptor with Redis-based DPU resolution.
// Returns the chain and a cleanup function that must be called during shutdown.
func NewServerChain() (*ServerChain, error) {
	// Create Redis clients for DPU info resolution from both StateDB and ConfigDB
	stateRedisClient := dpuproxy.NewRedisClient(dpuproxy.DefaultRedisSocket, dpuproxy.StateDB)
	stateRedisAdapter := dpuproxy.NewGoRedisAdapter(stateRedisClient)

	configRedisClient := dpuproxy.NewRedisClient(dpuproxy.DefaultRedisSocket, dpuproxy.ConfigDB)
	configRedisAdapter := dpuproxy.NewGoRedisAdapter(configRedisClient)

	// Create DPU resolver and proxy
	dpuResolver := dpuproxy.NewDPUResolver(stateRedisAdapter, configRedisAdapter)
	dpuProxy := dpuproxy.NewDPUProxy(dpuResolver)
	dpuproxy.SetDefaultProxy(dpuProxy)

	// Create interceptor chain with DPU proxy
	chain := NewChain(dpuProxy)

	// Create cleanup function to close Redis clients
	cleanup := func() error {
		var firstErr error

		// Close state Redis client
		if err := stateRedisClient.Close(); err != nil && firstErr == nil {
			firstErr = err
		}

		// Close config Redis client
		if err := configRedisClient.Close(); err != nil && firstErr == nil {
			firstErr = err
		}

		// TODO: Add DPU proxy connection cleanup when implemented
		// if dpuProxy != nil {
		//     if err := dpuProxy.Close(); err != nil && firstErr == nil {
		//         firstErr = err
		//     }
		// }

		return firstErr
	}

	return &ServerChain{
		chain:   chain,
		cleanup: cleanup,
	}, nil
}

// GetServerOptions returns gRPC server options with all interceptors configured.
func (sc *ServerChain) GetServerOptions() []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.UnaryInterceptor(sc.chain.UnaryInterceptor()),
		grpc.StreamInterceptor(sc.chain.StreamInterceptor()),
	}
}

// Close cleanly shuts down all managed resources.
// This should be called during server shutdown to prevent resource leaks.
func (sc *ServerChain) Close() error {
	if sc.cleanup != nil {
		return sc.cleanup()
	}
	return nil
}
