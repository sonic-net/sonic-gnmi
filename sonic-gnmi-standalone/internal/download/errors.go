package download

import (
	"fmt"
	"time"
)

// ErrorCategory represents the type of download error.
type ErrorCategory string

const (
	// ErrorCategoryNetwork represents network and HTTP errors.
	ErrorCategoryNetwork ErrorCategory = "network"
	// ErrorCategoryValidation represents validation errors (e.g., checksum mismatch).
	ErrorCategoryValidation ErrorCategory = "validation"
	// ErrorCategoryOther represents filesystem and other errors.
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

// NewNetworkError creates a new network/HTTP-related download error.
func NewNetworkError(url, message string, attempts []Attempt) *DownloadError {
	return &DownloadError{
		Category: ErrorCategoryNetwork,
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

// IsNetworkError returns true if this is a network/HTTP error.
func (e *DownloadError) IsNetworkError() bool {
	return e.Category == ErrorCategoryNetwork
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
