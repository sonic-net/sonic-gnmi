package interceptors

import (
	"context"

	log "github.com/golang/glog"
	"github.com/sonic-net/sonic-gnmi/pkg/interceptors/dpuproxy"
	"google.golang.org/grpc"
)

// ServerChain represents a configured interceptor chain with cleanup capabilities.
type ServerChain struct {
	chain   *Chain
	cleanup func() error
}

// NewServerChain creates a complete interceptor chain for the gNMI server.
// It includes RPC completion logging and DPU proxying with Redis-based DPU resolution.
// Returns the chain and a cleanup function that must be called during shutdown.
func NewServerChain(routeAuthorizers ...func(context.Context, string) error) (*ServerChain, error) {
	// Create Redis clients for DPU info resolution from both StateDB and ConfigDB
	stateRedisClient := dpuproxy.NewRedisClient(dpuproxy.DefaultRedisSocket, dpuproxy.StateDB)
	stateRedisAdapter := dpuproxy.NewGoRedisAdapter(stateRedisClient)

	configRedisClient := dpuproxy.NewRedisClient(dpuproxy.DefaultRedisSocket, dpuproxy.ConfigDB)
	configRedisAdapter := dpuproxy.NewGoRedisAdapter(configRedisClient)

	// Create DPU resolver and proxy
	dpuResolver := dpuproxy.NewDPUResolver(stateRedisAdapter, configRedisAdapter)
	var routeAuthorizer func(context.Context, string) error
	if len(routeAuthorizers) > 0 {
		routeAuthorizer = routeAuthorizers[0]
	}
	dpuProxy := dpuproxy.NewDPUProxy(dpuResolver, routeAuthorizer)
	dpuproxy.SetDefaultProxy(dpuProxy)

	sink := lineSink(func(line string) error {
		log.Infof("%s", line)
		return nil
	})

	// Keep completion logging outside authorization so it records rejected RPCs.
	chain := NewChain(newRPCCompletionLogger(sink), dpuProxy)

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
