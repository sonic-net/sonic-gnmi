package download

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/golang/glog"
)

// DownloadResult contains information about a successful download.
type DownloadResult struct {
	// FilePath is the path where the file was saved
	FilePath string `json:"file_path"`
	// FileSize is the size of the downloaded file in bytes
	FileSize int64 `json:"file_size"`
	// Duration is how long the download took
	Duration time.Duration `json:"duration"`
	// AttemptCount is the number of attempts made
	AttemptCount int `json:"attempt_count"`
	// FinalMethod describes how the download ultimately succeeded
	FinalMethod string `json:"final_method"`
	// URL is the URL that was downloaded
	URL string `json:"url"`
}

// DownloadConfig contains configuration options for downloads.
type DownloadConfig struct {
	// ConnectTimeout is the timeout for establishing connections
	ConnectTimeout time.Duration
	// Interface is the preferred network interface to use
	Interface string
	// MaxRetries is the maximum number of retry attempts
	MaxRetries int
	// UserAgent is the User-Agent header to send
	UserAgent string
}

// DefaultDownloadConfig returns a default download configuration.
func DefaultDownloadConfig() *DownloadConfig {
	return &DownloadConfig{
		ConnectTimeout: 30 * time.Second,
		Interface:      DefaultInterface,
		MaxRetries:     3,
		UserAgent:      "sonic-ops-server/1.0",
	}
}

// DownloadFirmware downloads a firmware image from the specified URL
// If outputPath is empty, it will be automatically determined from the URL.
func DownloadFirmware(ctx context.Context, downloadURL, outputPath string) (*DownloadResult, error) {
	return DownloadFirmwareWithConfig(ctx, downloadURL, outputPath, DefaultDownloadConfig())
}

// DownloadFirmwareWithConfig downloads a firmware image with custom configuration.
func DownloadFirmwareWithConfig(
	ctx context.Context, downloadURL, outputPath string, config *DownloadConfig,
) (*DownloadResult, error) {
	startTime := time.Now()
	var attempts []Attempt

	glog.V(1).Infof("Starting download of %s", downloadURL)

	// Determine output path if not provided
	if outputPath == "" {
		var err error
		outputPath, err = getOutputPathFromURL(downloadURL)
		if err != nil {
			return nil, NewOtherError(downloadURL, fmt.Sprintf("failed to determine output path: %v", err), attempts)
		}
	}

	// Ensure output directory exists
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return nil, NewFileSystemError(downloadURL, fmt.Sprintf("failed to create output directory: %v", err), attempts)
	}

	// Strategy 1: Try with interface binding if interface is up
	if config.Interface != "" && IsInterfaceUp(config.Interface) {
		glog.V(2).Infof("Interface %s is up, attempting download with interface binding", config.Interface)

		result, err := attemptDownloadWithInterface(ctx, downloadURL, outputPath, config, &attempts)
		if err == nil {
			return createSuccessResult(downloadURL, outputPath, startTime, len(attempts), "interface", result.FileSize), nil
		}

		// Check if we should continue with fallback strategies
		if !shouldRetryWithFallback(err) {
			return nil, err
		}
	}

	// Strategy 2: Try with specific IP addresses
	if config.Interface != "" {
		glog.V(2).Infof("Attempting download with specific IP addresses from %s", config.Interface)

		result, err := attemptDownloadWithIPs(ctx, downloadURL, outputPath, config, &attempts)
		if err == nil {
			return createSuccessResult(downloadURL, outputPath, startTime, len(attempts), "ip", result.FileSize), nil
		}

		// Check if we should continue with direct connection
		if !shouldRetryWithFallback(err) {
			return nil, err
		}
	}

	// Strategy 3: Try direct connection without interface binding
	glog.V(2).Info("Attempting download without interface binding")
	result, err := attemptDirectDownload(ctx, downloadURL, outputPath, config, &attempts)
	if err == nil {
		return createSuccessResult(downloadURL, outputPath, startTime, len(attempts), "direct", result.FileSize), nil
	}

	// All strategies failed
	return nil, err
}

// attemptDownloadWithInterface tries to download using interface binding.
func attemptDownloadWithInterface(
	ctx context.Context, downloadURL, outputPath string, config *DownloadConfig, attempts *[]Attempt,
) (*downloadAttemptResult, error) {
	attempt := Attempt{
		Method:    "interface",
		Interface: config.Interface,
	}
	attemptStart := time.Now()

	// Create HTTP client with interface binding
	dialer := &net.Dialer{
		Timeout: config.ConnectTimeout,
	}

	// Try to bind to the interface by using its first IP address
	interfaceInfo, err := GetInterfaceInfo(config.Interface)
	if err != nil {
		attempt.Error = fmt.Sprintf("failed to get interface info: %v", err)
		attempt.Duration = time.Since(attemptStart)
		*attempts = append(*attempts, attempt)
		return nil, NewNetworkError(downloadURL, attempt.Error, *attempts)
	}

	// Get the first available IP address
	var localAddr net.Addr
	if len(interfaceInfo.IPv4Addrs) > 0 {
		localAddr = &net.TCPAddr{IP: net.ParseIP(interfaceInfo.IPv4Addrs[0])}
		attempt.Interface = interfaceInfo.IPv4Addrs[0]
	} else if len(interfaceInfo.IPv6Addrs) > 0 {
		localAddr = &net.TCPAddr{IP: net.ParseIP(interfaceInfo.IPv6Addrs[0])}
		attempt.Interface = interfaceInfo.IPv6Addrs[0]
	} else {
		attempt.Error = "no IP addresses found on interface"
		attempt.Duration = time.Since(attemptStart)
		*attempts = append(*attempts, attempt)
		return nil, NewNetworkError(downloadURL, attempt.Error, *attempts)
	}

	dialer.LocalAddr = localAddr

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: dialer.DialContext,
		},
		Timeout: 5 * time.Minute, // Overall timeout for the entire request
	}

	result, err := performDownload(ctx, client, downloadURL, outputPath, config.UserAgent)
	attempt.Duration = time.Since(attemptStart)

	if err != nil {
		attempt.Error = err.Error()
		// Capture HTTP status even on error if available
		if result != nil {
			attempt.HTTPStatus = result.HTTPStatus
		}
		*attempts = append(*attempts, attempt)
		return nil, classifyError(downloadURL, err, *attempts)
	}

	attempt.HTTPStatus = result.HTTPStatus
	*attempts = append(*attempts, attempt)
	return result, nil
}

// attemptDownloadWithIPs tries to download using specific IP addresses.
func attemptDownloadWithIPs(
	ctx context.Context, downloadURL, outputPath string, config *DownloadConfig, attempts *[]Attempt,
) (*downloadAttemptResult, error) {
	interfaceInfo, err := GetInterfaceInfo(config.Interface)
	if err != nil {
		return nil, NewNetworkError(downloadURL, fmt.Sprintf("failed to get interface info: %v", err), *attempts)
	}

	// Get relevant IP addresses based on the target URL
	ipAddresses := GetRelevantIPAddresses(interfaceInfo, downloadURL)
	if len(ipAddresses) == 0 {
		return nil, NewNetworkError(downloadURL, "no suitable IP addresses found on interface", *attempts)
	}

	// Try each IP address
	for _, ipAddr := range ipAddresses {
		attempt := Attempt{
			Method:    "ip",
			Interface: ipAddr,
		}
		attemptStart := time.Now()

		glog.V(2).Infof("Retrying download through %s", ipAddr)

		// Create dialer with specific IP
		dialer := &net.Dialer{
			Timeout:   config.ConnectTimeout,
			LocalAddr: &net.TCPAddr{IP: net.ParseIP(ipAddr)},
		}

		client := &http.Client{
			Transport: &http.Transport{
				DialContext: dialer.DialContext,
			},
			Timeout: 5 * time.Minute,
		}

		result, err := performDownload(ctx, client, downloadURL, outputPath, config.UserAgent)
		attempt.Duration = time.Since(attemptStart)

		if err != nil {
			attempt.Error = err.Error()
			// Capture HTTP status even on error if available
			if result != nil {
				attempt.HTTPStatus = result.HTTPStatus
			}
			*attempts = append(*attempts, attempt)
			glog.V(2).Infof("Download failed with mgmt ip %s: %v", ipAddr, err)
			continue
		}

		// Success
		attempt.HTTPStatus = result.HTTPStatus
		*attempts = append(*attempts, attempt)
		return result, nil
	}

	// All IP addresses failed
	return nil, NewNetworkError(downloadURL, "download failed with all IP addresses", *attempts)
}

// attemptDirectDownload tries to download without interface binding.
func attemptDirectDownload(
	ctx context.Context, downloadURL, outputPath string, config *DownloadConfig, attempts *[]Attempt,
) (*downloadAttemptResult, error) {
	attempt := Attempt{
		Method: "direct",
	}
	attemptStart := time.Now()

	glog.V(2).Info("Download failed with interface specifier, retrying without it...")

	client := &http.Client{
		Timeout: 5 * time.Minute,
	}

	result, err := performDownload(ctx, client, downloadURL, outputPath, config.UserAgent)
	attempt.Duration = time.Since(attemptStart)

	if err != nil {
		attempt.Error = err.Error()
		// Capture HTTP status even on error if available
		if result != nil {
			attempt.HTTPStatus = result.HTTPStatus
		}
		*attempts = append(*attempts, attempt)
		return nil, classifyError(downloadURL, err, *attempts)
	}

	attempt.HTTPStatus = result.HTTPStatus
	*attempts = append(*attempts, attempt)
	return result, nil
}

// downloadAttemptResult contains the result of a single download attempt.
type downloadAttemptResult struct {
	FileSize   int64
	HTTPStatus int
}

// performDownload executes the actual HTTP download.
func performDownload(
	ctx context.Context, client *http.Client, downloadURL, outputPath, userAgent string,
) (*downloadAttemptResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &downloadAttemptResult{HTTPStatus: resp.StatusCode},
			fmt.Errorf("HTTP error %d: %s", resp.StatusCode, resp.Status)
	}

	// Create output file
	outFile, err := os.Create(outputPath)
	if err != nil {
		return &downloadAttemptResult{HTTPStatus: resp.StatusCode},
			fmt.Errorf("failed to create output file: %w", err)
	}
	defer outFile.Close()

	// Copy response body to file
	written, err := io.Copy(outFile, resp.Body)
	if err != nil {
		// Clean up partial file
		os.Remove(outputPath)
		return &downloadAttemptResult{HTTPStatus: resp.StatusCode},
			fmt.Errorf("failed to write file: %w", err)
	}

	return &downloadAttemptResult{
		FileSize:   written,
		HTTPStatus: resp.StatusCode,
	}, nil
}

// getOutputPathFromURL determines the output filename from a URL.
func getOutputPathFromURL(downloadURL string) (string, error) {
	parsedURL, err := url.Parse(downloadURL)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}

	// Extract filename from path
	filename := filepath.Base(parsedURL.Path)
	if filename == "" || filename == "." || filename == "/" {
		return "", fmt.Errorf("cannot determine filename from URL")
	}

	// Use current directory
	return filename, nil
}

// shouldRetryWithFallback determines if we should try fallback strategies.
func shouldRetryWithFallback(err error) bool {
	if downloadErr, ok := err.(*DownloadError); ok {
		// Only retry network errors, not HTTP or filesystem errors
		return downloadErr.Category == ErrorCategoryNetwork
	}
	return false
}

// classifyError converts a generic error into a structured DownloadError.
func classifyError(downloadURL string, err error, attempts []Attempt) *DownloadError {
	errMsg := err.Error()

	// Check for HTTP errors
	if strings.Contains(errMsg, "HTTP error") {
		// Extract status code if possible
		var statusCode int
		if lastAttempt := getLastAttempt(attempts); lastAttempt != nil && lastAttempt.HTTPStatus > 0 {
			statusCode = lastAttempt.HTTPStatus
		}
		return NewHTTPError(downloadURL, errMsg, statusCode, attempts)
	}

	// Check for filesystem errors
	if strings.Contains(errMsg, "failed to create") || strings.Contains(errMsg, "failed to write") ||
		strings.Contains(errMsg, "permission denied") || strings.Contains(errMsg, "no space left") {
		return NewFileSystemError(downloadURL, errMsg, attempts)
	}

	// Check for network errors
	if strings.Contains(errMsg, "connection") || strings.Contains(errMsg, "timeout") ||
		strings.Contains(errMsg, "network") || strings.Contains(errMsg, "dial") ||
		strings.Contains(errMsg, "no such host") || strings.Contains(errMsg, "refused") {
		return NewNetworkError(downloadURL, errMsg, attempts)
	}

	// Default to other error
	return NewOtherError(downloadURL, errMsg, attempts)
}

// getLastAttempt returns the last attempt from the attempts slice.
func getLastAttempt(attempts []Attempt) *Attempt {
	if len(attempts) == 0 {
		return nil
	}
	return &attempts[len(attempts)-1]
}

// createSuccessResult creates a DownloadResult for successful downloads.
func createSuccessResult(
	downloadURL, outputPath string, startTime time.Time, attemptCount int, method string, fileSize int64,
) *DownloadResult {
	return &DownloadResult{
		FilePath:     outputPath,
		FileSize:     fileSize,
		Duration:     time.Since(startTime),
		AttemptCount: attemptCount,
		FinalMethod:  method,
		URL:          downloadURL,
	}
}
