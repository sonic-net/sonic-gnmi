// Package hash provides cryptographic hash calculation utilities.
// This package is pure Go with no CGO or SONiC dependencies, enabling
// standalone testing and reuse across different components.
package hash

import (
	"crypto/md5"
	"fmt"
	"hash"
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
// MD5 is required by gNOI File.TransferToRemote specification (types.HashType_MD5).
// This is used for file integrity verification, not cryptographic security.
func CalculateMD5Reader(reader io.Reader) ([]byte, error) {
	hasher := md5.New() // nosemgrep: go.lang.security.audit.crypto.use_of_weak_crypto.use-of-md5

	if _, err := io.Copy(hasher, reader); err != nil {
		return nil, fmt.Errorf("failed to read data for hashing: %w", err)
	}

	return hasher.Sum(nil), nil
}

// StreamingMD5Calculator provides concurrent MD5 hash calculation during streaming.
// It implements io.Writer to receive data chunks and calculates MD5 hash concurrently.
// This is useful for calculating hashes while streaming data to another destination.
type StreamingMD5Calculator struct {
	hasher hash.Hash
}

// NewStreamingMD5Calculator creates a new streaming MD5 calculator.
func NewStreamingMD5Calculator() *StreamingMD5Calculator {
	return &StreamingMD5Calculator{
		hasher: md5.New(), // nosemgrep: go.lang.security.audit.crypto.use_of_weak_crypto.use-of-md5
	}
}

// Write implements io.Writer to receive data chunks for hash calculation.
func (s *StreamingMD5Calculator) Write(p []byte) (n int, err error) {
	return s.hasher.Write(p)
}

// Sum returns the final MD5 hash bytes. Can only be called after all data has been written.
// Returns the raw MD5 hash bytes (16 bytes), not hex-encoded.
func (s *StreamingMD5Calculator) Sum() []byte {
	return s.hasher.Sum(nil)
}
