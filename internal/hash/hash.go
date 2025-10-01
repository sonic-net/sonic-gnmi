// Package hash provides cryptographic hash calculation utilities.
// This package is pure Go with no CGO or SONiC dependencies, enabling
// standalone testing and reuse across different components.
package hash

import (
	"crypto/md5"
	"fmt"
	"io"
	"os"
)

// CalculateMD5 calculates the MD5 hash of a file.
// Returns the raw MD5 hash bytes (16 bytes), not hex-encoded.
// This matches the gNOI HashType format which expects raw bytes.
func CalculateMD5(filePath string) ([]byte, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	return CalculateMD5Reader(file)
}

// CalculateMD5Reader calculates the MD5 hash from an io.Reader.
// Returns the raw MD5 hash bytes (16 bytes), not hex-encoded.
// Useful for calculating hashes from streams or testing with in-memory data.
func CalculateMD5Reader(reader io.Reader) ([]byte, error) {
	hasher := md5.New()

	if _, err := io.Copy(hasher, reader); err != nil {
		return nil, fmt.Errorf("failed to read data for hashing: %w", err)
	}

	return hasher.Sum(nil), nil
}
