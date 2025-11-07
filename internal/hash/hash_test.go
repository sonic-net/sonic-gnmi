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

// Tests for streaming MD5 calculator

func TestNewStreamingMD5Calculator(t *testing.T) {
	calc := NewStreamingMD5Calculator()
	if calc == nil {
		t.Error("NewStreamingMD5Calculator() returned nil")
	}
}

func TestStreamingMD5Calculator_Write(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		expected string // hex-encoded MD5
	}{
		{
			name:     "empty data",
			data:     []byte{},
			expected: "d41d8cd98f00b204e9800998ecf8427e",
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
			calc := NewStreamingMD5Calculator()

			// Write all data at once
			n, err := calc.Write(tt.data)
			if err != nil {
				t.Fatalf("Write() error = %v", err)
			}
			if n != len(tt.data) {
				t.Errorf("Write() returned %d, want %d", n, len(tt.data))
			}

			// Get final hash
			hash := calc.Sum()
			gotHex := hex.EncodeToString(hash)
			if gotHex != tt.expected {
				t.Errorf("Sum() = %s, want %s", gotHex, tt.expected)
			}

			// Verify we got 16 bytes
			if len(hash) != 16 {
				t.Errorf("Sum() returned %d bytes, want 16", len(hash))
			}
		})
	}
}

func TestStreamingMD5Calculator_ChunkedWrite(t *testing.T) {
	data := []byte("The quick brown fox jumps over the lazy dog")
	expectedMD5 := "9e107d9d372bb6826bd81d3542a419d6"

	calc := NewStreamingMD5Calculator()

	// Write in small chunks
	chunkSize := 5
	for i := 0; i < len(data); i += chunkSize {
		end := i + chunkSize
		if end > len(data) {
			end = len(data)
		}

		n, err := calc.Write(data[i:end])
		if err != nil {
			t.Fatalf("Write() chunk %d error = %v", i/chunkSize, err)
		}
		if n != (end - i) {
			t.Errorf("Write() chunk %d returned %d, want %d", i/chunkSize, n, end-i)
		}
	}

	// Get final hash
	hash := calc.Sum()
	gotHex := hex.EncodeToString(hash)
	if gotHex != expectedMD5 {
		t.Errorf("Sum() after chunked writes = %s, want %s", gotHex, expectedMD5)
	}
}

func TestStreamingMD5Calculator_LargeData(t *testing.T) {
	// Test with 1MB of data
	data := make([]byte, 1024*1024)
	for i := range data {
		data[i] = byte(i % 256)
	}

	calc := NewStreamingMD5Calculator()

	// Write in 64KB chunks
	chunkSize := 64 * 1024
	for i := 0; i < len(data); i += chunkSize {
		end := i + chunkSize
		if end > len(data) {
			end = len(data)
		}

		n, err := calc.Write(data[i:end])
		if err != nil {
			t.Fatalf("Write() large chunk error = %v", err)
		}
		if n != (end - i) {
			t.Errorf("Write() large chunk returned %d, want %d", n, end-i)
		}
	}

	// Get final hash
	hash := calc.Sum()
	if len(hash) != 16 {
		t.Errorf("Sum() returned %d bytes, want 16", len(hash))
	}

	// Compare with direct MD5Reader calculation
	reader := bytes.NewReader(data)
	expectedHash, err := CalculateMD5Reader(reader)
	if err != nil {
		t.Fatalf("CalculateMD5Reader() error = %v", err)
	}

	if !bytes.Equal(hash, expectedHash) {
		t.Errorf("StreamingMD5Calculator produced different hash than CalculateMD5Reader")
	}
}

func TestStreamingMD5Calculator_MultipleWrites(t *testing.T) {
	calc := NewStreamingMD5Calculator()

	// Simulate streaming scenario with multiple writes
	writes := [][]byte{
		[]byte("The "),
		[]byte("quick "),
		[]byte("brown "),
		[]byte("fox "),
		[]byte("jumps "),
		[]byte("over "),
		[]byte("the "),
		[]byte("lazy "),
		[]byte("dog"),
	}

	for i, chunk := range writes {
		n, err := calc.Write(chunk)
		if err != nil {
			t.Fatalf("Write() %d error = %v", i, err)
		}
		if n != len(chunk) {
			t.Errorf("Write() %d returned %d, want %d", i, n, len(chunk))
		}
	}

	hash := calc.Sum()
	gotHex := hex.EncodeToString(hash)
	expectedMD5 := "9e107d9d372bb6826bd81d3542a419d6" // MD5 of combined text

	if gotHex != expectedMD5 {
		t.Errorf("Sum() after multiple writes = %s, want %s", gotHex, expectedMD5)
	}
}
