package server

import (
	"net"
	"testing"
	"time"
)

func TestNewServer_ValidAddress(t *testing.T) {
	server, err := NewServer("localhost:0") // Use port 0 to let OS assign available port

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if server == nil {
		t.Error("Expected non-nil server")
	}
	if server.grpcServer == nil {
		t.Error("Expected non-nil grpcServer")
	}
	if server.listener == nil {
		t.Error("Expected non-nil listener")
	}

	// Clean up
	if server != nil && server.listener != nil {
		server.listener.Close()
	}
}

func TestNewServer_InvalidAddress(t *testing.T) {
	server, err := NewServer("invalid:address:format")

	if err == nil {
		t.Error("Expected error but got nil")
	}
	if server != nil {
		t.Error("Expected nil server on error")
	}
}

func TestServer_StartAndStop(t *testing.T) {
	// Create server on available port
	server, err := NewServer("localhost:0")
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	// Get the actual address
	addr := server.listener.Addr().String()

	// Start server in goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Start()
	}()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Verify server is listening
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Errorf("Failed to connect to server: %v", err)
	} else {
		conn.Close()
	}

	// Stop the server
	server.Stop()

	// Wait for server to stop
	select {
	case err := <-errCh:
		// Server should have stopped without error
		if err != nil {
			t.Logf("Server stopped with error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("Server did not stop within timeout")
	}
}

func TestServer_Stop_WithoutStart(t *testing.T) {
	// Create server
	server, err := NewServer("localhost:0")
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.listener.Close()

	// Stop without starting should not panic
	server.Stop()
}
