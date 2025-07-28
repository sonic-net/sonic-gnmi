package loopback

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	gnmiclient "github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/client/gnmi"
	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/server"
	serverConfig "github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/server/config"
)

// TestGNMIDiskSpaceLoopback tests the complete client-server loopback
// for the gNMI disk space retrieval functionality.
func TestGNMIDiskSpaceLoopback(t *testing.T) {
	// Create temporary directory for rootFS
	tempDir := t.TempDir()

	// Find an available port for gRPC server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	serverAddr := fmt.Sprintf("127.0.0.1:%d", port)

	// Initialize server configuration
	serverConfig.Global = &serverConfig.Config{
		Addr:        serverAddr,
		RootFS:      tempDir, // Use temp dir as root filesystem
		TLSEnabled:  false,
		TLSCertFile: "",
		TLSKeyFile:  "",
	}

	// Create and start gRPC server with gNMI service
	srv, err := server.NewServerBuilder().
		EnableGNMI().
		Build()
	require.NoError(t, err, "Failed to create server")

	// Start server in background
	go func() {
		if err := srv.Start(); err != nil {
			t.Logf("Server error: %v", err)
		}
	}()

	// Give server time to start and bind to port
	time.Sleep(200 * time.Millisecond)

	// Verify server is listening
	conn, err := grpc.Dial(serverAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err, "Failed to connect to gRPC server")
	conn.Close()

	// Create gNMI client
	client, err := gnmiclient.NewClient(&gnmiclient.ClientConfig{
		Target:  serverAddr,
		Timeout: 10 * time.Second,
	})
	require.NoError(t, err, "Failed to create gNMI client")
	defer client.Close()

	// Test gNMI Capabilities loopback
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	t.Run("capabilities", func(t *testing.T) {
		resp, err := client.Capabilities(ctx)
		require.NoError(t, err, "Capabilities RPC failed")
		require.NotNil(t, resp)

		assert.Equal(t, "0.7.0", resp.GNMIVersion, "Unexpected gNMI version")
		assert.NotEmpty(t, resp.SupportedModels, "No supported models")
		assert.Equal(t, "sonic-system", resp.SupportedModels[0].Name, "Unexpected model name")
		assert.Equal(t, "SONiC", resp.SupportedModels[0].Organization, "Unexpected organization")
		assert.Equal(t, "1.0.0", resp.SupportedModels[0].Version, "Unexpected model version")
	})

	t.Run("disk_space_tempdir", func(t *testing.T) {
		// Test disk space for temp directory (using "." which resolves to tempDir)
		diskInfo, err := client.GetDiskSpace(ctx, ".")
		require.NoError(t, err, "GetDiskSpace RPC failed")
		require.NotNil(t, diskInfo)

		assert.Equal(t, ".", diskInfo.Path, "Path mismatch")
		assert.Greater(t, diskInfo.TotalMB, int64(0), "Total MB should be positive")
		assert.GreaterOrEqual(t, diskInfo.AvailableMB, int64(0), "Available MB should be non-negative")
		assert.LessOrEqual(t, diskInfo.AvailableMB, diskInfo.TotalMB, "Available should not exceed total")

		t.Logf("Disk space for temp dir: %d MB total, %d MB available", diskInfo.TotalMB, diskInfo.AvailableMB)
	})

	t.Run("disk_space_current_dir", func(t *testing.T) {
		// Test disk space for current directory
		diskInfo, err := client.GetDiskSpace(ctx, ".")
		require.NoError(t, err, "GetDiskSpace RPC failed")

		assert.Equal(t, ".", diskInfo.Path, "Path mismatch")
		assert.Greater(t, diskInfo.TotalMB, int64(0), "Total MB should be positive")
		assert.GreaterOrEqual(t, diskInfo.AvailableMB, int64(0), "Available MB should be non-negative")
	})

	t.Run("disk_space_total_only", func(t *testing.T) {
		// Test total disk space only
		totalMB, err := client.GetDiskSpaceTotal(ctx, ".")
		require.NoError(t, err, "GetDiskSpaceTotal RPC failed")

		assert.Greater(t, totalMB, int64(0), "Total MB should be positive")
	})

	t.Run("disk_space_available_only", func(t *testing.T) {
		// Test available disk space only
		availableMB, err := client.GetDiskSpaceAvailable(ctx, ".")
		require.NoError(t, err, "GetDiskSpaceAvailable RPC failed")

		assert.GreaterOrEqual(t, availableMB, int64(0), "Available MB should be non-negative")
	})

	// Stop server
	srv.Stop()
}

// TestGNMIDiskSpaceLoopback_InvalidPath tests error handling for invalid filesystem paths.
func TestGNMIDiskSpaceLoopback_InvalidPath(t *testing.T) {
	// Setup server
	tempDir := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	serverAddr := fmt.Sprintf("127.0.0.1:%d", port)

	serverConfig.Global = &serverConfig.Config{
		Addr:       serverAddr,
		RootFS:     tempDir, // Use temp dir as root filesystem
		TLSEnabled: false,
	}

	srv, err := server.NewServerBuilder().
		EnableGNMI().
		Build()
	require.NoError(t, err)

	go srv.Start()
	time.Sleep(200 * time.Millisecond)

	// Create client
	client, err := gnmiclient.NewClient(&gnmiclient.ClientConfig{
		Target:  serverAddr,
		Timeout: 10 * time.Second,
	})
	require.NoError(t, err)
	defer client.Close()

	// Test GetDiskSpace with invalid path - should fail
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err = client.GetDiskSpace(ctx, "/non/existent/path/that/should/not/exist")
	assert.Error(t, err, "GetDiskSpace should fail with invalid path")
	assert.Contains(t, err.Error(), "failed to retrieve disk space", "Error should mention disk space retrieval failure")

	srv.Stop()
}

// TestGNMIDiskSpaceLoopback_EmptyPath tests error handling for empty filesystem paths.
func TestGNMIDiskSpaceLoopback_EmptyPath(t *testing.T) {
	// Setup server
	tempDir := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	serverAddr := fmt.Sprintf("127.0.0.1:%d", port)

	serverConfig.Global = &serverConfig.Config{
		Addr:       serverAddr,
		RootFS:     tempDir, // Use temp dir as root filesystem
		TLSEnabled: false,
	}

	srv, err := server.NewServerBuilder().
		EnableGNMI().
		Build()
	require.NoError(t, err)

	go srv.Start()
	time.Sleep(200 * time.Millisecond)

	// Create client
	client, err := gnmiclient.NewClient(&gnmiclient.ClientConfig{
		Target:  serverAddr,
		Timeout: 10 * time.Second,
	})
	require.NoError(t, err)
	defer client.Close()

	// Test GetDiskSpace with empty path - should fail
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err = client.GetDiskSpace(ctx, "")
	assert.Error(t, err, "GetDiskSpace should fail with empty path")
	assert.Contains(t, err.Error(), "filesystem path is required", "Error should mention missing path")

	srv.Stop()
}

// TestGNMIDiskSpaceLoopback_RootFS tests disk space with rootFS path resolution.
func TestGNMIDiskSpaceLoopback_RootFS(t *testing.T) {
	// Setup server with custom rootFS
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	serverAddr := fmt.Sprintf("127.0.0.1:%d", port)

	serverConfig.Global = &serverConfig.Config{
		Addr:       serverAddr,
		RootFS:     "/", // Use root filesystem for this test
		TLSEnabled: false,
	}

	srv, err := server.NewServerBuilder().
		EnableGNMI().
		Build()
	require.NoError(t, err)

	go srv.Start()
	time.Sleep(200 * time.Millisecond)

	// Create client
	client, err := gnmiclient.NewClient(&gnmiclient.ClientConfig{
		Target:  serverAddr,
		Timeout: 10 * time.Second,
	})
	require.NoError(t, err)
	defer client.Close()

	// Test disk space for root filesystem
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	diskInfo, err := client.GetDiskSpace(ctx, "/")
	require.NoError(t, err)

	assert.Equal(t, "/", diskInfo.Path)
	assert.Greater(t, diskInfo.TotalMB, int64(0))
	assert.GreaterOrEqual(t, diskInfo.AvailableMB, int64(0))

	t.Logf("Root filesystem disk space: %d MB total, %d MB available", diskInfo.TotalMB, diskInfo.AvailableMB)

	srv.Stop()
}

// TestGNMIDiskSpaceLoopback_MultipleClients tests concurrent client access.
func TestGNMIDiskSpaceLoopback_MultipleClients(t *testing.T) {
	// Setup server
	tempDir := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	serverAddr := fmt.Sprintf("127.0.0.1:%d", port)

	serverConfig.Global = &serverConfig.Config{
		Addr:       serverAddr,
		RootFS:     tempDir, // Use temp dir as root filesystem
		TLSEnabled: false,
	}

	srv, err := server.NewServerBuilder().
		EnableGNMI().
		Build()
	require.NoError(t, err)

	go srv.Start()
	time.Sleep(200 * time.Millisecond)

	// Create multiple clients concurrently
	const numClients = 5
	results := make(chan error, numClients)

	for i := 0; i < numClients; i++ {
		go func(clientID int) {
			client, err := gnmiclient.NewClient(&gnmiclient.ClientConfig{
				Target:  serverAddr,
				Timeout: 10 * time.Second,
			})
			if err != nil {
				results <- fmt.Errorf("client %d: failed to create client: %w", clientID, err)
				return
			}
			defer client.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			// Test capabilities
			_, err = client.Capabilities(ctx)
			if err != nil {
				results <- fmt.Errorf("client %d: capabilities failed: %w", clientID, err)
				return
			}

			// Test disk space
			_, err = client.GetDiskSpace(ctx, ".")
			if err != nil {
				results <- fmt.Errorf("client %d: disk space failed: %w", clientID, err)
				return
			}

			results <- nil
		}(i)
	}

	// Wait for all clients to complete
	for i := 0; i < numClients; i++ {
		select {
		case err := <-results:
			assert.NoError(t, err, "Client operation failed")
		case <-time.After(15 * time.Second):
			t.Fatal("Timeout waiting for clients to complete")
		}
	}

	srv.Stop()
}