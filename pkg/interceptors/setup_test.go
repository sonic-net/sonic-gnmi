package interceptors

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

func TestServerChainLogsChainedDenials(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	var innerCalls []string
	chain := &ServerChain{
		chain: NewChain(&mockInterceptor{name: "inner", calls: &innerCalls}),
		completion: newRPCCompletionLogger(func(line string) error {
			mu.Lock()
			defer mu.Unlock()
			lines = append(lines, line)
			return nil
		}),
	}
	// Middleware prepended by the caller must run before the inner chain, but
	// its failures must still produce exactly one completion record per RPC.
	opts := []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(func(context.Context, interface{}, *grpc.UnaryServerInfo, grpc.UnaryHandler) (interface{}, error) {
			return nil, status.Error(codes.PermissionDenied, "denied")
		}),
		grpc.ChainStreamInterceptor(func(interface{}, grpc.ServerStream, *grpc.StreamServerInfo, grpc.StreamHandler) error {
			return status.Error(codes.Unauthenticated, "revoked")
		}),
	}
	srv := grpc.NewServer(append(opts, chain.GetServerOptions()...)...)
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
	// Stop joins active RPCs before inspecting the inner-chain call recorder.
	srv.Stop()
	if len(innerCalls) != 0 {
		t.Fatalf("denied RPC entered inner chain: %v", innerCalls)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(lines) != 2 || !strings.Contains(lines[0], `"code":"PermissionDenied"`) || !strings.Contains(lines[1], `"code":"Unauthenticated"`) {
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
	expectedOptCount := 4 // outer completion + inner chain, unary and stream
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
