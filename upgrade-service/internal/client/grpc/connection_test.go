package grpc

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// mockGRPCServer creates a test gRPC server using bufconn.
func mockGRPCServer(t *testing.T) (*grpc.Server, string) {
	lis := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()

	go func() {
		if err := server.Serve(lis); err != nil {
			t.Logf("Server exited with error: %v", err)
		}
	}()

	// Return a special address that we can intercept in tests
	return server, "bufconn"
}

// bufDialer creates a custom dialer for bufconn connections.
func bufDialer(lis *bufconn.Listener) func(context.Context, string) (net.Conn, error) {
	return func(ctx context.Context, address string) (net.Conn, error) {
		return lis.DialContext(ctx)
	}
}

func TestNewConnection_ValidateConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  ConnectionConfig
		wantErr string
	}{
		{
			name:    "empty address",
			config:  ConnectionConfig{},
			wantErr: "address is required",
		},
		{
			name: "valid minimal config",
			config: ConnectionConfig{
				Address:        "localhost:50051",
				ConnectTimeout: 100 * time.Millisecond, // Short timeout for testing
				MaxRetries:     0,                      // No retries for testing
			},
			wantErr: "", // Will fail to connect but config is valid
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewConnection(tt.config)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			}
			// Note: We expect connection failures for valid config since no server is running
		})
	}
}

func TestConnectionConfig_Defaults(t *testing.T) {
	config := ConnectionConfig{
		Address:        "localhost:50051",
		ConnectTimeout: 100 * time.Millisecond, // Short timeout for testing
		MaxRetries:     0,                      // No retries for testing
	}

	// This will fail to connect, but we can check that defaults were set
	_, err := NewConnection(config)

	// Connection will fail, but we can verify the config was processed
	assert.Error(t, err) // Expected since no server is running

	// Check that defaults would be set (we can't access them directly, but we can test behavior)
	assert.Contains(t, err.Error(), "failed to establish connection")
}

func TestConnection_Close(t *testing.T) {
	conn := &Connection{}

	// Test closing nil connection
	err := conn.Close()
	assert.NoError(t, err)

	// Test closing with mock connection (don't use WithBlock to avoid hanging)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	mockConn, err := grpc.DialContext(ctx, "localhost:0", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err == nil {
		conn.conn = mockConn
		err = conn.Close()
		assert.NoError(t, err)
		assert.Nil(t, conn.conn)
	}
}

func TestConnection_IsConnected(t *testing.T) {
	conn := &Connection{}

	// Test with nil connection
	assert.False(t, conn.IsConnected())

	// Test with closed connection
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	mockConn, err := grpc.DialContext(ctx, "localhost:0", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err == nil {
		conn.conn = mockConn
		conn.conn.Close()

		// Give it a moment to register as closed
		time.Sleep(10 * time.Millisecond)

		// Should eventually be false, but might be in intermediate state
		connected := conn.IsConnected()
		t.Logf("Connection state after close: %v", connected)

		conn.conn = nil
	}
}

func TestConnection_TLSConfig(t *testing.T) {
	tests := []struct {
		name     string
		config   ConnectionConfig
		wantFail bool
	}{
		{
			name: "TLS enabled without certs",
			config: ConnectionConfig{
				Address:         "localhost:50051",
				TLSEnabled:      true,
				InsecureSkipTLS: true, // For testing
				ConnectTimeout:  100 * time.Millisecond,
				MaxRetries:      1,
			},
			wantFail: true, // Will fail to connect but TLS config should be valid
		},
		{
			name: "TLS disabled",
			config: ConnectionConfig{
				Address:        "localhost:50051",
				TLSEnabled:     false,
				ConnectTimeout: 100 * time.Millisecond,
				MaxRetries:     1,
			},
			wantFail: true, // Will fail to connect but config should be valid
		},
		{
			name: "TLS with invalid cert files",
			config: ConnectionConfig{
				Address:        "localhost:50051",
				TLSEnabled:     true,
				TLSCertFile:    "/nonexistent/cert.pem",
				TLSKeyFile:     "/nonexistent/key.pem",
				ConnectTimeout: 100 * time.Millisecond,
				MaxRetries:     1,
			},
			wantFail: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewConnection(tt.config)
			if tt.wantFail {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestConnection_RetryLogic(t *testing.T) {
	config := ConnectionConfig{
		Address:        "localhost:0", // Invalid port to force failure
		TLSEnabled:     false,
		ConnectTimeout: 50 * time.Millisecond,
		MaxRetries:     2,
		RetryDelay:     10 * time.Millisecond,
	}

	start := time.Now()
	_, err := NewConnection(config)
	duration := time.Since(start)

	// Should fail with single attempt (no retries at connection level)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to establish connection")

	// Should fail quickly with single attempt (no retries at connection level)
	maxExpectedDuration := config.ConnectTimeout + 100*time.Millisecond // Allow some overhead
	assert.LessOrEqual(t, duration, maxExpectedDuration,
		"Connection should have failed quickly within %v, but took %v",
		maxExpectedDuration, duration)
}

func TestConnection_Reconnect(t *testing.T) {
	conn := &Connection{
		config: ConnectionConfig{
			Address:        "localhost:0", // Invalid to ensure failure
			TLSEnabled:     false,
			ConnectTimeout: 50 * time.Millisecond,
			MaxRetries:     1,
		},
	}

	// Test reconnect with no existing connection
	err := conn.Reconnect()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to connect")

	// Test reconnect with existing connection
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	mockConn, err := grpc.DialContext(ctx, "localhost:0", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err == nil {
		conn.conn = mockConn

		err = conn.Reconnect()
		assert.Error(t, err)     // Should fail due to invalid address
		assert.Nil(t, conn.conn) // Connection should be cleared
	}
}

func TestConnection_RealServerConnection(t *testing.T) {
	// Start a real gRPC server for testing
	lis, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)

	server := grpc.NewServer()
	go func() {
		server.Serve(lis)
	}()
	defer server.Stop()

	// Test successful connection
	config := ConnectionConfig{
		Address:        lis.Addr().String(),
		TLSEnabled:     false,
		ConnectTimeout: 2 * time.Second,
		MaxRetries:     1,
	}

	conn, err := NewConnection(config)
	require.NoError(t, err)
	require.NotNil(t, conn)

	// Test that connection is active
	assert.True(t, conn.IsConnected())
	assert.NotNil(t, conn.Conn())

	// Test cleanup
	err = conn.Close()
	assert.NoError(t, err)
}

func TestConnectionConfig_TLSCertificateLoading(t *testing.T) {
	// This test verifies the TLS certificate loading logic
	// We expect it to fail since we're using fake cert paths
	config := ConnectionConfig{
		Address:        "localhost:50051",
		TLSEnabled:     true,
		TLSCertFile:    "/fake/cert.pem",
		TLSKeyFile:     "/fake/key.pem",
		ConnectTimeout: 100 * time.Millisecond,
		MaxRetries:     0, // No retries to speed up test
	}

	_, err := NewConnection(config)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load TLS certificates")
}
