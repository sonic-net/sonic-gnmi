package loopback

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	clientGnmi "github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/client/gnmi"
)

// TestGNMIDiskSpaceLoopback tests the complete client-server loopback
// for the gNMI disk space retrieval functionality.
func TestGNMIDiskSpaceLoopback(t *testing.T) {
	// Setup test infrastructure
	tempDir := t.TempDir()
	testServer := SetupInsecureTestServer(t, tempDir, []string{"gnmi"})
	defer testServer.Stop()

	client := SetupGNMIClient(t, testServer.Addr, 10*time.Second)
	defer client.Close()

	// Test gNMI Capabilities loopback
	ctx, cancel := WithTestTimeout(10 * time.Second)
	defer cancel()

	t.Run("capabilities", func(t *testing.T) {
		resp, err := client.Capabilities(ctx)
		require.NoError(t, err, "Capabilities RPC failed")
		require.NotNil(t, resp)

		assert.Equal(t, "0.7.0", resp.GNMIVersion, "Unexpected gNMI version")
		assert.Empty(t, resp.SupportedModels, "No YANG models should be registered without proper schema definitions")
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
}

// TestGNMIDiskSpaceLoopback_InvalidPath tests error handling for invalid filesystem paths.
func TestGNMIDiskSpaceLoopback_InvalidPath(t *testing.T) {
	// Setup test infrastructure
	tempDir := t.TempDir()
	testServer := SetupInsecureTestServer(t, tempDir, []string{"gnmi"})
	defer testServer.Stop()

	client := SetupGNMIClient(t, testServer.Addr, 10*time.Second)
	defer client.Close()

	// Test GetDiskSpace with invalid path - should fail
	ctx, cancel := WithTestTimeout(10 * time.Second)
	defer cancel()

	_, err := client.GetDiskSpace(ctx, "/non/existent/path/that/should/not/exist")
	assert.Error(t, err, "GetDiskSpace should fail with invalid path")
	assert.Contains(t, err.Error(), "failed to retrieve disk space", "Error should mention disk space retrieval failure")
}

// TestGNMIDiskSpaceLoopback_EmptyPath tests error handling for empty filesystem paths.
func TestGNMIDiskSpaceLoopback_EmptyPath(t *testing.T) {
	// Setup test infrastructure
	tempDir := t.TempDir()
	testServer := SetupInsecureTestServer(t, tempDir, []string{"gnmi"})
	defer testServer.Stop()

	client := SetupGNMIClient(t, testServer.Addr, 10*time.Second)
	defer client.Close()

	// Test GetDiskSpace with empty path - should fail
	ctx, cancel := WithTestTimeout(10 * time.Second)
	defer cancel()

	_, err := client.GetDiskSpace(ctx, "")
	assert.Error(t, err, "GetDiskSpace should fail with empty path")
	assert.Contains(t, err.Error(), "filesystem path is required", "Error should mention missing path")
}

// TestGNMIDiskSpaceLoopback_RootFS tests disk space with rootFS path resolution.
func TestGNMIDiskSpaceLoopback_RootFS(t *testing.T) {
	// Setup test infrastructure with root filesystem
	testServer := SetupInsecureTestServer(t, "/", []string{"gnmi"}) // Use root filesystem for this test
	defer testServer.Stop()

	client := SetupGNMIClient(t, testServer.Addr, 10*time.Second)
	defer client.Close()

	// Test disk space for root filesystem
	ctx, cancel := WithTestTimeout(10 * time.Second)
	defer cancel()

	diskInfo, err := client.GetDiskSpace(ctx, "/")
	require.NoError(t, err)

	assert.Equal(t, "/", diskInfo.Path)
	assert.Greater(t, diskInfo.TotalMB, int64(0))
	assert.GreaterOrEqual(t, diskInfo.AvailableMB, int64(0))

	t.Logf("Root filesystem disk space: %d MB total, %d MB available", diskInfo.TotalMB, diskInfo.AvailableMB)
}

// TestGNMIDiskSpaceLoopback_MultipleClients tests concurrent client access.
func TestGNMIDiskSpaceLoopback_MultipleClients(t *testing.T) {
	// Setup test infrastructure
	tempDir := t.TempDir()
	testServer := SetupInsecureTestServer(t, tempDir, []string{"gnmi"})
	defer testServer.Stop()

	// Create multiple clients concurrently
	const numClients = 5
	results := make(chan error, numClients)

	for i := 0; i < numClients; i++ {
		go func(clientID int) {
			client, err := clientGnmi.NewClient(&clientGnmi.ClientConfig{
				Target:  testServer.Addr,
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
}
