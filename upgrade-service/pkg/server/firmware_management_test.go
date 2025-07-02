package server

import (
	"archive/zip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/config"
	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/download"
	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/firmware"
	pb "github.com/sonic-net/sonic-gnmi/upgrade-service/proto"
)

func TestNewFirmwareManagementServer(t *testing.T) {
	tempDir := t.TempDir()
	server := NewFirmwareManagementServer(tempDir)
	if server == nil {
		t.Error("Expected non-nil server")
	}
	if _, ok := server.(*firmwareManagementServer); !ok {
		t.Error("Expected server to be of type *firmwareManagementServer")
	}
}

func TestFirmwareManagementServer_CleanupOldFirmware(t *testing.T) {
	// Set up config to avoid nil pointer
	tempDir := t.TempDir()
	originalConfig := config.Global
	config.Global = &config.Config{RootFS: tempDir}
	defer func() { config.Global = originalConfig }()

	// Create host directory
	hostDir := filepath.Join(tempDir, "host")
	if err := os.MkdirAll(hostDir, 0755); err != nil {
		t.Fatalf("Failed to create host directory: %v", err)
	}

	server := NewFirmwareManagementServer(tempDir)
	ctx := context.Background()
	req := &pb.CleanupOldFirmwareRequest{}

	resp, err := server.CleanupOldFirmware(ctx, req)
	if err != nil {
		t.Errorf("CleanupOldFirmware returned unexpected error: %v", err)
	}

	if resp == nil {
		t.Error("Expected non-nil response")
	}

	// The actual cleanup is done by the firmware package which is already well tested
	// Here we just verify the server properly returns the response
	if resp.FilesDeleted < 0 {
		t.Error("Expected non-negative FilesDeleted")
	}
	if resp.SpaceFreedBytes < 0 {
		t.Error("Expected non-negative SpaceFreedBytes")
	}
	if resp.DeletedFiles == nil {
		t.Error("Expected non-nil DeletedFiles slice")
	}
	if resp.Errors == nil {
		t.Error("Expected non-nil Errors slice")
	}
}

func TestFirmwareManagementServer_ListFirmwareImages(t *testing.T) {
	t.Run("DefaultDirectories", testListFirmwareImagesDefault)
	t.Run("CustomDirectories", testListFirmwareImagesCustom)
	t.Run("VersionPatternFilter", testListFirmwareImagesWithPattern)
	t.Run("InvalidRegexPattern", testListFirmwareImagesInvalidPattern)
	t.Run("NonexistentDirectory", testListFirmwareImagesNonexistent)
}

func testListFirmwareImagesDefault(t *testing.T) {
	rootFS := setupListFirmwareTest(t)

	server := NewFirmwareManagementServer(rootFS)
	ctx := context.Background()
	req := &pb.ListFirmwareImagesRequest{}

	resp, err := server.ListFirmwareImages(ctx, req)
	if err != nil {
		t.Fatalf("ListFirmwareImages failed: %v", err)
	}

	if resp == nil {
		t.Fatal("Expected non-nil response")
	}

	if len(resp.Images) != 2 {
		t.Errorf("Expected 2 images, got %d", len(resp.Images))
	}

	// Verify response structure
	for _, img := range resp.Images {
		validateFirmwareImageInfo(t, img)
	}
}

func testListFirmwareImagesCustom(t *testing.T) {
	// Create a rootFS directory and custom test directories
	rootFS := t.TempDir()
	tempDir1 := t.TempDir()
	tempDir2 := t.TempDir()

	// Create test images in custom directories
	createTestImageInDir(t, tempDir1, "custom1.bin", "202311.10-custom1")
	createTestImageInDir(t, tempDir2, "custom2.swi", "202311.11-custom2")

	server := NewFirmwareManagementServer(rootFS)
	ctx := context.Background()
	req := &pb.ListFirmwareImagesRequest{
		SearchDirectories: []string{tempDir1, tempDir2},
	}

	resp, err := server.ListFirmwareImages(ctx, req)
	if err != nil {
		t.Fatalf("ListFirmwareImages failed: %v", err)
	}

	if len(resp.Images) != 2 {
		t.Errorf("Expected 2 images from custom directories, got %d", len(resp.Images))
	}

	// Verify we got images from custom directories
	foundVersions := make(map[string]bool)
	for _, img := range resp.Images {
		foundVersions[img.Version] = true
	}

	if !foundVersions["202311.10-custom1"] {
		t.Error("Missing image from first custom directory")
	}
	if !foundVersions["202311.11-custom2"] {
		t.Error("Missing image from second custom directory")
	}
}

func testListFirmwareImagesWithPattern(t *testing.T) {
	rootFS := setupListFirmwareTest(t)

	server := NewFirmwareManagementServer(rootFS)
	ctx := context.Background()

	// Test cases for regex patterns
	testCases := []struct {
		pattern       string
		expectedCount int
		description   string
	}{
		{"202311\\.4.*", 1, "Specific version pattern"},
		{".*-test.*", 2, "Contains test"},
		{"^202311\\.5.*", 0, "No matches"},
		{".*", 2, "Match all"},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			req := &pb.ListFirmwareImagesRequest{
				VersionPattern: tc.pattern,
			}

			resp, err := server.ListFirmwareImages(ctx, req)
			if err != nil {
				t.Fatalf("ListFirmwareImages failed: %v", err)
			}

			if len(resp.Images) != tc.expectedCount {
				t.Errorf("Pattern %s: expected %d images, got %d",
					tc.pattern, tc.expectedCount, len(resp.Images))
			}
		})
	}
}

func testListFirmwareImagesInvalidPattern(t *testing.T) {
	// Create a rootFS directory for this test
	rootFS := t.TempDir()

	server := NewFirmwareManagementServer(rootFS)
	ctx := context.Background()
	req := &pb.ListFirmwareImagesRequest{
		VersionPattern: "[invalid(regex", // Invalid regex
	}

	resp, err := server.ListFirmwareImages(ctx, req)
	if err == nil {
		t.Error("Expected error for invalid regex pattern")
	}
	if resp != nil {
		t.Error("Expected nil response for invalid regex")
	}
}

func testListFirmwareImagesNonexistent(t *testing.T) {
	// Create a rootFS directory for this test
	rootFS := t.TempDir()

	server := NewFirmwareManagementServer(rootFS)
	ctx := context.Background()
	req := &pb.ListFirmwareImagesRequest{
		SearchDirectories: []string{"/this/does/not/exist"},
	}

	resp, err := server.ListFirmwareImages(ctx, req)
	if err != nil {
		t.Fatalf("ListFirmwareImages should not fail for nonexistent directory: %v", err)
	}

	if len(resp.Images) != 0 {
		t.Errorf("Expected 0 images from nonexistent directory, got %d", len(resp.Images))
	}

	// Should have an error in the errors field
	if len(resp.Errors) == 0 {
		t.Error("Expected error message for nonexistent directory")
	}
}

// Helper functions for ListFirmwareImages tests

func setupListFirmwareTest(t *testing.T) string {
	// Save original values
	originalDirs := firmware.DefaultSearchDirectories

	// Set up cleanup
	t.Cleanup(func() {
		firmware.DefaultSearchDirectories = originalDirs
	})

	// Create a test root directory
	rootDir := t.TempDir()

	// Create subdirectory for firmware images
	firmwareDir := filepath.Join(rootDir, "tmp")
	if err := os.MkdirAll(firmwareDir, 0755); err != nil {
		t.Fatalf("Failed to create firmware directory: %v", err)
	}

	// Set firmware search directory to /tmp (which will be transformed to rootDir/tmp)
	firmware.DefaultSearchDirectories = []string{"/tmp"}

	// Create test images in the firmware directory
	createTestImageInDir(t, firmwareDir, "image1.bin", "202311.3-test123")
	createTestImageInDir(t, firmwareDir, "image2.swi", "202311.4-test456")

	// Return the root directory for the test to use
	return rootDir
}

func createTestImageInDir(t *testing.T, dir, filename, version string) {
	path := filepath.Join(dir, filename)
	var err error

	if filepath.Ext(filename) == ".swi" {
		err = createTestAbootImage(path, version)
	} else {
		err = createTestOnieImage(path, version)
	}

	if err != nil {
		t.Fatalf("Failed to create test image %s: %v", filename, err)
	}
}

func validateFirmwareImageInfo(t *testing.T, img *pb.FirmwareImageInfo) {
	if img.FilePath == "" {
		t.Error("FirmwareImageInfo missing file path")
	}
	if img.Version == "" {
		t.Error("FirmwareImageInfo missing version")
	}
	if img.FullVersion == "" {
		t.Error("FirmwareImageInfo missing full version")
	}
	if img.ImageType == "" {
		t.Error("FirmwareImageInfo missing image type")
	}
	if img.FileSizeBytes <= 0 {
		t.Error("FirmwareImageInfo has invalid file size")
	}
}

// Helper functions copied from version_test.go for creating test images.
func createTestOnieImage(path, version string) error {
	content := `#!/bin/bash
# SONiC ONIE installer
set -e

image_version="` + version + `"
build_date="2023-11-15T10:30:00"

echo "Installing SONiC $image_version"
# More installer content would follow...
exit_marker
BINARY_DATA_FOLLOWS_HERE...
`
	return os.WriteFile(path, []byte(content), 0644)
}

func createTestAbootImage(path, version string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	zipWriter := zip.NewWriter(file)
	defer zipWriter.Close()

	// Create .imagehash file with version
	imagehashWriter, err := zipWriter.Create(".imagehash")
	if err != nil {
		return err
	}
	if _, err := imagehashWriter.Write([]byte(version)); err != nil {
		return err
	}

	// Create a dummy boot file to make it look more realistic
	bootWriter, err := zipWriter.Create("boot0")
	if err != nil {
		return err
	}
	if _, err := bootWriter.Write([]byte("dummy boot content")); err != nil {
		return err
	}

	return nil
}

func TestFirmwareManagementServer_ConsolidateImages(t *testing.T) {
	// Set up config to avoid nil pointer
	tempDir := t.TempDir()
	originalConfig := config.Global
	config.Global = &config.Config{RootFS: tempDir}
	defer func() { config.Global = originalConfig }()

	// Create host directory for space estimation
	hostDir := filepath.Join(tempDir, "host")
	if err := os.MkdirAll(hostDir, 0755); err != nil {
		t.Fatalf("Failed to create host directory: %v", err)
	}

	server := NewFirmwareManagementServer(tempDir)
	ctx := context.Background()

	// Test dry run
	t.Run("DryRun", func(t *testing.T) {
		req := &pb.ConsolidateImagesRequest{
			DryRun: true,
		}

		// This will fail because sonic-installer is not available,
		// but we're testing the server layer integration
		resp, err := server.ConsolidateImages(ctx, req)
		if err == nil {
			// If somehow sonic-installer is available in test environment
			if resp == nil {
				t.Error("Expected non-nil response for successful consolidation")
			}
			if resp.Executed {
				t.Error("Expected dry run to have Executed=false")
			}
		} else {
			// Expected case: sonic-installer not available
			if resp != nil {
				t.Error("Expected nil response when consolidation fails")
			}
		}
	})

	// Test non-dry run (will likely fail due to no sonic-installer)
	t.Run("Execute", func(t *testing.T) {
		req := &pb.ConsolidateImagesRequest{
			DryRun: false,
		}

		// This will fail because sonic-installer is not available
		resp, err := server.ConsolidateImages(ctx, req)
		if err == nil {
			// If somehow sonic-installer is available in test environment
			if resp == nil {
				t.Error("Expected non-nil response for successful consolidation")
			}
			if !resp.Executed {
				t.Error("Expected non-dry run to have Executed=true")
			}
		} else {
			// Expected case: sonic-installer not available
			if resp != nil {
				t.Error("Expected nil response when consolidation fails")
			}
		}
	})
}

func TestFirmwareManagementServer_ListImages(t *testing.T) {
	// Set up config to avoid nil pointer
	tempDir := t.TempDir()
	originalConfig := config.Global
	config.Global = &config.Config{RootFS: tempDir}
	defer func() { config.Global = originalConfig }()

	server := NewFirmwareManagementServer(tempDir)
	ctx := context.Background()

	t.Run("ListImages", func(t *testing.T) {
		req := &pb.ListImagesRequest{}

		// This will fail because sonic-installer is not available in test environment,
		// but we're testing the server layer integration
		resp, err := server.ListImages(ctx, req)
		if err != nil {
			// Expected case: sonic-installer not available
			if resp != nil {
				t.Error("Expected nil response when list fails")
			}
			return
		}

		// If somehow sonic-installer is available in test environment
		if resp == nil {
			t.Error("Expected non-nil response for successful list")
		}
		if resp.Images == nil {
			t.Error("Expected non-nil images slice")
		}
		if resp.Warnings == nil {
			t.Error("Expected non-nil warnings slice")
		}
	})
}

func TestFirmwareManagementServer_DownloadFirmware(t *testing.T) {
	// Reset global download state before each test
	t.Cleanup(func() {
		downloadMutex.Lock()
		currentDownload = nil
		downloadMutex.Unlock()
	})

	t.Run("ValidDownload", func(t *testing.T) {
		// Create mock HTTP server
		testContent := "test firmware content"
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(testContent)))
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(testContent))
		}))
		defer server.Close()

		// Setup temp directory
		tempDir := t.TempDir()
		fwServer := NewFirmwareManagementServer(tempDir)
		ctx := context.Background()

		req := &pb.DownloadFirmwareRequest{
			Url: server.URL + "/firmware.bin",
		}

		resp, err := fwServer.DownloadFirmware(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)

		assert.NotEmpty(t, resp.SessionId)
		assert.True(t, resp.Status == "starting" || resp.Status == "completed")
		assert.Contains(t, resp.OutputPath, "firmware.bin")

		// Wait for download to complete
		time.Sleep(100 * time.Millisecond)

		// Verify download completed
		downloadMutex.RLock()
		assert.NotNil(t, currentDownload)
		assert.True(t, currentDownload.Done)
		assert.Nil(t, currentDownload.Error)
		assert.NotNil(t, currentDownload.Result)
		downloadMutex.RUnlock()
	})

	t.Run("InvalidURL", func(t *testing.T) {
		tempDir := t.TempDir()
		fwServer := NewFirmwareManagementServer(tempDir)
		ctx := context.Background()

		req := &pb.DownloadFirmwareRequest{
			Url: "", // Empty URL
		}

		resp, err := fwServer.DownloadFirmware(ctx, req)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "url is required")
	})

	t.Run("ConcurrentDownloadBlocked", func(t *testing.T) {
		// Create slow mock HTTP server
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(200 * time.Millisecond) // Slow download
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("slow content"))
		}))
		defer server.Close()

		tempDir := t.TempDir()
		fwServer := NewFirmwareManagementServer(tempDir)
		ctx := context.Background()

		// Start first download
		req1 := &pb.DownloadFirmwareRequest{
			Url: server.URL + "/slow.bin",
		}

		resp1, err1 := fwServer.DownloadFirmware(ctx, req1)
		require.NoError(t, err1)
		require.NotNil(t, resp1)

		// Try second download immediately - should fail
		req2 := &pb.DownloadFirmwareRequest{
			Url: server.URL + "/another.bin",
		}

		resp2, err2 := fwServer.DownloadFirmware(ctx, req2)
		assert.Error(t, err2)
		assert.Nil(t, resp2)
		assert.Contains(t, err2.Error(), "download already in progress")

		// Wait for first download to complete
		time.Sleep(300 * time.Millisecond)

		// Now second download should work
		resp3, err3 := fwServer.DownloadFirmware(ctx, req2)
		assert.NoError(t, err3)
		assert.NotNil(t, resp3)
	})

	t.Run("CustomOutputPath", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("test content"))
		}))
		defer server.Close()

		tempDir := t.TempDir()
		fwServer := NewFirmwareManagementServer(tempDir)
		ctx := context.Background()

		customPath := "/custom/path/firmware.bin"
		req := &pb.DownloadFirmwareRequest{
			Url:        server.URL + "/test.bin",
			OutputPath: customPath,
		}

		resp, err := fwServer.DownloadFirmware(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)

		assert.Contains(t, resp.OutputPath, customPath)
	})

	t.Run("WithTimeouts", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("test content"))
		}))
		defer server.Close()

		tempDir := t.TempDir()
		fwServer := NewFirmwareManagementServer(tempDir)
		ctx := context.Background()

		req := &pb.DownloadFirmwareRequest{
			Url:                   server.URL + "/test.bin",
			ConnectTimeoutSeconds: 10,
			TotalTimeoutSeconds:   30,
		}

		resp, err := fwServer.DownloadFirmware(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.NotEmpty(t, resp.SessionId)
	})
}

func TestFirmwareManagementServer_GetDownloadStatus(t *testing.T) {
	// Reset global download state before each test
	t.Cleanup(func() {
		downloadMutex.Lock()
		currentDownload = nil
		downloadMutex.Unlock()
	})

	t.Run("NoDownloadSession", func(t *testing.T) {
		tempDir := t.TempDir()
		fwServer := NewFirmwareManagementServer(tempDir)
		ctx := context.Background()

		req := &pb.GetDownloadStatusRequest{
			SessionId: "nonexistent",
		}

		resp, err := fwServer.GetDownloadStatus(ctx, req)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "no download session found")
	})

	t.Run("EmptySessionId", func(t *testing.T) {
		tempDir := t.TempDir()
		fwServer := NewFirmwareManagementServer(tempDir)
		ctx := context.Background()

		req := &pb.GetDownloadStatusRequest{
			SessionId: "",
		}

		resp, err := fwServer.GetDownloadStatus(ctx, req)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "session_id is required")
	})

	t.Run("CompletedDownload", func(t *testing.T) {
		// Create mock HTTP server
		testContent := "completed firmware content"
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(testContent)))
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(testContent))
		}))
		defer server.Close()

		tempDir := t.TempDir()
		fwServer := NewFirmwareManagementServer(tempDir)
		ctx := context.Background()

		// Start download
		downloadReq := &pb.DownloadFirmwareRequest{
			Url: server.URL + "/firmware.bin",
		}

		downloadResp, err := fwServer.DownloadFirmware(ctx, downloadReq)
		require.NoError(t, err)
		require.NotNil(t, downloadResp)

		// Wait for download to complete
		time.Sleep(200 * time.Millisecond)

		// Get status
		statusReq := &pb.GetDownloadStatusRequest{
			SessionId: downloadResp.SessionId,
		}

		statusResp, err := fwServer.GetDownloadStatus(ctx, statusReq)
		require.NoError(t, err)
		require.NotNil(t, statusResp)

		assert.Equal(t, downloadResp.SessionId, statusResp.SessionId)

		// Should have result state
		result := statusResp.GetResult()
		require.NotNil(t, result)
		assert.Contains(t, result.FilePath, "firmware.bin")
		assert.Equal(t, int64(len(testContent)), result.FileSizeBytes)
		assert.GreaterOrEqual(t, result.DurationMs, int64(0))
		assert.Greater(t, result.AttemptCount, int32(0))
	})

	t.Run("FailedDownload", func(t *testing.T) {
		// Create mock HTTP server that returns 404
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte("Not Found"))
		}))
		defer server.Close()

		tempDir := t.TempDir()
		fwServer := NewFirmwareManagementServer(tempDir)
		ctx := context.Background()

		// Start download
		downloadReq := &pb.DownloadFirmwareRequest{
			Url: server.URL + "/nonexistent.bin",
		}

		downloadResp, err := fwServer.DownloadFirmware(ctx, downloadReq)
		require.NoError(t, err)
		require.NotNil(t, downloadResp)

		// Wait for download to fail
		time.Sleep(200 * time.Millisecond)

		// Get status
		statusReq := &pb.GetDownloadStatusRequest{
			SessionId: downloadResp.SessionId,
		}

		statusResp, err := fwServer.GetDownloadStatus(ctx, statusReq)
		require.NoError(t, err)
		require.NotNil(t, statusResp)

		// Should have error state
		downloadError := statusResp.GetError()
		require.NotNil(t, downloadError)
		assert.Equal(t, "http", downloadError.Category)
		assert.Equal(t, int32(404), downloadError.HttpCode)
		assert.Contains(t, downloadError.Message, "HTTP error")
		assert.NotEmpty(t, downloadError.Attempts)
	})

	t.Run("SessionIdMismatch", func(t *testing.T) {
		// Create mock session
		downloadMutex.Lock()
		currentDownload = &downloadSessionInfo{
			Session: &download.DownloadSession{
				ID: "test-session-123",
			},
		}
		downloadMutex.Unlock()

		tempDir := t.TempDir()
		fwServer := NewFirmwareManagementServer(tempDir)
		ctx := context.Background()

		req := &pb.GetDownloadStatusRequest{
			SessionId: "wrong-session-id",
		}

		resp, err := fwServer.GetDownloadStatus(ctx, req)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "session_id mismatch")
	})
}

func TestExtractFilenameFromURL(t *testing.T) {
	testCases := []struct {
		url      string
		expected string
		hasError bool
	}{
		{
			url:      "http://example.com/firmware.bin",
			expected: "firmware.bin",
			hasError: false,
		},
		{
			url:      "http://10.201.148.43/pipelines/Networking-acs-buildimage-Official/vs/test/sonic-vs.bin",
			expected: "sonic-vs.bin",
			hasError: false,
		},
		{
			url:      "https://example.com/path/to/firmware.swi",
			expected: "firmware.swi",
			hasError: false,
		},
		{
			url:      "http://example.com/firmware.bin?version=1.2.3",
			expected: "firmware.bin",
			hasError: false,
		},
		{
			url:      "http://example.com/",
			expected: "",
			hasError: true,
		},
		{
			url:      "://invalid-url",
			expected: "",
			hasError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.url, func(t *testing.T) {
			result, err := extractFilenameFromURL(tc.url)
			if tc.hasError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expected, result)
			}
		})
	}
}
