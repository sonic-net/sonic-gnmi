package interceptors

import (
	log "github.com/golang/glog"
	"github.com/sonic-net/sonic-gnmi/pkg/interceptors/dpuproxy"
	"google.golang.org/grpc"
)

// ServerChain represents a configured interceptor chain with cleanup capabilities.
type ServerChain struct {
	chain      *Chain
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
		chain:      chain,
		completion: completion,
		cleanup:    cleanup,
	}, nil
}

// GetServerOptions orders completion logging, supplied middleware, then proxying.
// Pass middleware here rather than registering it as a separate server option.
func (sc *ServerChain) GetServerOptions(middleware ...Interceptor) []grpc.ServerOption {
	unary := []grpc.UnaryServerInterceptor{sc.completion.UnaryInterceptor()}
	stream := []grpc.StreamServerInterceptor{sc.completion.StreamInterceptor()}
	for _, interceptor := range middleware {
		unary = append(unary, interceptor.UnaryInterceptor())
		stream = append(stream, interceptor.StreamInterceptor())
	}
	for _, interceptor := range sc.chain.interceptors {
		unary = append(unary, interceptor.UnaryInterceptor())
		stream = append(stream, interceptor.StreamInterceptor())
	}
	return []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(unary...),
		grpc.ChainStreamInterceptor(stream...),
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
