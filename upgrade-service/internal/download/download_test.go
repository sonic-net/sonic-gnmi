package download

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDownloadFirmware_Success(t *testing.T) {
	// Create a test HTTP server
	testContent := "test firmware content"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(testContent))
	}))
	defer server.Close()

	// Create temporary directory for output
	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "firmware.bin")

	// Perform download
	ctx := context.Background()
	session, result, err := DownloadFirmware(ctx, server.URL, outputPath)

	// Verify success
	require.NoError(t, err)
	require.NotNil(t, session)
	require.NotNil(t, result)

	assert.Equal(t, outputPath, result.FilePath)
	assert.Equal(t, int64(len(testContent)), result.FileSize)
	assert.Equal(t, server.URL, result.URL)
	assert.Greater(t, result.AttemptCount, 0)
	assert.NotEmpty(t, result.FinalMethod)
	assert.Greater(t, result.Duration, time.Duration(0))

	// Verify file content
	content, err := os.ReadFile(outputPath)
	require.NoError(t, err)
	assert.Equal(t, testContent, string(content))
}

func TestDownloadFirmware_AutoOutputPath(t *testing.T) {
	// Create a test HTTP server
	testContent := "test firmware content"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(testContent))
	}))
	defer server.Close()

	// Create temporary directory and change to it
	tempDir := t.TempDir()
	oldWd, err := os.Getwd()
	require.NoError(t, err)
	defer os.Chdir(oldWd)

	err = os.Chdir(tempDir)
	require.NoError(t, err)

	// Add a filename to the server URL for testing
	downloadURL := server.URL + "/test-firmware.bin"

	// Perform download without output path
	ctx := context.Background()
	session, result, err := DownloadFirmware(ctx, downloadURL, "")

	// Verify success
	require.NoError(t, err)
	require.NotNil(t, session)
	require.NotNil(t, result)

	expectedPath := "test-firmware.bin"
	assert.Equal(t, expectedPath, result.FilePath)
	assert.Equal(t, int64(len(testContent)), result.FileSize)

	// Verify file exists and has correct content
	content, err := os.ReadFile(expectedPath)
	require.NoError(t, err)
	assert.Equal(t, testContent, string(content))
}

func TestDownloadFirmware_HTTPError(t *testing.T) {
	// Create a test HTTP server that returns 404
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("Not Found"))
	}))
	defer server.Close()

	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "firmware.bin")

	ctx := context.Background()
	session, result, err := DownloadFirmware(ctx, server.URL, outputPath)

	// Verify error
	assert.NotNil(t, session) // Session should still exist even on error
	assert.Nil(t, result)
	require.Error(t, err)

	downloadErr, ok := err.(*DownloadError)
	require.True(t, ok, "Expected DownloadError")
	assert.Equal(t, ErrorCategoryHTTP, downloadErr.Category)
	assert.Equal(t, 404, downloadErr.Code)
	assert.True(t, downloadErr.IsHTTPError())
	assert.False(t, downloadErr.IsNetworkError())
}

func TestDownloadFirmware_NetworkError(t *testing.T) {
	// Use an invalid URL to trigger network error
	invalidURL := "http://192.0.2.1:12345/firmware.bin" // RFC3330 test address

	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "firmware.bin")

	// Use a short timeout to make the test faster
	config := DefaultDownloadConfig()
	config.ConnectTimeout = 100 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	session, result, err := DownloadFirmwareWithConfig(ctx, invalidURL, outputPath, config)

	// Verify error
	assert.NotNil(t, session) // Session should still exist even on error
	assert.Nil(t, result)
	require.Error(t, err)

	downloadErr, ok := err.(*DownloadError)
	require.True(t, ok, "Expected DownloadError, got: %T", err)
	// Should be classified as network error due to connection failure
	assert.True(t, downloadErr.IsNetworkError() || downloadErr.Category == ErrorCategoryOther,
		"Expected network or other error, got: %s", downloadErr.Category)
	assert.Greater(t, len(downloadErr.Attempts), 0)
}

func TestDownloadFirmware_FileSystemError(t *testing.T) {
	// Create a test HTTP server
	testContent := "test content"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(testContent))
	}))
	defer server.Close()

	// Try to write to a directory that doesn't exist and can't be created
	invalidPath := "/proc/invalid/firmware.bin" // /proc is read-only

	ctx := context.Background()
	session, result, err := DownloadFirmware(ctx, server.URL, invalidPath)

	// Verify error
	assert.NotNil(t, session) // Session should still exist even on error
	assert.Nil(t, result)
	require.Error(t, err)

	downloadErr, ok := err.(*DownloadError)
	require.True(t, ok, "Expected DownloadError")
	assert.Equal(t, ErrorCategoryFileSystem, downloadErr.Category)
	assert.True(t, downloadErr.IsFileSystemError())
}

func TestDownloadFirmware_ContextCancellation(t *testing.T) {
	// Create a slow server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second) // Slow response
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("content"))
	}))
	defer server.Close()

	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "firmware.bin")

	// Create context that cancels quickly
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	session, result, err := DownloadFirmware(ctx, server.URL, outputPath)

	// Verify cancellation
	assert.NotNil(t, session) // Session should still exist even on cancellation
	assert.Nil(t, result)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "context") || strings.Contains(err.Error(), "canceled"))
}

func TestDownloadError_Methods(t *testing.T) {
	attempts := []Attempt{
		{Method: "interface", Error: "connection failed", Duration: time.Second},
		{Method: "direct", Error: "timeout", Duration: 2 * time.Second},
	}

	// Test network error
	netErr := NewNetworkError("http://example.com", "connection failed", attempts)
	assert.True(t, netErr.IsNetworkError())
	assert.False(t, netErr.IsHTTPError())
	assert.False(t, netErr.IsFileSystemError())
	assert.Equal(t, ErrorCategoryNetwork, netErr.Category)
	assert.Equal(t, &attempts[1], netErr.LastAttempt())

	// Test HTTP error
	httpErr := NewHTTPError("http://example.com", "HTTP 404", 404, attempts)
	assert.False(t, httpErr.IsNetworkError())
	assert.True(t, httpErr.IsHTTPError())
	assert.False(t, httpErr.IsFileSystemError())
	assert.Equal(t, ErrorCategoryHTTP, httpErr.Category)
	assert.Equal(t, 404, httpErr.Code)

	// Test filesystem error
	fsErr := NewFileSystemError("http://example.com", "permission denied", attempts)
	assert.False(t, fsErr.IsNetworkError())
	assert.False(t, fsErr.IsHTTPError())
	assert.True(t, fsErr.IsFileSystemError())
	assert.Equal(t, ErrorCategoryFileSystem, fsErr.Category)

	// Test other error
	otherErr := NewOtherError("http://example.com", "unknown error", attempts)
	assert.False(t, otherErr.IsNetworkError())
	assert.False(t, otherErr.IsHTTPError())
	assert.False(t, otherErr.IsFileSystemError())
	assert.Equal(t, ErrorCategoryOther, otherErr.Category)
}

func TestDownloadError_ErrorMessage(t *testing.T) {
	// Test with no attempts
	err1 := &DownloadError{
		Message: "test error",
	}
	assert.Equal(t, "download failed: test error", err1.Error())

	// Test with attempts
	attempts := []Attempt{{Method: "test"}}
	err2 := &DownloadError{
		Message:  "test error",
		Attempts: attempts,
	}
	assert.Equal(t, "download failed after 1 attempts: test error", err2.Error())
}

func TestDownloadError_LastAttempt(t *testing.T) {
	// Test with no attempts
	err1 := &DownloadError{}
	assert.Nil(t, err1.LastAttempt())

	// Test with attempts
	attempts := []Attempt{
		{Method: "first"},
		{Method: "last"},
	}
	err2 := &DownloadError{Attempts: attempts}
	lastAttempt := err2.LastAttempt()
	require.NotNil(t, lastAttempt)
	assert.Equal(t, "last", lastAttempt.Method)
}

func TestDefaultDownloadConfig(t *testing.T) {
	config := DefaultDownloadConfig()
	assert.Equal(t, 30*time.Second, config.ConnectTimeout)
	assert.Equal(t, DefaultInterface, config.Interface)
	assert.Equal(t, 3, config.MaxRetries)
	assert.Equal(t, "sonic-ops-server/1.0", config.UserAgent)
}

func TestGetOutputPathFromURL(t *testing.T) {
	tests := []struct {
		name        string
		url         string
		expected    string
		expectError bool
	}{
		{
			name:     "simple filename",
			url:      "http://example.com/firmware.bin",
			expected: "firmware.bin",
		},
		{
			name:     "path with directories",
			url:      "http://example.com/path/to/firmware.bin",
			expected: "firmware.bin",
		},
		{
			name:     "filename with query params",
			url:      "http://example.com/firmware.bin?version=1.2.3",
			expected: "firmware.bin",
		},
		{
			name:        "no filename",
			url:         "http://example.com/",
			expectError: true,
		},
		{
			name:        "invalid URL",
			url:         "://invalid-url",
			expectError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := getOutputPathFromURL(test.url)
			if test.expectError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, test.expected, result)
			}
		})
	}
}

func TestClassifyError(t *testing.T) {
	attempts := []Attempt{{Method: "test"}}

	tests := []struct {
		name     string
		err      error
		expected ErrorCategory
	}{
		{
			name:     "HTTP error",
			err:      fmt.Errorf("HTTP error 404: Not Found"),
			expected: ErrorCategoryHTTP,
		},
		{
			name:     "filesystem error - create",
			err:      fmt.Errorf("failed to create output file"),
			expected: ErrorCategoryFileSystem,
		},
		{
			name:     "filesystem error - write",
			err:      fmt.Errorf("failed to write file"),
			expected: ErrorCategoryFileSystem,
		},
		{
			name:     "network error - connection",
			err:      fmt.Errorf("connection refused"),
			expected: ErrorCategoryNetwork,
		},
		{
			name:     "network error - timeout",
			err:      fmt.Errorf("timeout waiting for response"),
			expected: ErrorCategoryNetwork,
		},
		{
			name:     "network error - dial",
			err:      fmt.Errorf("dial tcp: connection failed"),
			expected: ErrorCategoryNetwork,
		},
		{
			name:     "other error",
			err:      fmt.Errorf("unknown error type"),
			expected: ErrorCategoryOther,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			downloadErr := classifyError("http://example.com", test.err, attempts)
			assert.Equal(t, test.expected, downloadErr.Category)
			assert.Equal(t, test.err.Error(), downloadErr.Message)
		})
	}
}

func TestCreateSuccessResult(t *testing.T) {
	startTime := time.Now().Add(-5 * time.Second)
	validation := ChecksumValidationResult{
		ValidationRequested: false,
		Algorithm:           "md5",
	}
	result := createSuccessResult(
		"http://example.com/firmware.bin",
		"/tmp/firmware.bin",
		startTime,
		3,
		"direct",
		1024,
		validation,
	)

	assert.Equal(t, "http://example.com/firmware.bin", result.URL)
	assert.Equal(t, "/tmp/firmware.bin", result.FilePath)
	assert.Equal(t, int64(1024), result.FileSize)
	assert.Equal(t, 3, result.AttemptCount)
	assert.Equal(t, "direct", result.FinalMethod)
	assert.Greater(t, result.Duration, 4*time.Second)
	assert.Less(t, result.Duration, 6*time.Second)
	assert.False(t, result.ChecksumValidation.ValidationRequested)
	assert.Equal(t, "md5", result.ChecksumValidation.Algorithm)
}

func TestValidateChecksum(t *testing.T) {
	tempDir := t.TempDir()

	tests := []struct {
		name          string
		fileContent   string
		expectedMD5   string
		shouldPass    bool
		shouldRequest bool
	}{
		{
			name:          "no validation requested",
			fileContent:   "Hello, World!",
			expectedMD5:   "",
			shouldPass:    false, // Not meaningful when validation not requested
			shouldRequest: false,
		},
		{
			name:          "valid MD5 checksum",
			fileContent:   "Hello, World!",
			expectedMD5:   "65a8e27d8879283831b664bd8b7f0ad4",
			shouldPass:    true,
			shouldRequest: true,
		},
		{
			name:          "invalid MD5 checksum",
			fileContent:   "Hello, World!",
			expectedMD5:   "invalid_checksum",
			shouldPass:    false,
			shouldRequest: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test file
			testFile := filepath.Join(tempDir, tt.name+".txt")
			err := os.WriteFile(testFile, []byte(tt.fileContent), 0644)
			require.NoError(t, err)

			// Validate checksum
			validation, err := validateChecksum(testFile, tt.expectedMD5)

			// Check validation result structure
			assert.Equal(t, tt.shouldRequest, validation.ValidationRequested)
			assert.Equal(t, "md5", validation.Algorithm)
			assert.Equal(t, tt.expectedMD5, validation.ExpectedChecksum)

			if tt.shouldRequest {
				assert.NotEmpty(t, validation.ActualChecksum)
				assert.Equal(t, tt.shouldPass, validation.ValidationPassed)

				if tt.shouldPass {
					assert.NoError(t, err)
				} else {
					assert.Error(t, err)
					assert.Contains(t, err.Error(), "checksum mismatch")
				}
			} else {
				assert.Empty(t, validation.ActualChecksum)
				assert.NoError(t, err)
			}
		})
	}
}

func TestShouldRetryWithFallback(t *testing.T) {
	// Network error should retry
	netErr := NewNetworkError("http://example.com", "connection failed", nil)
	assert.True(t, shouldRetryWithFallback(netErr))

	// HTTP error should not retry
	httpErr := NewHTTPError("http://example.com", "HTTP 404", 404, nil)
	assert.False(t, shouldRetryWithFallback(httpErr))

	// Filesystem error should not retry
	fsErr := NewFileSystemError("http://example.com", "permission denied", nil)
	assert.False(t, shouldRetryWithFallback(fsErr))

	// Other error should not retry
	otherErr := NewOtherError("http://example.com", "unknown error", nil)
	assert.False(t, shouldRetryWithFallback(otherErr))

	// Non-DownloadError should not retry
	genericErr := fmt.Errorf("generic error")
	assert.False(t, shouldRetryWithFallback(genericErr))
}

// TestDownloadFirmware_Integration tests the full download process with a real HTTP server.
func TestDownloadFirmware_Integration(t *testing.T) {
	// Create a test server with multiple endpoints
	mux := http.NewServeMux()

	// Successful download endpoint
	mux.HandleFunc("/success", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "13")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "test firmware")
	})

	// Slow endpoint for timeout testing
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "slow response")
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	t.Run("successful download", func(t *testing.T) {
		tempDir := t.TempDir()
		outputPath := filepath.Join(tempDir, "firmware.bin")

		ctx := context.Background()
		session, result, err := DownloadFirmware(ctx, server.URL+"/success", outputPath)

		require.NoError(t, err)
		require.NotNil(t, session)
		require.NotNil(t, result)
		assert.Equal(t, int64(13), result.FileSize)

		content, err := os.ReadFile(outputPath)
		require.NoError(t, err)
		assert.Equal(t, "test firmware", string(content))
	})

	t.Run("timeout handling", func(t *testing.T) {
		tempDir := t.TempDir()
		outputPath := filepath.Join(tempDir, "firmware.bin")

		config := DefaultDownloadConfig()
		config.ConnectTimeout = 50 * time.Millisecond

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		session, result, err := DownloadFirmwareWithConfig(ctx, server.URL+"/slow", outputPath, config)

		assert.NotNil(t, session) // Session should exist even on timeout
		assert.Nil(t, result)
		assert.Error(t, err)
	})
}
