// Package download provides a robust file download engine with
// network interface binding and comprehensive error handling.
//
// Key features:
//   - Session management for future progress tracking
//   - Network interface-specific binding for multi-interface systems
//   - Configurable timeouts for connection and total download time
//   - Automatic retry mechanisms with fallback strategies
//   - IPv4/IPv6 dual-stack support
//   - Thread-safe session updates
//
// The download engine supports various network configurations including:
//   - Single and multi-interface systems
//   - Container deployments with host network access
//   - Baremetal installations with direct network access
//   - Custom HTTP client configurations with proxy support
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
	"sync"
	"time"

	"github.com/golang/glog"
	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/internal/checksum"
)

// DownloadSession tracks the progress and state of an ongoing download.
// All progress data is protected by an internal mutex for thread-safe access
// from both the download goroutine and status query handlers.
type DownloadSession struct {
	ID         string // Unique session identifier
	URL        string // Source URL being downloaded
	OutputPath string // Destination file path

	// Progress data - updated by download, read by status queries
	Downloaded       int64     // Bytes downloaded so far
	Total            int64     // Total bytes to download (from Content-Length)
	SpeedBytesPerSec float64   // Current download speed in bytes per second
	Status           string    // Current status (starting, downloading, completed, failed)
	CurrentMethod    string    // Active download method (interface name, etc.)
	AttemptNumber    int       // Current retry attempt number
	StartTime        time.Time // When the download session began
	LastUpdate       time.Time // Last progress update timestamp
	Error            error     // Last error encountered (nil if no error)

	mu     sync.RWMutex       // Protects all fields above for concurrent access
	cancel context.CancelFunc // Allows cancellation of the download operation
}

// UpdateProgress updates the download progress in a thread-safe manner.
// This method is called by the download goroutine to report real-time progress
// and can be safely called concurrently with GetProgress().
func (s *DownloadSession) UpdateProgress(downloaded, total int64, speed float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Downloaded = downloaded
	s.Total = total
	s.SpeedBytesPerSec = speed
	s.LastUpdate = time.Now()
}

// GetProgress returns current progress in a thread-safe manner.
// This method is called by status query handlers to provide real-time download information
// to clients. Returns downloaded bytes, total bytes, current speed, and status string.
func (s *DownloadSession) GetProgress() (downloaded, total int64, speed float64, status string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Downloaded, s.Total, s.SpeedBytesPerSec, s.Status
}

// UpdateStatus updates the download status in a thread-safe manner.
func (s *DownloadSession) UpdateStatus(status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Status = status
	s.LastUpdate = time.Now()
}

// UpdateCurrentMethod updates the current download method in a thread-safe manner.
func (s *DownloadSession) UpdateCurrentMethod(method string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.CurrentMethod = method
	s.LastUpdate = time.Now()
}

// ChecksumValidationResult contains information about checksum validation.
type ChecksumValidationResult struct {
	// ValidationRequested indicates whether checksum validation was requested
	ValidationRequested bool `json:"validation_requested"`
	// ValidationPassed indicates whether validation passed (only meaningful if ValidationRequested is true)
	ValidationPassed bool `json:"validation_passed"`
	// ExpectedChecksum is the checksum provided by the client
	ExpectedChecksum string `json:"expected_checksum"`
	// ActualChecksum is the checksum calculated from the downloaded file
	ActualChecksum string `json:"actual_checksum"`
	// Algorithm is the checksum algorithm used (e.g., "md5")
	Algorithm string `json:"algorithm"`
}

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
	// ChecksumValidation contains checksum validation information
	ChecksumValidation ChecksumValidationResult `json:"checksum_validation"`
}

// DownloadConfig contains configuration options for downloads.
type DownloadConfig struct {
	// ConnectTimeout is the timeout for establishing connections
	ConnectTimeout time.Duration
	// OverallTimeout is the timeout for the entire HTTP request
	OverallTimeout time.Duration
	// Interface is the preferred network interface to use
	Interface string
	// MaxRetries is the maximum number of retry attempts
	MaxRetries int
	// UserAgent is the User-Agent header to send
	UserAgent string
	// ExpectedMD5 is the expected MD5 checksum for validation (optional)
	ExpectedMD5 string
}

// DefaultDownloadConfig returns a default download configuration.
func DefaultDownloadConfig() *DownloadConfig {
	return &DownloadConfig{
		ConnectTimeout: 30 * time.Second,
		OverallTimeout: 10 * time.Minute,
		Interface:      DefaultInterface,
		MaxRetries:     3,
		UserAgent:      "sonic-ops-server/1.0",
	}
}

// DownloadFile downloads a file from the specified URL
// If outputPath is empty, it will be automatically determined from the URL.
// Returns both the session (for future progress tracking) and final result.
func DownloadFile(ctx context.Context, downloadURL, outputPath string) (*DownloadSession, *DownloadResult, error) {
	return DownloadFileWithConfig(ctx, downloadURL, outputPath, DefaultDownloadConfig())
}

// DownloadFileWithConfig downloads a file with custom configuration.
// Returns both the session (for future progress tracking) and final result.
func DownloadFileWithConfig(
	ctx context.Context, downloadURL, outputPath string, config *DownloadConfig,
) (*DownloadSession, *DownloadResult, error) {
	startTime := time.Now()
	var attempts []Attempt

	glog.V(1).Infof("Starting download of %s", downloadURL)

	// Create download session
	session := &DownloadSession{
		ID:         fmt.Sprintf("download-%d", time.Now().UnixNano()),
		URL:        downloadURL,
		OutputPath: outputPath,
		Status:     "starting",
		StartTime:  startTime,
		LastUpdate: startTime,
	}

	// Determine output path if not provided
	if outputPath == "" {
		var err error
		outputPath, err = getOutputPathFromURL(downloadURL)
		if err != nil {
			session.UpdateStatus("failed")
			return session, nil, NewOtherError(downloadURL, fmt.Sprintf("failed to determine output path: %v", err), attempts)
		}
		session.OutputPath = outputPath
	}

	// Ensure output directory exists
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		session.UpdateStatus("failed")
		return session, nil, NewOtherError(downloadURL,
			fmt.Sprintf("failed to create output directory: %v", err), attempts)
	}

	// Strategy 1: Try with interface binding if interface is up
	if config.Interface != "" && IsInterfaceUp(config.Interface) {
		glog.V(2).Infof("Interface %s is up, attempting download with interface binding", config.Interface)
		session.UpdateCurrentMethod("interface")
		session.UpdateStatus("downloading")

		result, err := attemptDownloadWithInterface(ctx, downloadURL, outputPath, config, &attempts, session)
		if err == nil {
			// Perform checksum validation if requested
			validation, validationErr := validateChecksum(outputPath, config.ExpectedMD5)
			if validationErr != nil {
				session.UpdateStatus("failed")
				return session, nil, NewValidationError(downloadURL, validationErr.Error(), attempts)
			}
			session.UpdateStatus("completed")
			return session, createSuccessResult(downloadURL, outputPath, startTime, len(attempts),
				"interface", result.FileSize, validation), nil
		}

		// Check if we should continue with fallback strategies
		if !shouldRetryWithFallback(err) {
			session.UpdateStatus("failed")
			return session, nil, err
		}
	}

	// Strategy 2: Try direct connection without interface binding
	glog.V(2).Info("Attempting download without interface binding")
	session.UpdateCurrentMethod("direct")
	session.UpdateStatus("downloading")
	result, err := attemptDirectDownload(ctx, downloadURL, outputPath, config, &attempts, session)
	if err != nil {
		session.UpdateStatus("failed")
		return session, nil, err
	}

	// Perform checksum validation if requested
	validation, validationErr := validateChecksum(outputPath, config.ExpectedMD5)
	if validationErr != nil {
		session.UpdateStatus("failed")
		return session, nil, NewValidationError(downloadURL, validationErr.Error(), attempts)
	}
	session.UpdateStatus("completed")
	return session, createSuccessResult(downloadURL, outputPath, startTime, len(attempts),
		"direct", result.FileSize, validation), nil
}

// attemptDownloadWithInterface tries to download using interface binding.
func attemptDownloadWithInterface(
	ctx context.Context, downloadURL, outputPath string, config *DownloadConfig,
	attempts *[]Attempt, session *DownloadSession,
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
		Timeout: config.OverallTimeout,
	}

	result, err := performDownload(ctx, client, downloadURL, outputPath, config.UserAgent, session)
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

// attemptDirectDownload tries to download without interface binding.
func attemptDirectDownload(
	ctx context.Context, downloadURL, outputPath string, config *DownloadConfig,
	attempts *[]Attempt, session *DownloadSession,
) (*downloadAttemptResult, error) {
	attempt := Attempt{
		Method: "direct",
	}
	attemptStart := time.Now()

	glog.V(2).Info("Download failed with interface specifier, retrying without it...")

	client := &http.Client{
		Timeout: config.OverallTimeout,
	}

	result, err := performDownload(ctx, client, downloadURL, outputPath, config.UserAgent, session)
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

// updateProgress updates progress in session only.
func updateProgress(
	session *DownloadSession, written, contentLength int64, startTime time.Time, lastReport *time.Time,
) {
	elapsed := time.Since(startTime)
	speed := float64(written) / elapsed.Seconds()
	session.UpdateProgress(written, contentLength, speed)
	*lastReport = time.Now()
}

// copyWithProgress copies data from src to dst while updating progress in session.
func copyWithProgress(dst io.Writer, src io.Reader, session *DownloadSession, contentLength int64) (int64, error) {
	var written int64
	buffer := make([]byte, 32*1024) // 32KB chunks
	lastReport := time.Now()
	startTime := time.Now()

	for {
		nr, er := src.Read(buffer)
		if nr > 0 {
			nw, ew := dst.Write(buffer[0:nr])
			if nw > 0 {
				written += int64(nw)
			}

			// Update progress every 500ms
			if time.Since(lastReport) >= 500*time.Millisecond {
				updateProgress(session, written, contentLength, startTime, &lastReport)
			}

			if ew != nil {
				return written, ew
			}
		}
		if er != nil {
			if er != io.EOF {
				return written, er
			}
			break
		}
	}

	// Final progress update
	elapsed := time.Since(startTime)
	speed := float64(written) / elapsed.Seconds()
	session.UpdateProgress(written, contentLength, speed)

	return written, nil
}

// performDownload executes the actual HTTP download.
func performDownload(
	ctx context.Context, client *http.Client, downloadURL, outputPath, userAgent string, session *DownloadSession,
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

	// Get content length for session tracking
	contentLength := resp.ContentLength
	if contentLength > 0 {
		session.UpdateProgress(0, contentLength, 0)
	}

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

	// Copy response body to file with progress updates
	written, err := copyWithProgress(outFile, resp.Body, session, contentLength)
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

	// Check for validation errors
	if strings.Contains(errMsg, "checksum") || strings.Contains(errMsg, "validation") {
		return NewValidationError(downloadURL, errMsg, attempts)
	}

	// Check for network/HTTP errors
	if strings.Contains(errMsg, "HTTP error") || strings.Contains(errMsg, "connection") ||
		strings.Contains(errMsg, "timeout") || strings.Contains(errMsg, "network") ||
		strings.Contains(errMsg, "dial") || strings.Contains(errMsg, "no such host") ||
		strings.Contains(errMsg, "refused") {
		return NewNetworkError(downloadURL, errMsg, attempts)
	}

	// Default to other error (filesystem, etc.)
	return NewOtherError(downloadURL, errMsg, attempts)
}

// getLastAttempt returns the last attempt from the attempts slice.
func getLastAttempt(attempts []Attempt) *Attempt {
	if len(attempts) == 0 {
		return nil
	}
	return &attempts[len(attempts)-1]
}

// validateChecksum performs MD5 checksum validation if requested.
func validateChecksum(filePath, expectedMD5 string) (ChecksumValidationResult, error) {
	validation := ChecksumValidationResult{
		ValidationRequested: expectedMD5 != "",
		Algorithm:           "md5",
		ExpectedChecksum:    expectedMD5,
	}

	if !validation.ValidationRequested {
		return validation, nil
	}

	glog.V(2).Infof("Validating MD5 checksum for downloaded file: %s", filePath)

	validator := checksum.NewMD5Validator()
	actualChecksum, err := validator.CalculateChecksum(filePath)
	if err != nil {
		return validation, fmt.Errorf("failed to calculate checksum: %w", err)
	}

	validation.ActualChecksum = actualChecksum
	validation.ValidationPassed = strings.EqualFold(actualChecksum, expectedMD5)

	if !validation.ValidationPassed {
		return validation, fmt.Errorf("MD5 checksum mismatch: expected %s, got %s", expectedMD5, actualChecksum)
	}

	glog.V(2).Infof("MD5 checksum validation passed for file: %s", filePath)
	return validation, nil
}

// createSuccessResult creates a DownloadResult for successful downloads.
func createSuccessResult(
	downloadURL, outputPath string, startTime time.Time, attemptCount int, method string, fileSize int64,
	validation ChecksumValidationResult,
) *DownloadResult {
	return &DownloadResult{
		FilePath:           outputPath,
		FileSize:           fileSize,
		Duration:           time.Since(startTime),
		AttemptCount:       attemptCount,
		FinalMethod:        method,
		URL:                downloadURL,
		ChecksumValidation: validation,
	}
}
