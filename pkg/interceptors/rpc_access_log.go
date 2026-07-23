package interceptors

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

const rpcAccessLogPrefix = "RPC_ACCESS"

type logfFunc func(string, ...interface{})

// rpcLogger emits one structured access log when each dispatched RPC completes.
type rpcLogger struct {
	logf logfFunc
}

type rpcAccessLogRecord struct {
	Version    int    `json:"v"`
	RPCType    string `json:"type"`
	Method     string `json:"method"`
	PeerType   string `json:"peer_type"`
	Peer       string `json:"peer"`
	Code       string `json:"code"`
	DurationMS int64  `json:"duration_ms"`
}

func newRPCLogger(logf logfFunc) *rpcLogger {
	return &rpcLogger{logf: logf}
}

func (l *rpcLogger) log(ctx context.Context, rpcType, method string, started time.Time, err error) {
	peerType, peerAddress := rpcPeer(ctx)
	record, marshalErr := json.Marshal(rpcAccessLogRecord{
		Version:    1,
		RPCType:    rpcType,
		Method:     method,
		PeerType:   peerType,
		Peer:       peerAddress,
		Code:       rpcCode(err).String(),
		DurationMS: time.Since(started).Milliseconds(),
	})
	if marshalErr != nil {
		return
	}
	l.write(record)
}

func (l *rpcLogger) write(record []byte) {
	defer func() {
		_ = recover()
	}()
	l.logf("%s %s", rpcAccessLogPrefix, record)
}

func rpcCode(err error) codes.Code {
	if err == nil {
		return codes.OK
	}
	if rpcStatus, ok := status.FromError(err); ok {
		return rpcStatus.Code()
	}
	return status.FromContextError(err).Code()
}

func rpcPeer(ctx context.Context) (string, string) {
	requestPeer, ok := peer.FromContext(ctx)
	if !ok || requestPeer.Addr == nil {
		return "unknown", ""
	}

	peerType := "unknown"
	network := requestPeer.Addr.Network()
	switch {
	case strings.HasPrefix(network, "tcp"):
		peerType = "tcp"
	case strings.HasPrefix(network, "unix"):
		peerType = "unix"
	}
	return peerType, requestPeer.Addr.String()
}

// UnaryInterceptor logs the final outcome of a unary RPC.
func (l *rpcLogger) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		started := time.Now()
		response, err := handler(ctx, req)
		l.log(ctx, "unary", info.FullMethod, started, err)
		return response, err
	}
}

// StreamInterceptor logs the final outcome of a streaming RPC.
func (l *rpcLogger) StreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		started := time.Now()
		err := handler(srv, stream)
		l.log(stream.Context(), "stream", info.FullMethod, started, err)
		return err
	}
}
