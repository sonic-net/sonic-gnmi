// Package download provides HTTP file download functionality.
// This package is pure Go with no CGO or SONiC dependencies, enabling
// standalone testing and reuse across different components.
package download

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// DownloadHTTP downloads a file from an HTTP URL to a local path with a size limit.
// The download is performed with the provided context for cancellation support.
// The output directory will be created if it doesn't exist.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - url: HTTP URL to download from
//   - localPath: Absolute path where the file should be saved
//   - maxSize: Maximum file size in bytes (0 = no limit)
//
// Returns an error if:
//   - The URL is invalid or unreachable
//   - The HTTP response status is not 2xx
//   - The file size exceeds maxSize
//   - The output directory cannot be created
//   - The file cannot be written
//   - The context timeout is exceeded
func DownloadHTTP(ctx context.Context, url, localPath string, maxSize int64) error {
	// Ensure output directory exists
	dir := filepath.Dir(localPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Create HTTP request with context for timeout and cancellation support
	// The context timeout (set by caller) controls the entire download operation
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Perform HTTP GET
	// Timeout is enforced via the context passed to NewRequestWithContext above.
	// This allows proper cancellation during any phase: connection, headers, or body download.
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	// Check HTTP status code
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP error %d: %s", resp.StatusCode, resp.Status)
	}

	// Check file size limit if specified
	if maxSize > 0 && resp.ContentLength > maxSize {
		return fmt.Errorf("file size %d bytes exceeds maximum allowed size %d bytes",
			resp.ContentLength, maxSize)
	}

	// Create output file
	outFile, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer outFile.Close()

	// Create a limited reader to enforce size limit during download
	var reader io.Reader = resp.Body
	if maxSize > 0 {
		// Add 1 byte to detect if server sends more than declared
		reader = io.LimitReader(resp.Body, maxSize+1)
	}

	// Copy response body to file
	written, err := io.Copy(outFile, reader)
	if err != nil {
		// Clean up partial file on error
		os.Remove(localPath)
		return fmt.Errorf("failed to write file: %w", err)
	}

	// Verify size limit wasn't exceeded during download
	if maxSize > 0 && written > maxSize {
		os.Remove(localPath)
		return fmt.Errorf("downloaded file size %d bytes exceeds maximum allowed size %d bytes",
			written, maxSize)
	}

	return nil
}

// DownloadHTTPStreaming creates an HTTP stream for downloading from a URL.
// Returns an io.ReadCloser that streams the response body directly without
// writing to disk. The caller is responsible for closing the reader.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - url: HTTP URL to download from
//   - maxSize: Maximum file size in bytes (0 = no limit)
//
// Returns:
//   - io.ReadCloser: Stream of the response body
//   - int64: Content length from HTTP headers (-1 if unknown)
//   - error: Any error during HTTP request setup
//
// The returned reader will enforce size limits during streaming.
// Context cancellation will abort the stream.
func DownloadHTTPStreaming(ctx context.Context, url string, maxSize int64) (io.ReadCloser, int64, error) {
	// Create HTTP request with context for timeout and cancellation support
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, -1, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Perform HTTP GET
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, -1, fmt.Errorf("HTTP request failed: %w", err)
	}

	// Check HTTP status code
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, -1, fmt.Errorf("HTTP error %d: %s", resp.StatusCode, resp.Status)
	}

	// Check file size limit if specified
	if maxSize > 0 && resp.ContentLength > maxSize {
		resp.Body.Close()
		return nil, -1, fmt.Errorf("file size %d bytes exceeds maximum allowed size %d bytes",
			resp.ContentLength, maxSize)
	}

	// Create a limited reader to enforce size limit during streaming
	var reader io.ReadCloser = resp.Body
	if maxSize > 0 {
		// Wrap with limit reader allowing one extra byte for oversized file detection.
		// This intentionally allows reading maxSize+1 bytes so that limitedReadCloser.Read()
		// can detect when the file exceeds the limit and return an appropriate error.
		limitedReader := io.LimitReader(resp.Body, maxSize+1)
		reader = &limitedReadCloser{
			Reader:  limitedReader,
			closer:  resp.Body,
			maxSize: maxSize,
		}
	}

	return reader, resp.ContentLength, nil
}

// limitedReadCloser wraps a LimitReader with size checking and proper cleanup
type limitedReadCloser struct {
	io.Reader
	closer  io.Closer
	maxSize int64
	read    int64
}

func (l *limitedReadCloser) Read(p []byte) (n int, err error) {
	n, err = l.Reader.Read(p)
	l.read += int64(n)

	// Check if we exceeded the size limit
	if l.maxSize > 0 && l.read > l.maxSize {
		l.closer.Close()
		return n, fmt.Errorf("downloaded file size exceeds maximum allowed size %d bytes", l.maxSize)
	}

	return n, err
}

func (l *limitedReadCloser) Close() error {
	return l.closer.Close()
}
