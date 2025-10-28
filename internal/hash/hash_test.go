package hash

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestCalculateMD5Reader(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		expected string // hex-encoded MD5
	}{
		{
			name:     "empty data",
			data:     []byte{},
			expected: "d41d8cd98f00b204e9800998ecf8427e", // MD5 of empty string
		},
		{
			name:     "hello world",
			data:     []byte("hello world"),
			expected: "5eb63bbbe01eeed093cb22bb8f5acdc3",
		},
		{
			name:     "test data",
			data:     []byte("The quick brown fox jumps over the lazy dog"),
			expected: "9e107d9d372bb6826bd81d3542a419d6",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := bytes.NewReader(tt.data)
			hash, err := CalculateMD5Reader(reader)
			if err != nil {
				t.Fatalf("CalculateMD5Reader() error = %v", err)
			}

			// Convert to hex for comparison
			gotHex := hex.EncodeToString(hash)
			if gotHex != tt.expected {
				t.Errorf("CalculateMD5Reader() = %s, want %s", gotHex, tt.expected)
			}

			// Verify we got 16 bytes (MD5 is always 128 bits = 16 bytes)
			if len(hash) != 16 {
				t.Errorf("CalculateMD5Reader() returned %d bytes, want 16", len(hash))
			}
		})
	}
}

func TestCalculateMD5(t *testing.T) {
	// Create a temporary file with known content
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test.txt")

	testData := []byte("hello world")
	expectedMD5 := "5eb63bbbe01eeed093cb22bb8f5acdc3"

	err := os.WriteFile(testFile, testData, 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Calculate MD5 of the file
	hash, err := CalculateMD5(testFile)
	if err != nil {
		t.Fatalf("CalculateMD5() error = %v", err)
	}

	gotHex := hex.EncodeToString(hash)
	if gotHex != expectedMD5 {
		t.Errorf("CalculateMD5() = %s, want %s", gotHex, expectedMD5)
	}

	// Verify we got 16 bytes
	if len(hash) != 16 {
		t.Errorf("CalculateMD5() returned %d bytes, want 16", len(hash))
	}
}

func TestCalculateMD5_NonExistentFile(t *testing.T) {
	_, err := CalculateMD5("/nonexistent/file/path")
	if err == nil {
		t.Error("CalculateMD5() expected error for non-existent file, got nil")
	}
}

func TestCalculateMD5_LargeFile(t *testing.T) {
	// Test with a larger file to ensure streaming works
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "large.txt")

	// Create a 1MB file
	data := make([]byte, 1024*1024)
	for i := range data {
		data[i] = byte(i % 256)
	}

	err := os.WriteFile(testFile, data, 0644)
	if err != nil {
		t.Fatalf("Failed to create large test file: %v", err)
	}

	// Calculate MD5 - should not error
	hash, err := CalculateMD5(testFile)
	if err != nil {
		t.Fatalf("CalculateMD5() error = %v", err)
	}

	// Verify we got 16 bytes
	if len(hash) != 16 {
		t.Errorf("CalculateMD5() returned %d bytes, want 16", len(hash))
	}

	// Calculate using reader for comparison
	file, err := os.Open(testFile)
	if err != nil {
		t.Fatalf("Failed to open test file: %v", err)
	}
	defer file.Close()

	hash2, err := CalculateMD5Reader(file)
	if err != nil {
		t.Fatalf("CalculateMD5Reader() error = %v", err)
	}

	// Both methods should produce same hash
	if !bytes.Equal(hash, hash2) {
		t.Errorf("CalculateMD5() and CalculateMD5Reader() produced different hashes")
	}
}
