package interceptors

import (
	"context"

	sharedlog "github.com/sonic-net/sonic-gnmi/pkg/logging"
	"google.golang.org/grpc"
)

// rpcCompletionLogger owns the server RPC completion lifecycle. Access and
// gNMI audit logging retain separate schemas and limiter state.
type rpcCompletionLogger struct {
	access *rpcLogger
	audit  *gnmiAuditLogger
}

func newRPCCompletionLogger(sink sharedlog.LineSink) *rpcCompletionLogger {
	return &rpcCompletionLogger{
		access: newRPCLogger(sink),
		audit:  newGNMIAuditLogger(sink),
	}
}

func newRPCCompletionLoggerWithLoggers(access *rpcLogger, audit *gnmiAuditLogger) *rpcCompletionLogger {
	return &rpcCompletionLogger{access: access, audit: audit}
}

func (l *rpcCompletionLogger) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		started := l.access.now()

		var response interface{}
		var err error
		if l.audit != nil {
			response, err = l.audit.unary(ctx, req, info, handler)
		} else {
			response, err = handler(ctx, req)
		}

		l.access.log(ctx, "unary", info.FullMethod, started, err)
		return response, err
	}
}

func (l *rpcCompletionLogger) StreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		started := l.access.now()
		err := handler(srv, stream)
		l.access.log(stream.Context(), "stream", info.FullMethod, started, err)
		return err
	}
}
