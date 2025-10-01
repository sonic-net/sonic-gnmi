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

// DownloadHTTP downloads a file from an HTTP URL to a local path.
// The download is performed with the provided context for cancellation support.
// The output directory will be created if it doesn't exist.
//
// Parameters:
//   - ctx: Context for cancellation
//   - url: HTTP URL to download from
//   - localPath: Absolute path where the file should be saved
//
// Returns an error if:
//   - The URL is invalid or unreachable
//   - The HTTP response status is not 2xx
//   - The output directory cannot be created
//   - The file cannot be written
func DownloadHTTP(ctx context.Context, url, localPath string) error {
	// Ensure output directory exists
	dir := filepath.Dir(localPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Create HTTP request with context
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Perform HTTP GET
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

	// Create output file
	outFile, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer outFile.Close()

	// Copy response body to file
	_, err = io.Copy(outFile, resp.Body)
	if err != nil {
		// Clean up partial file on error
		os.Remove(localPath)
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}
