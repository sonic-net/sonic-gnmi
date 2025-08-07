// Package checksum provides utilities for validating file checksums.
// Currently supports MD5 validation with plans for future algorithm support.
package checksum

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/golang/glog"
)

// Validator interface defines the contract for checksum validation.
// This allows for future extension to support multiple checksum algorithms.
type Validator interface {
	// ValidateFile checks if the file at the given path matches the expected checksum.
	// Returns nil if validation succeeds, error otherwise.
	ValidateFile(filePath, expectedChecksum string) error

	// CalculateChecksum computes the checksum of the file at the given path.
	// Returns the checksum as a hex string and any error encountered.
	CalculateChecksum(filePath string) (string, error)
}

// MD5Validator implements the Validator interface for MD5 checksums.
type MD5Validator struct{}

// NewMD5Validator creates a new MD5 checksum validator.
func NewMD5Validator() *MD5Validator {
	return &MD5Validator{}
}

// ValidateFile validates that the file's MD5 checksum matches the expected value.
func (v *MD5Validator) ValidateFile(filePath, expectedChecksum string) error {
	if filePath == "" {
		return fmt.Errorf("file path cannot be empty")
	}
	if expectedChecksum == "" {
		return fmt.Errorf("expected checksum cannot be empty")
	}

	glog.V(2).Infof("Validating MD5 checksum for file: %s", filePath)

	actualChecksum, err := v.CalculateChecksum(filePath)
	if err != nil {
		return fmt.Errorf("failed to calculate checksum: %w", err)
	}

	// Case-insensitive comparison as checksums can be represented in either case
	if !strings.EqualFold(actualChecksum, expectedChecksum) {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", expectedChecksum, actualChecksum)
	}

	glog.V(2).Infof("MD5 checksum validation successful for file: %s", filePath)
	return nil
}

// CalculateChecksum computes the MD5 checksum of the file.
func (v *MD5Validator) CalculateChecksum(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	hash := md5.New() // nosemgrep: go.lang.security.audit.crypto.use_of_weak_crypto.use-of-md5
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}

	checksum := hex.EncodeToString(hash.Sum(nil))
	glog.V(3).Infof("Calculated MD5 checksum for %s: %s", filePath, checksum)

	return checksum, nil
}
