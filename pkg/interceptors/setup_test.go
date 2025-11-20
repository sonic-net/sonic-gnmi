package interceptors

import (
	"testing"
)

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

func TestServerChain_GetServerOptions(t *testing.T) {
	chain, err := NewServerChain()
	if err != nil {
		t.Fatalf("NewServerChain() failed: %v", err)
	}
	defer chain.Close()

	opts := chain.GetServerOptions()
	if len(opts) == 0 {
		t.Error("GetServerOptions() returned empty options")
	}

	// Verify we get both unary and stream interceptors
	expectedOptCount := 2 // unary + stream interceptor
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
