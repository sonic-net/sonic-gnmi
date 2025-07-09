package e2e

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/config"
	"github.com/sonic-net/sonic-gnmi/upgrade-service/pkg/server"
	pb "github.com/sonic-net/sonic-gnmi/upgrade-service/proto"
)

const bufSize = 1024 * 1024

// setupGRPCServer creates an in-memory gRPC server using bufconn
// and registers the SystemInfoServer.
func setupGRPCServer(t *testing.T) (*grpc.Server, *bufconn.Listener) {
	listener := bufconn.Listen(bufSize)
	grpcServer := grpc.NewServer()

	// Register SystemInfoServer
	systemInfoServer := server.NewSystemInfoServer()
	pb.RegisterSystemInfoServer(grpcServer, systemInfoServer)

	// Start server in background
	go func() {
		if err := grpcServer.Serve(listener); err != nil {
			t.Logf("Server exited with error: %v", err)
		}
	}()

	return grpcServer, listener
}

// createClient creates a gRPC client connected to the bufconn listener.
func createClient(ctx context.Context, listener *bufconn.Listener) (pb.SystemInfoClient, *grpc.ClientConn, error) {
	conn, err := grpc.DialContext(ctx, "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, nil, err
	}

	client := pb.NewSystemInfoClient(conn)
	return client, conn, nil
}

func TestGetPlatformType_E2E(t *testing.T) {
	tests := []struct {
		name                       string
		machineConfContent         string
		expectedPlatformIdentifier string
	}{
		{
			name: "Success - Mellanox Platform",
			machineConfContent: `onie_platform=x86_64-mlnx_msn4600c-r0
onie_machine=mlnx_msn4600c
onie_arch=x86_64
onie_switch_asic=mlnx`,
			expectedPlatformIdentifier: "x86_64-mlnx_msn4600c-r0",
		},
		{
			name: "Success - Arista Platform",
			machineConfContent: `aboot_vendor=arista
aboot_platform=x86_64-arista_7060x6_64pe
aboot_machine=arista_7060x6_64pe
aboot_arch=x86_64`,
			expectedPlatformIdentifier: "x86_64-arista_7060x6_64pe",
		},
		{
			name: "Generic Platform",
			machineConfContent: `onie_platform=x86_64-generic_platform-r0
onie_machine=generic_platform
onie_arch=x86_64
onie_switch_asic=unknown`,
			expectedPlatformIdentifier: "x86_64-generic_platform-r0",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Set up temporary directory and machine.conf
			tempDir := t.TempDir()
			machineConfPath := filepath.Join(tempDir, "host", "machine.conf")

			// Create directory
			err := os.MkdirAll(filepath.Dir(machineConfPath), 0755)
			require.NoError(t, err)

			// Write machine.conf
			err = os.WriteFile(machineConfPath, []byte(test.machineConfContent), 0644)
			require.NoError(t, err)

			// Mock config
			originalConfig := config.Global
			config.Global = &config.Config{RootFS: tempDir}
			defer func() { config.Global = originalConfig }()

			// Setup gRPC server
			grpcServer, listener := setupGRPCServer(t)
			defer grpcServer.Stop()

			// Create client
			ctx := context.Background()
			client, conn, err := createClient(ctx, listener)
			require.NoError(t, err)
			defer conn.Close()

			// Make request
			resp, err := client.GetPlatformType(ctx, &pb.GetPlatformTypeRequest{})
			require.NoError(t, err)
			require.NotNil(t, resp)

			// Verify response
			assert.Equal(t, test.expectedPlatformIdentifier, resp.PlatformIdentifier)
		})
	}
}

func TestGetDiskSpace_E2E(t *testing.T) {
	tests := []struct {
		name         string
		setupDirs    []string
		requestPaths []string
		expectError  bool
	}{
		{
			name:         "Success - Default paths",
			setupDirs:    []string{"host", "tmp"},
			requestPaths: nil, // Use default paths
			expectError:  false,
		},
		{
			name:         "Success - Custom paths",
			setupDirs:    []string{"custom1", "custom2"},
			requestPaths: []string{"/custom1", "/custom2"},
			expectError:  false,
		},
		{
			name:         "Mixed - Some paths exist",
			setupDirs:    []string{"existing"},
			requestPaths: []string{"/existing", "/nonexistent"},
			expectError:  false, // Should not error, just populate error_message for nonexistent
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Set up temporary directory structure
			tempDir := t.TempDir()

			// Create the requested directories
			for _, dir := range test.setupDirs {
				fullPath := filepath.Join(tempDir, dir)
				err := os.MkdirAll(fullPath, 0755)
				require.NoError(t, err)
			}

			// Mock config
			originalConfig := config.Global
			config.Global = &config.Config{RootFS: tempDir}
			defer func() { config.Global = originalConfig }()

			// Setup gRPC server
			grpcServer, listener := setupGRPCServer(t)
			defer grpcServer.Stop()

			// Create client
			ctx := context.Background()
			client, conn, err := createClient(ctx, listener)
			require.NoError(t, err)
			defer conn.Close()

			// Make request
			req := &pb.GetDiskSpaceRequest{
				Paths: test.requestPaths,
			}
			resp, err := client.GetDiskSpace(ctx, req)

			if test.expectError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, resp)

			// Verify response structure
			assert.NotEmpty(t, resp.Filesystems, "Should have at least one filesystem result")

			// Verify each filesystem result
			for _, fs := range resp.Filesystems {
				assert.NotEmpty(t, fs.Path, "Path should not be empty")

				if fs.ErrorMessage == "" {
					// Successful case - should have valid disk space values
					assert.Greater(t, fs.TotalMb, int64(0), "Total space should be positive for %s", fs.Path)
					assert.GreaterOrEqual(t, fs.FreeMb, int64(0), "Free space should be non-negative for %s", fs.Path)
					assert.GreaterOrEqual(t, fs.UsedMb, int64(0), "Used space should be non-negative for %s", fs.Path)
					assert.Equal(t, fs.TotalMb, fs.FreeMb+fs.UsedMb, "Total should equal Free + Used for %s", fs.Path)
				} else {
					// Error case - disk space values should be zero
					assert.Equal(t, int64(0), fs.TotalMb, "Total should be zero on error for %s", fs.Path)
					assert.Equal(t, int64(0), fs.FreeMb, "Free should be zero on error for %s", fs.Path)
					assert.Equal(t, int64(0), fs.UsedMb, "Used should be zero on error for %s", fs.Path)
				}
			}

			// For default paths test, verify we get results for expected paths
			if test.requestPaths == nil {
				expectedPaths := []string{"/", "/host", "/tmp"}
				assert.Len(t, resp.Filesystems, len(expectedPaths), "Should have results for all default paths")

				pathsSeen := make(map[string]bool)
				for _, fs := range resp.Filesystems {
					pathsSeen[fs.Path] = true
				}

				for _, expectedPath := range expectedPaths {
					assert.True(t, pathsSeen[expectedPath], "Should have result for path %s", expectedPath)
				}
			}

			// For custom paths test, verify we get results for requested paths
			if test.requestPaths != nil {
				assert.Len(t, resp.Filesystems, len(test.requestPaths), "Should have results for all requested paths")

				pathsSeen := make(map[string]bool)
				for _, fs := range resp.Filesystems {
					pathsSeen[fs.Path] = true
				}

				for _, requestedPath := range test.requestPaths {
					assert.True(t, pathsSeen[requestedPath], "Should have result for requested path %s", requestedPath)
				}
			}
		})
	}
}
