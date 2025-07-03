package e2e

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

// waitForDownloadCompletion waits for any active download to complete.
func waitForDownloadCompletion(
	t *testing.T,
	client pb.FirmwareManagementClient,
	sessionId string,
	maxWait time.Duration,
) {
	if sessionId == "" {
		return
	}

	ctx := context.Background()
	statusReq := &pb.GetDownloadStatusRequest{SessionId: sessionId}

	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		statusResp, err := client.GetDownloadStatus(ctx, statusReq)
		if err != nil {
			return // Session not found, download likely completed or failed
		}

		if statusResp.GetResult() != nil || statusResp.GetError() != nil {
			return // Download completed
		}

		time.Sleep(50 * time.Millisecond)
	}
}

// setupDownloadTestClient creates a test client for download tests.
func setupDownloadTestClient(t *testing.T) pb.FirmwareManagementClient {
	tempDir := t.TempDir()
	grpcServer, lis := setupFirmwareGRPCServer(t, tempDir)
	t.Cleanup(func() { grpcServer.Stop() })

	conn, err := grpc.DialContext(context.Background(), "",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return lis.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	return pb.NewFirmwareManagementClient(conn)
}

// TestDownloadFirmware_E2E tests the firmware download RPCs end-to-end.
func TestDownloadFirmware_E2E(t *testing.T) {
	client := setupDownloadTestClient(t)

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

		// Wait for this download to complete
		for i := 0; i < 50; i++ {
			statusResp, err := client.GetDownloadStatus(ctx, statusReq)
			if err != nil || statusResp.GetResult() != nil || statusResp.GetError() != nil {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
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

// TestDownloadFirmware_SessionIdFix tests that sessionId is returned immediately.
func TestDownloadFirmware_SessionIdFix(t *testing.T) {
	client := setupDownloadTestClient(t)

	t.Run("SessionIdImmediatelyAvailable", func(t *testing.T) {
		// Test that sessionId is returned immediately in DownloadFirmware response
		// This catches the sessionId bug we fixed

		// Create mock HTTP server with delay to ensure download doesn't complete instantly
		testContent := "session id test content"
		httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Add small delay to ensure download is in progress
			time.Sleep(100 * time.Millisecond)
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(testContent)))
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(testContent))
		}))
		defer httpServer.Close()

		ctx := context.Background()

		// Start download
		downloadReq := &pb.DownloadFirmwareRequest{
			Url:                 httpServer.URL + "/session-test.bin",
			TotalTimeoutSeconds: 10,
		}

		downloadResp, err := client.DownloadFirmware(ctx, downloadReq)
		require.NoError(t, err)
		require.NotNil(t, downloadResp)

		// CRITICAL: sessionId must be present immediately in the response
		assert.NotEmpty(t, downloadResp.SessionId, "sessionId must be returned immediately")
		assert.NotEqual(t, "", downloadResp.SessionId, "sessionId cannot be empty string")

		// Status should be starting (not completed yet due to delay)
		assert.Equal(t, "starting", downloadResp.Status)

		// Verify we can immediately query status using the session ID
		statusReq := &pb.GetDownloadStatusRequest{
			SessionId: downloadResp.SessionId,
		}

		statusResp, err := client.GetDownloadStatus(ctx, statusReq)
		require.NoError(t, err, "Should be able to query status immediately with sessionId")
		require.NotNil(t, statusResp)
		assert.Equal(t, downloadResp.SessionId, statusResp.SessionId)
	})
}

// TestDownloadFirmware_ContextFix tests large file downloads without context cancellation.
func TestDownloadFirmware_ContextFix(t *testing.T) {
	client := setupDownloadTestClient(t)

	t.Run("LargeFileDownloadWithTimeout", func(t *testing.T) {
		// Test downloading larger files to catch context cancellation issues
		// This simulates the large file scenario that exposed the context bug

		// Create 1MB of test data
		largeContent := make([]byte, 1024*1024) // 1MB
		for i := range largeContent {
			largeContent[i] = byte(i % 256)
		}

		httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(largeContent)))
			w.WriteHeader(http.StatusOK)

			// Write in chunks to simulate realistic download
			chunkSize := 32 * 1024 // 32KB chunks
			for i := 0; i < len(largeContent); i += chunkSize {
				end := i + chunkSize
				if end > len(largeContent) {
					end = len(largeContent)
				}
				w.Write(largeContent[i:end])
				w.(http.Flusher).Flush()
				// Small delay between chunks to make download take some time
				time.Sleep(10 * time.Millisecond)
			}
		}))
		defer httpServer.Close()

		ctx := context.Background()

		// Start download with reasonable timeout
		downloadReq := &pb.DownloadFirmwareRequest{
			Url:                   httpServer.URL + "/large-file.bin",
			ConnectTimeoutSeconds: 5,
			TotalTimeoutSeconds:   30, // Give enough time for 1MB download
		}

		downloadResp, err := client.DownloadFirmware(ctx, downloadReq)
		require.NoError(t, err)
		require.NotNil(t, downloadResp)

		// SessionId should be available immediately
		assert.NotEmpty(t, downloadResp.SessionId)

		// Monitor progress and wait for completion
		sessionId := downloadResp.SessionId
		statusReq := &pb.GetDownloadStatusRequest{
			SessionId: sessionId,
		}

		var finalStatus *pb.GetDownloadStatusResponse
		maxWaitTime := 60 * time.Second // Generous timeout
		pollInterval := 100 * time.Millisecond
		maxPolls := int(maxWaitTime / pollInterval)

		for i := 0; i < maxPolls; i++ {
			time.Sleep(pollInterval)

			statusResp, err := client.GetDownloadStatus(ctx, statusReq)
			require.NoError(t, err)
			require.NotNil(t, statusResp)

			// Check if download completed successfully
			if statusResp.GetResult() != nil {
				finalStatus = statusResp
				break
			}

			// Check for errors (this would catch context cancellation)
			if statusResp.GetError() != nil {
				t.Fatalf("Download failed with error: %+v", statusResp.GetError())
			}

			// Log progress if available
			if progress := statusResp.GetProgress(); progress != nil {
				t.Logf("Large file progress: %d/%d bytes (%.1f%%) @ %.1f KB/s",
					progress.DownloadedBytes, progress.TotalBytes,
					progress.Percentage, progress.SpeedBytesPerSec/1024)
			}
		}

		// Verify download completed successfully (not canceled due to context issues)
		require.NotNil(t, finalStatus, "Large file download should complete without context cancellation")
		result := finalStatus.GetResult()
		require.NotNil(t, result, "Expected successful result for large file download")

		// Verify file size matches
		assert.Equal(t, int64(len(largeContent)), result.FileSizeBytes)
		assert.Contains(t, result.FilePath, "large-file.bin")

		// Verify actual file content
		downloadedContent, err := os.ReadFile(result.FilePath)
		require.NoError(t, err)
		assert.Equal(t, largeContent, downloadedContent, "Downloaded content should match original")
	})
}

// TestDownloadFirmware_ProgressTracking tests real-time progress tracking.
func TestDownloadFirmware_ProgressTracking(t *testing.T) {
	client := setupDownloadTestClient(t)

	t.Run("ProgressTrackingDuringDownload", func(t *testing.T) {
		// Test real-time progress tracking to ensure session synchronization works

		// Create content that takes time to download
		mediumContent := make([]byte, 512*1024) // 512KB
		for i := range mediumContent {
			mediumContent[i] = byte(i % 256)
		}

		httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(mediumContent)))
			w.WriteHeader(http.StatusOK)

			// Write slowly in small chunks to allow progress tracking
			chunkSize := 16 * 1024 // 16KB chunks
			for i := 0; i < len(mediumContent); i += chunkSize {
				end := i + chunkSize
				if end > len(mediumContent) {
					end = len(mediumContent)
				}
				w.Write(mediumContent[i:end])
				w.(http.Flusher).Flush()
				time.Sleep(50 * time.Millisecond) // Slow down to capture progress
			}
		}))
		defer httpServer.Close()

		ctx := context.Background()

		// Start download
		downloadReq := &pb.DownloadFirmwareRequest{
			Url:                 httpServer.URL + "/progress-test.bin",
			TotalTimeoutSeconds: 30,
		}

		downloadResp, err := client.DownloadFirmware(ctx, downloadReq)
		require.NoError(t, err)
		require.NotNil(t, downloadResp)
		assert.NotEmpty(t, downloadResp.SessionId)

		// Track progress updates
		sessionId := downloadResp.SessionId
		statusReq := &pb.GetDownloadStatusRequest{
			SessionId: sessionId,
		}

		var progressUpdates []*pb.DownloadProgress
		var finalStatus *pb.GetDownloadStatusResponse

		// Poll frequently to catch progress updates
		for i := 0; i < 200; i++ { // Poll for up to 20 seconds
			time.Sleep(100 * time.Millisecond)

			statusResp, err := client.GetDownloadStatus(ctx, statusReq)
			require.NoError(t, err)
			require.NotNil(t, statusResp)

			// Collect progress updates
			if progress := statusResp.GetProgress(); progress != nil {
				progressUpdates = append(progressUpdates, progress)
				t.Logf("Progress update %d: %d/%d bytes (%.1f%%)",
					len(progressUpdates), progress.DownloadedBytes,
					progress.TotalBytes, progress.Percentage)
			}

			// Check if completed
			if statusResp.GetResult() != nil {
				finalStatus = statusResp
				break
			}

			// Check for errors
			if statusResp.GetError() != nil {
				t.Fatalf("Download failed: %+v", statusResp.GetError())
			}
		}

		// Verify we got progress updates (may be 0 if download was very fast)
		t.Logf("Captured %d progress updates", len(progressUpdates))

		// Verify progress is increasing
		if len(progressUpdates) > 1 {
			firstProgress := progressUpdates[0]
			lastProgress := progressUpdates[len(progressUpdates)-1]
			assert.LessOrEqual(t, firstProgress.DownloadedBytes, lastProgress.DownloadedBytes,
				"Downloaded bytes should increase over time")
		}

		// Verify final completion
		require.NotNil(t, finalStatus, "Download should complete")
		result := finalStatus.GetResult()
		require.NotNil(t, result, "Should have successful result")
		assert.Equal(t, int64(len(mediumContent)), result.FileSizeBytes)
	})
}

// TestDownloadFirmware_ConcurrentBlocking tests concurrent download blocking.
func TestDownloadFirmware_ConcurrentBlocking(t *testing.T) {
	client := setupDownloadTestClient(t)

	t.Run("ConcurrentDownloadBlocking", func(t *testing.T) {
		// Test that concurrent downloads are properly blocked
		// This ensures our global download state management works

		testContent := "concurrent test content"
		httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Add delay to keep first download active
			time.Sleep(200 * time.Millisecond)
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(testContent)))
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(testContent))
		}))
		defer httpServer.Close()

		ctx := context.Background()

		// Start first download
		downloadReq1 := &pb.DownloadFirmwareRequest{
			Url: httpServer.URL + "/concurrent1.bin",
		}

		downloadResp1, err := client.DownloadFirmware(ctx, downloadReq1)
		require.NoError(t, err)
		require.NotNil(t, downloadResp1)
		assert.NotEmpty(t, downloadResp1.SessionId)

		// Immediately try second download - should be blocked
		downloadReq2 := &pb.DownloadFirmwareRequest{
			Url: httpServer.URL + "/concurrent2.bin",
		}

		_, err = client.DownloadFirmware(ctx, downloadReq2)
		// Should get error about download in progress
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "download already in progress")

		// Wait for first download to complete
		time.Sleep(500 * time.Millisecond)

		// Now second download should work
		downloadResp3, err := client.DownloadFirmware(ctx, downloadReq2)
		require.NoError(t, err)
		require.NotNil(t, downloadResp3)
		assert.NotEmpty(t, downloadResp3.SessionId)
		assert.NotEqual(t, downloadResp1.SessionId, downloadResp3.SessionId)
	})
}

// calculateMD5 calculates the MD5 checksum of the given content.
func calculateMD5(content []byte) string {
	hash := md5.Sum(content) // nosemgrep: go.lang.security.audit.crypto.use_of_weak_crypto.use-of-md5
	return hex.EncodeToString(hash[:])
}

// waitForDownloadServiceReady waits for any existing download to complete before starting a new test.
func waitForDownloadServiceReady(t *testing.T, client pb.FirmwareManagementClient) {
	t.Helper()
	// Simple approach: just wait a bit between tests to avoid concurrency issues
	// since the server only allows one download at a time
	time.Sleep(500 * time.Millisecond)
}

// TestDownloadFirmware_MD5ValidationSuccess tests successful MD5 validation.
func TestDownloadFirmware_MD5ValidationSuccess(t *testing.T) {
	client := setupDownloadTestClient(t)
	waitForDownloadServiceReady(t, client)
	// Test successful download with correct MD5 checksum
	testContent := "Hello, World! This is firmware content for MD5 testing."
	expectedMD5 := calculateMD5([]byte(testContent))

	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(testContent)))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(testContent))
	}))
	defer httpServer.Close()

	ctx := context.Background()

	// Start download with MD5 checksum
	downloadReq := &pb.DownloadFirmwareRequest{
		Url:                   httpServer.URL + "/firmware-with-md5.bin",
		ConnectTimeoutSeconds: 10,
		TotalTimeoutSeconds:   30,
		ExpectedMd5:           expectedMD5,
	}

	downloadResp, err := client.DownloadFirmware(ctx, downloadReq)
	require.NoError(t, err)
	require.NotNil(t, downloadResp)
	assert.NotEmpty(t, downloadResp.SessionId)

	// Poll for completion
	statusReq := &pb.GetDownloadStatusRequest{
		SessionId: downloadResp.SessionId,
	}

	var finalResult *pb.DownloadResult
	timeout := time.Now().Add(10 * time.Second)
	for time.Now().Before(timeout) {
		statusResp, err := client.GetDownloadStatus(ctx, statusReq)
		require.NoError(t, err)
		require.NotNil(t, statusResp)

		if result := statusResp.GetResult(); result != nil {
			finalResult = result
			break
		}

		if errorResp := statusResp.GetError(); errorResp != nil {
			t.Fatalf("Download failed: %s", errorResp.Message)
		}

		time.Sleep(100 * time.Millisecond)
	}

	require.NotNil(t, finalResult, "Download should have completed successfully")

	// Verify checksum validation in result
	checksumValidation := finalResult.ChecksumValidation
	require.NotNil(t, checksumValidation)
	assert.True(t, checksumValidation.ValidationRequested)
	assert.True(t, checksumValidation.ValidationPassed)
	assert.Equal(t, expectedMD5, checksumValidation.ExpectedChecksum)
	assert.Equal(t, expectedMD5, checksumValidation.ActualChecksum)
	assert.Equal(t, "md5", checksumValidation.Algorithm)

	// Verify file was created and has correct content
	downloadedContent, err := os.ReadFile(finalResult.FilePath)
	require.NoError(t, err)
	assert.Equal(t, testContent, string(downloadedContent))
}

// TestDownloadFirmware_MD5ValidationFailure tests failed MD5 validation.
func TestDownloadFirmware_MD5ValidationFailure(t *testing.T) {
	client := setupDownloadTestClient(t)
	waitForDownloadServiceReady(t, client)
	// Test download with incorrect MD5 checksum
	testContent := "This is firmware content with wrong checksum."
	wrongMD5 := "deadbeefcafebabe12345678901234567890" // Intentionally wrong

	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(testContent)))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(testContent))
	}))
	defer httpServer.Close()

	ctx := context.Background()

	// Start download with wrong MD5 checksum
	downloadReq := &pb.DownloadFirmwareRequest{
		Url:                   httpServer.URL + "/firmware-wrong-md5.bin",
		ConnectTimeoutSeconds: 10,
		TotalTimeoutSeconds:   30,
		ExpectedMd5:           wrongMD5,
	}

	downloadResp, err := client.DownloadFirmware(ctx, downloadReq)
	require.NoError(t, err)
	require.NotNil(t, downloadResp)
	assert.NotEmpty(t, downloadResp.SessionId)

	// Poll for failure
	statusReq := &pb.GetDownloadStatusRequest{
		SessionId: downloadResp.SessionId,
	}

	var finalError *pb.DownloadError
	timeout := time.Now().Add(10 * time.Second)
	for time.Now().Before(timeout) {
		statusResp, err := client.GetDownloadStatus(ctx, statusReq)
		require.NoError(t, err)
		require.NotNil(t, statusResp)

		if errorResp := statusResp.GetError(); errorResp != nil {
			finalError = errorResp
			break
		}

		if result := statusResp.GetResult(); result != nil {
			t.Fatalf("Download should have failed due to checksum mismatch")
		}

		time.Sleep(100 * time.Millisecond)
	}

	require.NotNil(t, finalError, "Download should have failed due to checksum mismatch")

	// Verify error details
	assert.Equal(t, "validation", finalError.Category)
	assert.Contains(t, finalError.Message, "checksum mismatch")
	assert.Contains(t, finalError.Message, wrongMD5)
	assert.NotEmpty(t, finalError.Attempts)
}

// TestDownloadFirmware_MD5ValidationNone tests download without MD5 validation.
func TestDownloadFirmware_MD5ValidationNone(t *testing.T) {
	client := setupDownloadTestClient(t)
	waitForDownloadServiceReady(t, client)
	// Test download without MD5 checksum (should not validate)
	testContent := "This firmware will not be validated."

	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(testContent)))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(testContent))
	}))
	defer httpServer.Close()

	ctx := context.Background()

	// Start download without MD5 checksum
	downloadReq := &pb.DownloadFirmwareRequest{
		Url:                   httpServer.URL + "/firmware-no-validation.bin",
		ConnectTimeoutSeconds: 10,
		TotalTimeoutSeconds:   30,
		// No ExpectedMd5 field
	}

	downloadResp, err := client.DownloadFirmware(ctx, downloadReq)
	require.NoError(t, err)
	require.NotNil(t, downloadResp)
	assert.NotEmpty(t, downloadResp.SessionId)

	// Poll for completion
	statusReq := &pb.GetDownloadStatusRequest{
		SessionId: downloadResp.SessionId,
	}

	var finalResult *pb.DownloadResult
	timeout := time.Now().Add(10 * time.Second)
	for time.Now().Before(timeout) {
		statusResp, err := client.GetDownloadStatus(ctx, statusReq)
		require.NoError(t, err)
		require.NotNil(t, statusResp)

		if result := statusResp.GetResult(); result != nil {
			finalResult = result
			break
		}

		if errorResp := statusResp.GetError(); errorResp != nil {
			t.Fatalf("Download failed: %s", errorResp.Message)
		}

		time.Sleep(100 * time.Millisecond)
	}

	require.NotNil(t, finalResult, "Download should have completed successfully")

	// Verify no checksum validation was performed
	checksumValidation := finalResult.ChecksumValidation
	require.NotNil(t, checksumValidation)
	assert.False(t, checksumValidation.ValidationRequested)
	assert.False(t, checksumValidation.ValidationPassed) // Not meaningful when not requested
	assert.Empty(t, checksumValidation.ExpectedChecksum)
	assert.Empty(t, checksumValidation.ActualChecksum)
	assert.Equal(t, "md5", checksumValidation.Algorithm) // Algorithm is always set
}

// TestDownloadFirmware_MD5ValidationCaseInsensitive tests case-insensitive MD5 validation.
func TestDownloadFirmware_MD5ValidationCaseInsensitive(t *testing.T) {
	client := setupDownloadTestClient(t)
	waitForDownloadServiceReady(t, client)
	// Test that MD5 validation is case-insensitive
	testContent := "Case insensitive MD5 test content."
	correctMD5 := calculateMD5([]byte(testContent))
	uppercaseMD5 := strings.ToUpper(correctMD5)

	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(testContent)))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(testContent))
	}))
	defer httpServer.Close()

	ctx := context.Background()

	// Start download with uppercase MD5 checksum
	downloadReq := &pb.DownloadFirmwareRequest{
		Url:                   httpServer.URL + "/firmware-uppercase-md5.bin",
		ConnectTimeoutSeconds: 10,
		TotalTimeoutSeconds:   30,
		ExpectedMd5:           uppercaseMD5,
	}

	downloadResp, err := client.DownloadFirmware(ctx, downloadReq)
	require.NoError(t, err)
	require.NotNil(t, downloadResp)

	// Poll for completion
	statusReq := &pb.GetDownloadStatusRequest{
		SessionId: downloadResp.SessionId,
	}

	var finalResult *pb.DownloadResult
	timeout := time.Now().Add(10 * time.Second)
	for time.Now().Before(timeout) {
		statusResp, err := client.GetDownloadStatus(ctx, statusReq)
		require.NoError(t, err)

		if result := statusResp.GetResult(); result != nil {
			finalResult = result
			break
		}

		if errorResp := statusResp.GetError(); errorResp != nil {
			t.Fatalf("Download failed: %s", errorResp.Message)
		}

		time.Sleep(100 * time.Millisecond)
	}

	require.NotNil(t, finalResult, "Download should have completed successfully with case-insensitive MD5")

	// Verify checksum validation passed despite case difference
	checksumValidation := finalResult.ChecksumValidation
	require.NotNil(t, checksumValidation)
	assert.True(t, checksumValidation.ValidationRequested)
	assert.True(t, checksumValidation.ValidationPassed)
	assert.Equal(t, uppercaseMD5, checksumValidation.ExpectedChecksum)
	assert.Equal(t, correctMD5, checksumValidation.ActualChecksum) // Server calculates in lowercase
}
