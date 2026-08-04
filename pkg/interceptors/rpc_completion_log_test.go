package interceptors

import (
	"context"
	"strings"
	"testing"

	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc"
)

func TestRPCCompletionLoggerNonAuditUnaryEmitsAccessOnly(t *testing.T) {
	var lines []string
	logger := newRPCCompletionLogger(func(line string) error {
		lines = append(lines, line)
		return nil
	})
	handlerCalls := 0

	_, err := logger.UnaryInterceptor()(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: "/gnoi.system.System/Time"},
		func(context.Context, interface{}) (interface{}, error) {
			handlerCalls++
			return nil, nil
		})
	if err != nil {
		t.Fatalf("UnaryInterceptor() error = %v", err)
	}
	if handlerCalls != 1 {
		t.Fatalf("handler calls = %d, want 1", handlerCalls)
	}
	if len(lines) != 1 || !strings.HasPrefix(lines[0], rpcAccessLogPrefix+" ") {
		t.Fatalf("completion lines = %v, want one RPC_ACCESS", lines)
	}
}

func TestRPCCompletionLoggerStreamEmitsAccessOnly(t *testing.T) {
	var lines []string
	logger := newRPCCompletionLogger(func(line string) error {
		lines = append(lines, line)
		return nil
	})
	handlerCalls := 0
	stream := &accessLogServerStream{ctx: context.Background()}

	err := logger.StreamInterceptor()(nil, stream,
		&grpc.StreamServerInfo{FullMethod: "/gnmi.gNMI/Subscribe"},
		func(interface{}, grpc.ServerStream) error {
			handlerCalls++
			return nil
		})
	if err != nil {
		t.Fatalf("StreamInterceptor() error = %v", err)
	}
	if handlerCalls != 1 {
		t.Fatalf("handler calls = %d, want 1", handlerCalls)
	}
	if len(lines) != 1 || !strings.HasPrefix(lines[0], rpcAccessLogPrefix+" ") {
		t.Fatalf("completion lines = %v, want one RPC_ACCESS", lines)
	}
}

func TestRPCCompletionLoggerPanicPreservesAccessBehavior(t *testing.T) {
	var lines []string
	logger := newRPCCompletionLogger(func(line string) error {
		lines = append(lines, line)
		return nil
	})
	var recovered interface{}

	func() {
		defer func() { recovered = recover() }()
		_, _ = logger.UnaryInterceptor()(context.Background(), &gnmipb.GetRequest{},
			&grpc.UnaryServerInfo{FullMethod: gnmiGetMethod},
			func(context.Context, interface{}) (interface{}, error) {
				panic("panic-value")
			})
	}()

	if recovered != "panic-value" {
		t.Fatalf("recovered = %v, want original panic", recovered)
	}
	if len(lines) != 1 || !strings.HasPrefix(lines[0], gnmiAuditPrefix+" ") {
		t.Fatalf("panic completion lines = %v, want GNMI_AUDIT only", lines)
	}
}
