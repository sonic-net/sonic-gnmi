package e2e

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

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

// TestDownloadFirmware_E2E tests the firmware download RPCs end-to-end.
func TestDownloadFirmware_E2E(t *testing.T) {
	// Reset global download state before each test
	t.Cleanup(func() {
		// Need to import the server package to access the global state
		// For now, we'll work around this by ensuring tests don't interfere
	})

	// Setup temp directory for test
	tempDir := t.TempDir()

	// Create gRPC server
	grpcServer, lis := setupFirmwareGRPCServer(t, tempDir)
	defer grpcServer.Stop()

	// Create client connection
	conn, err := grpc.DialContext(context.Background(), "",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return lis.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	defer conn.Close()

	client := pb.NewFirmwareManagementClient(conn)

	t.Run("SuccessfulDownload", func(t *testing.T) {
		// Create mock HTTP server
		testContent := "test firmware content for e2e"
		httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(testContent)))
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(testContent))
		}))
		defer httpServer.Close()

		ctx := context.Background()

		// Start download
		downloadReq := &pb.DownloadFirmwareRequest{
			Url:                   httpServer.URL + "/test-firmware.bin",
			ConnectTimeoutSeconds: 10,
			TotalTimeoutSeconds:   30,
		}

		downloadResp, err := client.DownloadFirmware(ctx, downloadReq)
		require.NoError(t, err)
		require.NotNil(t, downloadResp)

		assert.NotEmpty(t, downloadResp.SessionId)
		assert.True(t, downloadResp.Status == "starting" || downloadResp.Status == "completed")
		assert.Contains(t, downloadResp.OutputPath, "test-firmware.bin")

		// Poll for status updates
		sessionId := downloadResp.SessionId
		statusReq := &pb.GetDownloadStatusRequest{
			SessionId: sessionId,
		}

		// Check initial status (starting or downloading)
		statusResp, err := client.GetDownloadStatus(ctx, statusReq)
		require.NoError(t, err)
		require.NotNil(t, statusResp)
		assert.Equal(t, sessionId, statusResp.SessionId)

		// Wait for download to complete (poll every 50ms for up to 2 seconds)
		var finalStatus *pb.GetDownloadStatusResponse
		for i := 0; i < 40; i++ {
			time.Sleep(50 * time.Millisecond)

			statusResp, err := client.GetDownloadStatus(ctx, statusReq)
			require.NoError(t, err)
			require.NotNil(t, statusResp)

			// Check if download completed
			if statusResp.GetResult() != nil {
				finalStatus = statusResp
				break
			}

			// Check for errors
			if statusResp.GetError() != nil {
				t.Fatalf("Download failed: %+v", statusResp.GetError())
			}

			// Should be in progress
			progress := statusResp.GetProgress()
			if progress != nil {
				t.Logf("Download progress: %d/%d bytes (%.1f%%) @ %.1f KB/s",
					progress.DownloadedBytes, progress.TotalBytes,
					progress.Percentage, progress.SpeedBytesPerSec/1024)
			}
		}

		// Verify download completed successfully
		require.NotNil(t, finalStatus, "Download did not complete within timeout")
		result := finalStatus.GetResult()
		require.NotNil(t, result, "Expected successful result")

		assert.Contains(t, result.FilePath, "test-firmware.bin")
		assert.Equal(t, int64(len(testContent)), result.FileSizeBytes)
		assert.GreaterOrEqual(t, result.DurationMs, int64(0))
		assert.Greater(t, result.AttemptCount, int32(0))
		assert.NotEmpty(t, result.FinalMethod)
		assert.Equal(t, httpServer.URL+"/test-firmware.bin", result.Url)

		// Verify file was actually created and has correct content
		downloadedContent, err := os.ReadFile(result.FilePath)
		require.NoError(t, err)
		assert.Equal(t, testContent, string(downloadedContent))
	})

	t.Run("StatusPolling", func(t *testing.T) {
		// Create mock HTTP server
		testContent := "status polling test content"
		httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(testContent)))
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(testContent))
		}))
		defer httpServer.Close()

		ctx := context.Background()

		// Start download
		downloadReq := &pb.DownloadFirmwareRequest{
			Url: httpServer.URL + "/status-test.bin",
		}

		downloadResp, err := client.DownloadFirmware(ctx, downloadReq)
		require.NoError(t, err)
		require.NotNil(t, downloadResp)

		// Poll status
		sessionId := downloadResp.SessionId
		statusReq := &pb.GetDownloadStatusRequest{
			SessionId: sessionId,
		}

		// Should get status response
		statusResp, err := client.GetDownloadStatus(ctx, statusReq)
		require.NoError(t, err)
		require.NotNil(t, statusResp)
		assert.Equal(t, sessionId, statusResp.SessionId)

		// Should have some state (starting, progress, or result)
		assert.True(t,
			statusResp.GetStarting() != nil ||
				statusResp.GetProgress() != nil ||
				statusResp.GetResult() != nil ||
				statusResp.GetError() != nil)
	})

	t.Run("DownloadError", func(t *testing.T) {
		// Create mock HTTP server that returns 404
		httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte("Not Found"))
		}))
		defer httpServer.Close()

		ctx := context.Background()

		// Start download that will fail
		downloadReq := &pb.DownloadFirmwareRequest{
			Url: httpServer.URL + "/nonexistent.bin",
		}

		downloadResp, err := client.DownloadFirmware(ctx, downloadReq)
		require.NoError(t, err)
		require.NotNil(t, downloadResp)

		// Wait for download to fail
		time.Sleep(200 * time.Millisecond)

		// Get status - should show error
		statusReq := &pb.GetDownloadStatusRequest{
			SessionId: downloadResp.SessionId,
		}

		statusResp, err := client.GetDownloadStatus(ctx, statusReq)
		require.NoError(t, err)
		require.NotNil(t, statusResp)

		downloadError := statusResp.GetError()
		require.NotNil(t, downloadError)
		assert.Equal(t, "http", downloadError.Category)
		assert.Equal(t, int32(404), downloadError.HttpCode)
		assert.Contains(t, downloadError.Message, "HTTP error")
		assert.NotEmpty(t, downloadError.Attempts)
	})
}
