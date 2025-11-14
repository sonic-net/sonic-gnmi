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
)

func TestDownloadHTTP_Success(t *testing.T) {
	// Create a test HTTP server
	testContent := []byte("test file content")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(testContent)
	}))
	defer server.Close()

	// Create temp directory for output
	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "downloaded.txt")

	// Download file
	ctx := context.Background()
	err := DownloadHTTP(ctx, server.URL, outputPath, 0)
	if err != nil {
		t.Fatalf("DownloadHTTP() error = %v", err)
	}

	// Verify file was created and content matches
	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("Failed to read downloaded file: %v", err)
	}

	if string(content) != string(testContent) {
		t.Errorf("Downloaded content = %q, want %q", content, testContent)
	}
}

func TestDownloadHTTP_HTTPError(t *testing.T) {
	// Create a test HTTP server that returns 404
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("Not Found"))
	}))
	defer server.Close()

	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "downloaded.txt")

	ctx := context.Background()
	err := DownloadHTTP(ctx, server.URL, outputPath, 0)
	if err == nil {
		t.Error("DownloadHTTP() expected error for 404 response, got nil")
	}

	// Verify file was not created
	if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
		t.Error("DownloadHTTP() should not create file on HTTP error")
	}
}

func TestDownloadHTTP_ContextCancellation(t *testing.T) {
	// Create a test HTTP server with slow response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate slow download
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("slow content"))
	}))
	defer server.Close()

	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "downloaded.txt")

	// Create context that cancels immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := DownloadHTTP(ctx, server.URL, outputPath, 0)
	if err == nil {
		t.Error("DownloadHTTP() expected error for cancelled context, got nil")
	}

	// Verify file was not created or was cleaned up
	if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
		t.Error("DownloadHTTP() should not create file on context cancellation")
	}
}

func TestDownloadHTTP_InvalidURL(t *testing.T) {
	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "downloaded.txt")

	ctx := context.Background()
	err := DownloadHTTP(ctx, "not-a-valid-url", outputPath, 0)
	if err == nil {
		t.Error("DownloadHTTP() expected error for invalid URL, got nil")
	}
}

func TestDownloadHTTP_CreateDirectory(t *testing.T) {
	// Test that DownloadHTTP creates nested directories
	testContent := []byte("test")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(testContent)
	}))
	defer server.Close()

	tempDir := t.TempDir()
	// Create path with nested directories that don't exist
	outputPath := filepath.Join(tempDir, "dir1", "dir2", "dir3", "file.txt")

	ctx := context.Background()
	err := DownloadHTTP(ctx, server.URL, outputPath, 0)
	if err != nil {
		t.Fatalf("DownloadHTTP() error = %v", err)
	}

	// Verify file was created
	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("Failed to read downloaded file: %v", err)
	}

	if string(content) != string(testContent) {
		t.Errorf("Downloaded content = %q, want %q", content, testContent)
	}
}

func TestDownloadHTTP_LargeFile(t *testing.T) {
	// Test downloading a larger file (1MB)
	largeContent := make([]byte, 1024*1024) // 1MB
	for i := range largeContent {
		largeContent[i] = byte(i % 256)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(largeContent)
	}))
	defer server.Close()

	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "large.bin")

	ctx := context.Background()
	err := DownloadHTTP(ctx, server.URL, outputPath, 0)
	if err != nil {
		t.Fatalf("DownloadHTTP() error = %v", err)
	}

	// Verify file size
	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatalf("Failed to stat downloaded file: %v", err)
	}

	if info.Size() != int64(len(largeContent)) {
		t.Errorf("Downloaded file size = %d, want %d", info.Size(), len(largeContent))
	}
}

func TestDownloadHTTP_Overwrite(t *testing.T) {
	// Test that DownloadHTTP overwrites existing files
	testContent := []byte("new content")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(testContent)
	}))
	defer server.Close()

	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "file.txt")

	// Create existing file with different content
	oldContent := []byte("old content")
	err := os.WriteFile(outputPath, oldContent, 0644)
	if err != nil {
		t.Fatalf("Failed to create existing file: %v", err)
	}

	// Download should overwrite
	ctx := context.Background()
	err = DownloadHTTP(ctx, server.URL, outputPath, 0)
	if err != nil {
		t.Fatalf("DownloadHTTP() error = %v", err)
	}

	// Verify content was replaced
	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("Failed to read downloaded file: %v", err)
	}

	if string(content) != string(testContent) {
		t.Errorf("Downloaded content = %q, want %q (old was %q)", content, testContent, oldContent)
	}
}

func TestDownloadHTTP_ServerError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
	}{
		{"400 Bad Request", http.StatusBadRequest},
		{"401 Unauthorized", http.StatusUnauthorized},
		{"403 Forbidden", http.StatusForbidden},
		{"404 Not Found", http.StatusNotFound},
		{"500 Internal Server Error", http.StatusInternalServerError},
		{"503 Service Unavailable", http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				fmt.Fprintf(w, "Error %d", tt.statusCode)
			}))
			defer server.Close()

			tempDir := t.TempDir()
			outputPath := filepath.Join(tempDir, "file.txt")

			ctx := context.Background()
			err := DownloadHTTP(ctx, server.URL, outputPath, 0)
			if err == nil {
				t.Errorf("DownloadHTTP() expected error for status %d, got nil", tt.statusCode)
			}
		})
	}
}

func TestDownloadHTTP_SizeExceedsLimit(t *testing.T) {
	// Create a test HTTP server with 100 bytes of content
	testContent := make([]byte, 100)
	for i := range testContent {
		testContent[i] = 'A'
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(testContent)))
		w.WriteHeader(http.StatusOK)
		w.Write(testContent)
	}))
	defer server.Close()

	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "downloaded.txt")

	// Set limit to 50 bytes (less than actual size)
	ctx := context.Background()
	err := DownloadHTTP(ctx, server.URL, outputPath, 50)
	if err == nil {
		t.Error("DownloadHTTP() expected error for size limit exceeded, got nil")
	}

	// Verify file was not created or was cleaned up
	if _, statErr := os.Stat(outputPath); !os.IsNotExist(statErr) {
		t.Error("DownloadHTTP() should clean up file when size limit exceeded")
	}
}

func TestDownloadHTTP_WithinLimit(t *testing.T) {
	// Create a test HTTP server with 50 bytes of content
	testContent := make([]byte, 50)
	for i := range testContent {
		testContent[i] = 'B'
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(testContent)))
		w.WriteHeader(http.StatusOK)
		w.Write(testContent)
	}))
	defer server.Close()

	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "downloaded.txt")

	// Set limit to 100 bytes (more than actual size)
	ctx := context.Background()
	err := DownloadHTTP(ctx, server.URL, outputPath, 100)
	if err != nil {
		t.Fatalf("DownloadHTTP() error = %v", err)
	}

	// Verify file was created and content matches
	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("Failed to read downloaded file: %v", err)
	}

	if len(content) != len(testContent) {
		t.Errorf("Downloaded size = %d, want %d", len(content), len(testContent))
	}
}

func TestDownloadHTTP_NoLimit(t *testing.T) {
	// Create a test HTTP server with 200 bytes of content
	testContent := make([]byte, 200)
	for i := range testContent {
		testContent[i] = 'C'
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(testContent)
	}))
	defer server.Close()

	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "downloaded.txt")

	// Set limit to 0 (no limit)
	ctx := context.Background()
	err := DownloadHTTP(ctx, server.URL, outputPath, 0)
	if err != nil {
		t.Fatalf("DownloadHTTP() error = %v", err)
	}

	// Verify file was created
	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("Failed to read downloaded file: %v", err)
	}

	if len(content) != len(testContent) {
		t.Errorf("Downloaded size = %d, want %d", len(content), len(testContent))
	}
}

func TestDownloadHTTP_Timeout(t *testing.T) {
	// Create a test HTTP server with very slow response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		// Write slowly to trigger timeout
		for i := 0; i < 100; i++ {
			w.Write([]byte("slow"))
			time.Sleep(100 * time.Millisecond) // Total 10 seconds
		}
	}))
	defer server.Close()

	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "downloaded.txt")

	// Create context with 1 second timeout
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	err := DownloadHTTP(ctx, server.URL, outputPath, 0)
	if err == nil {
		t.Error("DownloadHTTP() expected timeout error, got nil")
	}

	// Verify file was cleaned up
	if _, statErr := os.Stat(outputPath); !os.IsNotExist(statErr) {
		t.Error("DownloadHTTP() should clean up file on timeout")
	}
}

// Tests for streaming download functionality

func TestDownloadHTTPStreaming_Success(t *testing.T) {
	testContent := []byte("test streaming content")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(testContent)))
		w.WriteHeader(http.StatusOK)
		w.Write(testContent)
	}))
	defer server.Close()

	ctx := context.Background()
	reader, contentLength, err := DownloadHTTPStreaming(ctx, server.URL, 0)
	if err != nil {
		t.Fatalf("DownloadHTTPStreaming() error = %v", err)
	}
	defer reader.Close()

	if contentLength != int64(len(testContent)) {
		t.Errorf("Content length = %d, want %d", contentLength, len(testContent))
	}

	// Read all content from stream
	content, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("Failed to read stream: %v", err)
	}

	if string(content) != string(testContent) {
		t.Errorf("Streamed content = %q, want %q", content, testContent)
	}
}

func TestDownloadHTTPStreaming_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("Not Found"))
	}))
	defer server.Close()

	ctx := context.Background()
	reader, _, err := DownloadHTTPStreaming(ctx, server.URL, 0)
	if err == nil {
		reader.Close()
		t.Error("DownloadHTTPStreaming() expected error for 404 response, got nil")
	}
}

func TestDownloadHTTPStreaming_SizeLimit(t *testing.T) {
	testContent := make([]byte, 100)
	for i := range testContent {
		testContent[i] = 'A'
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(testContent)))
		w.WriteHeader(http.StatusOK)
		w.Write(testContent)
	}))
	defer server.Close()

	ctx := context.Background()
	reader, _, err := DownloadHTTPStreaming(ctx, server.URL, 50) // Limit to 50 bytes
	if err == nil {
		reader.Close()
		t.Error("DownloadHTTPStreaming() expected error for size limit exceeded")
	}

	if !strings.Contains(err.Error(), "exceeds maximum allowed size") {
		t.Errorf("Error should mention size limit, got: %v", err)
	}
}

func TestDownloadHTTPStreaming_SizeLimitDuringRead(t *testing.T) {
	// Test size limit enforcement during streaming (chunked transfer encoding)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Use chunked transfer encoding to avoid Content-Length header
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("Server does not support flushing")
			return
		}

		w.Header().Set("Transfer-Encoding", "chunked")
		w.WriteHeader(http.StatusOK)

		// Send data in chunks that will exceed the limit
		for i := 0; i < 10; i++ {
			w.Write([]byte("1234567890")) // 10 bytes per chunk = 100 bytes total
			flusher.Flush()
		}
	}))
	defer server.Close()

	ctx := context.Background()
	reader, contentLength, err := DownloadHTTPStreaming(ctx, server.URL, 50) // Limit to 50 bytes
	if err != nil {
		t.Fatalf("DownloadHTTPStreaming() setup error = %v", err)
	}
	defer reader.Close()

	// Content length should be -1 for chunked transfer
	if contentLength != -1 {
		t.Errorf("Expected content length -1 for chunked transfer, got %d", contentLength)
	}

	// Try to read more than the limit
	buffer := make([]byte, 100)
	totalRead := 0
	for totalRead < 100 {
		n, err := reader.Read(buffer[totalRead:])
		totalRead += n
		if err != nil {
			if strings.Contains(err.Error(), "exceeds maximum allowed size") {
				// This is expected when size limit is exceeded during read
				if totalRead > 50 {
					return // Test passed - limit was enforced
				}
			}
			if err == io.EOF {
				break
			}
			t.Fatalf("Unexpected read error: %v", err)
		}
	}

	// The limitedReadCloser should enforce the limit during read
	if totalRead > 51 { // Allow 1 byte over to detect the limit
		t.Errorf("Read %d bytes, but limit was 50 bytes", totalRead)
	}
}

func TestDownloadHTTPStreaming_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second) // Slow response
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("slow content"))
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	reader, _, err := DownloadHTTPStreaming(ctx, server.URL, 0)
	if err == nil {
		reader.Close()
		t.Error("DownloadHTTPStreaming() expected error for cancelled context, got nil")
	}
}

func TestDownloadHTTPStreaming_LargeFile(t *testing.T) {
	// Test streaming a large file (1MB)
	largeContent := make([]byte, 1024*1024)
	for i := range largeContent {
		largeContent[i] = byte(i % 256)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(largeContent)))
		w.WriteHeader(http.StatusOK)

		// Write in chunks to simulate real streaming
		chunkSize := 64 * 1024
		for i := 0; i < len(largeContent); i += chunkSize {
			end := i + chunkSize
			if end > len(largeContent) {
				end = len(largeContent)
			}
			w.Write(largeContent[i:end])
		}
	}))
	defer server.Close()

	ctx := context.Background()
	reader, contentLength, err := DownloadHTTPStreaming(ctx, server.URL, 0)
	if err != nil {
		t.Fatalf("DownloadHTTPStreaming() error = %v", err)
	}
	defer reader.Close()

	if contentLength != int64(len(largeContent)) {
		t.Errorf("Content length = %d, want %d", contentLength, len(largeContent))
	}

	// Read in chunks and verify content
	buffer := make([]byte, 32*1024) // 32KB chunks
	totalRead := 0
	for {
		n, err := reader.Read(buffer)
		if n > 0 {
			// Verify content matches expected pattern
			for i := 0; i < n; i++ {
				expected := byte((totalRead + i) % 256)
				if buffer[i] != expected {
					t.Errorf("Content mismatch at byte %d: got %d, want %d",
						totalRead+i, buffer[i], expected)
					return
				}
			}
			totalRead += n
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Read error: %v", err)
		}
	}

	if totalRead != len(largeContent) {
		t.Errorf("Total read = %d, want %d", totalRead, len(largeContent))
	}
}
