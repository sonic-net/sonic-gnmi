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
