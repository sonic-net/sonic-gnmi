package download

import (
	"fmt"
	"time"
)

// ErrorCategory represents the type of download error.
type ErrorCategory string

const (
	// ErrorCategoryNetwork represents network connectivity issues.
	ErrorCategoryNetwork ErrorCategory = "network"
	// ErrorCategoryHTTP represents HTTP protocol errors.
	ErrorCategoryHTTP ErrorCategory = "http"
	// ErrorCategoryFileSystem represents local file system errors.
	ErrorCategoryFileSystem ErrorCategory = "filesystem"
	// ErrorCategoryValidation represents validation errors (e.g., checksum mismatch).
	ErrorCategoryValidation ErrorCategory = "validation"
	// ErrorCategoryOther represents other types of errors.
	ErrorCategoryOther ErrorCategory = "other"
)

// Attempt represents a single download attempt with its outcome.
type Attempt struct {
	// Method describes how the download was attempted.
	Method string `json:"method"`
	// Interface is the network interface or IP address used.
	Interface string `json:"interface,omitempty"`
	// Error contains the error message for this attempt (empty if successful).
	Error string `json:"error,omitempty"`
	// Duration is how long this attempt took.
	Duration time.Duration `json:"duration"`
	// HTTPStatus is the HTTP status code received (0 if no response).
	HTTPStatus int `json:"http_status,omitempty"`
}

// DownloadError provides structured error information for download failures.
type DownloadError struct {
	// Category classifies the type of error.
	Category ErrorCategory `json:"category"`
	// Code provides additional error context (e.g., HTTP status code).
	Code int `json:"code,omitempty"`
	// Message is a human-readable error description.
	Message string `json:"message"`
	// URL is the URL that failed to download.
	URL string `json:"url"`
	// Attempts contains details of all retry attempts made.
	Attempts []Attempt `json:"attempts"`
}

// NewNetworkError creates a new network-related download error.
func NewNetworkError(url, message string, attempts []Attempt) *DownloadError {
	return &DownloadError{
		Category: ErrorCategoryNetwork,
		Message:  message,
		URL:      url,
		Attempts: attempts,
	}
}

// NewHTTPError creates a new HTTP-related download error.
func NewHTTPError(url, message string, httpCode int, attempts []Attempt) *DownloadError {
	return &DownloadError{
		Category: ErrorCategoryHTTP,
		Code:     httpCode,
		Message:  message,
		URL:      url,
		Attempts: attempts,
	}
}

// NewFileSystemError creates a new file system-related download error.
func NewFileSystemError(url, message string, attempts []Attempt) *DownloadError {
	return &DownloadError{
		Category: ErrorCategoryFileSystem,
		Message:  message,
		URL:      url,
		Attempts: attempts,
	}
}

// NewValidationError creates a new validation-related download error.
func NewValidationError(url, message string, attempts []Attempt) *DownloadError {
	return &DownloadError{
		Category: ErrorCategoryValidation,
		Message:  message,
		URL:      url,
		Attempts: attempts,
	}
}

// NewOtherError creates a new error for cases that don't fit other categories.
func NewOtherError(url, message string, attempts []Attempt) *DownloadError {
	return &DownloadError{
		Category: ErrorCategoryOther,
		Message:  message,
		URL:      url,
		Attempts: attempts,
	}
}

// Error implements the error interface.
func (e *DownloadError) Error() string {
	if len(e.Attempts) == 0 {
		return fmt.Sprintf("download failed: %s", e.Message)
	}
	return fmt.Sprintf("download failed after %d attempts: %s", len(e.Attempts), e.Message)
}

// IsNetworkError returns true if this is a network connectivity error.
func (e *DownloadError) IsNetworkError() bool {
	return e.Category == ErrorCategoryNetwork
}

// IsHTTPError returns true if this is an HTTP protocol error.
func (e *DownloadError) IsHTTPError() bool {
	return e.Category == ErrorCategoryHTTP
}

// IsFileSystemError returns true if this is a file system error.
func (e *DownloadError) IsFileSystemError() bool {
	return e.Category == ErrorCategoryFileSystem
}

// IsValidationError returns true if this is a validation error.
func (e *DownloadError) IsValidationError() bool {
	return e.Category == ErrorCategoryValidation
}

// LastAttempt returns the last attempt made, or nil if no attempts were made.
func (e *DownloadError) LastAttempt() *Attempt {
	if len(e.Attempts) == 0 {
		return nil
	}
	return &e.Attempts[len(e.Attempts)-1]
}
