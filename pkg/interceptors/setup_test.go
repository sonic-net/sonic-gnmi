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

func TestServerChainLogsProxyErrors(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	var proxyCalls []string
	completed := make(chan struct{}, 3)
	proxy := &mockInterceptor{name: "proxy", calls: &proxyCalls, shouldError: true}
	chain := &ServerChain{
		proxy: proxy,
		completion: newRPCCompletionLogger(func(line string) error {
			mu.Lock()
			defer mu.Unlock()
			lines = append(lines, line)
			completed <- struct{}{}
			return nil
		}),
	}
	srv := grpc.NewServer(chain.GetServerOptions()...)
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
	if _, err := client.Check(ctx, &healthpb.HealthCheckRequest{}); status.Code(err) != codes.Unknown {
		t.Fatal(err)
	}
	stream, err := client.Watch(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); status.Code(err) != codes.Unknown {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-completed:
		case <-ctx.Done():
			t.Fatal("RPC did not complete")
		}
	}
	proxy.shouldError = false
	if _, err := client.Check(ctx, &healthpb.HealthCheckRequest{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-completed:
	case <-ctx.Done():
		t.Fatal("successful RPC did not complete")
	}
	// Stop joins active RPCs before inspecting the call recorder and final logs.
	srv.Stop()
	if len(proxyCalls) != 3 {
		t.Fatalf("proxy calls: %v", proxyCalls)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(lines) != 3 || !strings.Contains(lines[0], `"code":"Unknown"`) || !strings.Contains(lines[1], `"code":"Unknown"`) || !strings.Contains(lines[2], `"code":"OK"`) {
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
	if chain.proxy == nil {
		t.Error("ServerChain has nil proxy")
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

	if serverChain.completion == nil || serverChain.proxy == nil {
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
