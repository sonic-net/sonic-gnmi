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

	"github.com/sonic-net/sonic-gnmi/upgrade-service/pkg/server"
	pb "github.com/sonic-net/sonic-gnmi/upgrade-service/proto"
)

// setupFirmwareGRPCServer creates an in-memory gRPC server with firmware management.
func setupFirmwareGRPCServer(t *testing.T, rootFS string) (*grpc.Server, *bufconn.Listener) {
	t.Helper()

	// Create a buffer connection for in-memory gRPC
	lis := bufconn.Listen(bufSize)

	// Create a new gRPC server
	grpcServer := grpc.NewServer()

	// Register FirmwareManagementServer
	firmwareServer := server.NewFirmwareManagementServer(rootFS)
	pb.RegisterFirmwareManagementServer(grpcServer, firmwareServer)

	// Start the server
	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			t.Errorf("failed to serve: %v", err)
		}
	}()

	return grpcServer, lis
}

// TestCleanupOldFirmware_E2E tests the CleanupOldFirmware RPC end-to-end.
func TestCleanupOldFirmware_E2E(t *testing.T) {
	testCases := []struct {
		name          string
		setupFiles    func(string) ([]string, []string) // returns (created files, expected deleted files)
		expectError   bool
		expectDeleted int32
	}{
		{
			name: "No files to cleanup",
			setupFiles: func(tempDir string) ([]string, []string) {
				return []string{}, []string{}
			},
			expectError:   false,
			expectDeleted: 0,
		},
		{
			name: "Cleanup firmware files from host and tmp",
			setupFiles: func(tempDir string) ([]string, []string) {
				hostDir := filepath.Join(tempDir, "host")
				tmpDir := filepath.Join(tempDir, "tmp")

				// Create directories
				require.NoError(t, os.MkdirAll(hostDir, 0755))
				require.NoError(t, os.MkdirAll(tmpDir, 0755))

				// Files to create
				filesToCreate := []string{
					filepath.Join(hostDir, "sonic.bin"),
					filepath.Join(hostDir, "installer.swi"),
					filepath.Join(tmpDir, "package.rpm"),
					filepath.Join(hostDir, "keep.txt"), // Should not be deleted
				}

				// Create test files
				for _, file := range filesToCreate {
					require.NoError(t, os.WriteFile(file, []byte("test content"), 0644))
				}

				// Expected deleted files (first 3)
				expectedDeleted := filesToCreate[:3]

				return filesToCreate, expectedDeleted
			},
			expectError:   false,
			expectDeleted: 3,
		},
		{
			name: "Mixed file types in multiple directories",
			setupFiles: func(tempDir string) ([]string, []string) {
				hostDir := filepath.Join(tempDir, "host")
				tmpDir := filepath.Join(tempDir, "tmp")

				require.NoError(t, os.MkdirAll(hostDir, 0755))
				require.NoError(t, os.MkdirAll(tmpDir, 0755))

				filesToCreate := []string{
					filepath.Join(hostDir, "firmware1.bin"),
					filepath.Join(hostDir, "firmware2.swi"),
					filepath.Join(tmpDir, "package1.rpm"),
					filepath.Join(tmpDir, "package2.bin"),
					filepath.Join(hostDir, "config.json"), // Should not be deleted
				}

				for _, file := range filesToCreate {
					require.NoError(t, os.WriteFile(file, []byte("test content"), 0644))
				}

				expectedDeleted := []string{
					filesToCreate[0], filesToCreate[1],
					filesToCreate[2], filesToCreate[3],
				}

				return filesToCreate, expectedDeleted
			},
			expectError:   false,
			expectDeleted: 4,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create temporary directory for this test
			tempDir := t.TempDir()

			// Set up files
			createdFiles, expectedDeleted := tc.setupFiles(tempDir)

			// Set up server with temp directory as rootFS
			srv, lis := setupFirmwareGRPCServer(t, tempDir)
			defer srv.Stop()

			// Set up client
			ctx := context.Background()
			conn, err := grpc.DialContext(ctx, "bufnet",
				grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
					return lis.Dial()
				}),
				grpc.WithTransportCredentials(insecure.NewCredentials()),
			)
			require.NoError(t, err)
			defer conn.Close()

			// Create client
			client := pb.NewFirmwareManagementClient(conn)

			// Make the RPC call
			resp, err := client.CleanupOldFirmware(ctx, &pb.CleanupOldFirmwareRequest{})

			// Check results
			if tc.expectError {
				assert.Error(t, err)
				return
			}

			// Should not have error
			assert.NoError(t, err)
			assert.NotNil(t, resp)

			// Check response fields
			assert.Equal(t, tc.expectDeleted, resp.FilesDeleted)
			assert.Equal(t, int(tc.expectDeleted), len(resp.DeletedFiles))
			assert.Empty(t, resp.Errors) // No errors expected in these tests

			// If files were expected to be deleted, check space freed
			if tc.expectDeleted > 0 {
				assert.Greater(t, resp.SpaceFreedBytes, int64(0))
			} else {
				assert.Equal(t, int64(0), resp.SpaceFreedBytes)
			}

			// Verify actual file deletion
			verifyFilesDeleted(t, expectedDeleted)
			verifyFilesKept(t, createdFiles, expectedDeleted)
		})
	}
}

func verifyFilesDeleted(t *testing.T, expectedDeleted []string) {
	for _, file := range expectedDeleted {
		_, err := os.Stat(file)
		assert.True(t, os.IsNotExist(err), "File %s should have been deleted", file)
	}
}

func verifyFilesKept(t *testing.T, createdFiles, expectedDeleted []string) {
	for _, file := range createdFiles {
		if !contains(expectedDeleted, file) {
			_, err := os.Stat(file)
			assert.NoError(t, err, "File %s should not have been deleted", file)
		}
	}
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
