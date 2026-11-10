package interceptors

import (
	"context"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

type gateInterceptor struct {
	allow atomic.Bool
	calls *[]string
}

func (g *gateInterceptor) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, _ *grpc.UnaryServerInfo, next grpc.UnaryHandler) (interface{}, error) {
		if !g.allow.Load() {
			return nil, status.Error(codes.PermissionDenied, "denied")
		}
		*g.calls = append(*g.calls, "gate")
		return next(ctx, req)
	}
}

func (g *gateInterceptor) StreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, stream grpc.ServerStream, _ *grpc.StreamServerInfo, next grpc.StreamHandler) error {
		if !g.allow.Load() {
			return status.Error(codes.Unauthenticated, "revoked")
		}
		*g.calls = append(*g.calls, "gate")
		return next(srv, stream)
	}
}

func TestServerChainLogsChainedDenials(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	var innerCalls []string
	completed := make(chan struct{}, 4)
	chain := &ServerChain{
		chain: NewChain(&mockInterceptor{name: "inner", calls: &innerCalls}),
		completion: newRPCCompletionLogger(func(line string) error {
			mu.Lock()
			defer mu.Unlock()
			lines = append(lines, line)
			completed <- struct{}{}
			return nil
		}),
	}
	// The public composition entry keeps logging outside middleware and the
	// inner chain behind it. Each test request uses a distinct method/status bucket.
	gate := &gateInterceptor{calls: &innerCalls}
	srv := grpc.NewServer(chain.GetServerOptions(gate)...)
	healthpb.RegisterHealthServer(srv, health.NewServer())
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(lis)
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := healthpb.NewHealthClient(conn)
	if _, err := client.Check(ctx, &healthpb.HealthCheckRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatal(err)
	}
	stream, err := client.Watch(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); status.Code(err) != codes.Unauthenticated {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-completed:
		case <-ctx.Done():
			t.Fatal("denied RPC did not complete")
		}
	}
	// Both denied RPCs have completed before checking the untouched inner chain.
	if len(innerCalls) != 0 {
		t.Fatalf("denied RPC entered inner chain: %v", innerCalls)
	}
	gate.allow.Store(true)
	if _, err := client.Check(ctx, &healthpb.HealthCheckRequest{}); err != nil {
		t.Fatal(err)
	}
	streamCtx, stopStream := context.WithCancel(ctx)
	stream, err = client.Watch(streamCtx, &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatal(err)
	}
	stopStream()
	_, _ = stream.Recv()
	for i := 0; i < 2; i++ {
		select {
		case <-completed:
		case <-ctx.Done():
			t.Fatal("allowed RPC did not complete")
		}
	}
	// Stop joins active RPCs before inspecting the call recorder and final logs.
	srv.Stop()
	if strings.Join(innerCalls, ",") != "gate,inner,gate,inner" {
		t.Fatalf("allowed RPC order: %v", innerCalls)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(lines) != 4 || !strings.Contains(lines[0], `"code":"PermissionDenied"`) || !strings.Contains(lines[1], `"code":"Unauthenticated"`) || !strings.Contains(lines[2], `"code":"OK"`) {
		t.Fatalf("completion records: %v", lines)
	}
}

func TestNewServerChain(t *testing.T) {
	chain, err := NewServerChain()
	if err != nil {
		t.Fatalf("NewServerChain() failed: %v", err)
	}
	defer func() {
		if closeErr := chain.Close(); closeErr != nil {
			t.Errorf("Failed to close server chain: %v", closeErr)
		}
	}()

	if chain == nil {
		t.Fatal("NewServerChain() returned nil chain")
	}
	if chain.chain == nil {
		t.Error("ServerChain has nil internal chain")
	}
	if chain.cleanup == nil {
		t.Error("ServerChain has nil cleanup function")
	}
}

func TestNewServerChainStartsWithCompletionLogger(t *testing.T) {
	serverChain, err := NewServerChain()
	if err != nil {
		t.Fatalf("NewServerChain() failed: %v", err)
	}
	defer func() {
		if closeErr := serverChain.Close(); closeErr != nil {
			t.Errorf("Failed to close server chain: %v", closeErr)
		}
	}()

	if serverChain.completion == nil || len(serverChain.chain.interceptors) != 1 {
		t.Fatal("expected outer completion logger and inner DPU proxy")
	}
}

func TestServerChain_GetServerOptions(t *testing.T) {
	chain, err := NewServerChain()
	if err != nil {
		t.Fatalf("NewServerChain() failed: %v", err)
	}
	defer func() {
		if closeErr := chain.Close(); closeErr != nil {
			t.Errorf("Failed to close server chain: %v", closeErr)
		}
	}()

	opts := chain.GetServerOptions()
	if len(opts) == 0 {
		t.Error("GetServerOptions() returned empty options")
	}

	// Verify we get both unary and stream interceptors
	expectedOptCount := 2 // one explicit gRPC chain for unary and one for stream
	if len(opts) != expectedOptCount {
		t.Errorf("Expected %d server options, got %d", expectedOptCount, len(opts))
	}

	// Verify options are valid gRPC server options
	for i, opt := range opts {
		if opt == nil {
			t.Errorf("Server option at index %d is nil", i)
		}
	}
}

func TestServerChain_Close(t *testing.T) {
	chain, err := NewServerChain()
	if err != nil {
		t.Fatalf("NewServerChain() failed: %v", err)
	}

	// Test that Close() can be called successfully
	err = chain.Close()
	if err != nil {
		t.Errorf("Close() failed: %v", err)
	}

	// Test that multiple Close() calls are handled gracefully
	// (Redis client may return "client is closed" error, which is expected)
	_ = chain.Close() // Don't check error for second close
}

func TestServerChain_CloseNilCleanup(t *testing.T) {
	chain := &ServerChain{
		chain:   NewChain(),
		cleanup: nil,
	}

	// Test that Close() handles nil cleanup gracefully
	err := chain.Close()
	if err != nil {
		t.Errorf("Close() with nil cleanup failed: %v", err)
	}
}

func TestServerChain_GetServerOptionsAfterClose(t *testing.T) {
	chain, err := NewServerChain()
	if err != nil {
		t.Fatalf("NewServerChain() failed: %v", err)
	}

	// Close the chain first
	err = chain.Close()
	if err != nil {
		t.Fatalf("Close() failed: %v", err)
	}

	// GetServerOptions should still work after Close
	opts := chain.GetServerOptions()
	if len(opts) == 0 {
		t.Error("GetServerOptions() after Close() returned empty options")
	}
}
