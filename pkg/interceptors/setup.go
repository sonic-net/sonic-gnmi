package interceptors

import (
	log "github.com/golang/glog"
	"github.com/sonic-net/sonic-gnmi/pkg/interceptors/dpuproxy"
	"google.golang.org/grpc"
)

// ServerChain represents a configured interceptor chain with cleanup capabilities.
type ServerChain struct {
	proxy      Interceptor
	completion *rpcCompletionLogger
	cleanup    func() error
}

// NewServerChain creates a complete interceptor chain for the gNMI server.
// It includes RPC completion logging and DPU proxying with Redis-based DPU resolution.
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

	sink := lineSink(func(line string) error {
		log.Infof("%s", line)
		return nil
	})

	// Keep completion logging outside the DPU proxy so it records forwarded and rejected RPCs.
	completion := newRPCCompletionLogger(sink)

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
		proxy:      dpuProxy,
		completion: completion,
		cleanup:    cleanup,
	}, nil
}

// GetServerOptions keeps completion logging outside DPU proxying.
func (sc *ServerChain) GetServerOptions() []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(
			sc.completion.UnaryInterceptor(),
			sc.proxy.UnaryInterceptor(),
		),
		grpc.ChainStreamInterceptor(
			sc.completion.StreamInterceptor(),
			sc.proxy.StreamInterceptor(),
		),
	}
}

// GetServerOptionsWithAuthorization explicitly inserts the server-owned gate.
func (sc *ServerChain) GetServerOptionsWithAuthorization(auth Interceptor) []grpc.ServerOption {
	if auth == nil {
		return sc.GetServerOptions()
	}
	return []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(sc.completion.UnaryInterceptor(), auth.UnaryInterceptor(), sc.proxy.UnaryInterceptor()),
		grpc.ChainStreamInterceptor(sc.completion.StreamInterceptor(), auth.StreamInterceptor(), sc.proxy.StreamInterceptor()),
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
