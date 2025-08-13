package checksum

import (
	"io/ioutil"
	"path/filepath"
	"testing"
)

func TestMD5Validator_ValidateFile(t *testing.T) {
	validator := NewMD5Validator()
	tempDir := t.TempDir()

	tests := []struct {
		name          string
		fileContent   string
		expectedMD5   string
		expectError   bool
		errorContains string
	}{
		{
			name:        "valid MD5 checksum",
			fileContent: "Hello, World!",
			expectedMD5: "65a8e27d8879283831b664bd8b7f0ad4",
			expectError: false,
		},
		{
			name:        "valid MD5 checksum uppercase",
			fileContent: "Hello, World!",
			expectedMD5: "65A8E27D8879283831B664BD8B7F0AD4",
			expectError: false,
		},
		{
			name:          "invalid MD5 checksum",
			fileContent:   "Hello, World!",
			expectedMD5:   "incorrect_checksum",
			expectError:   true,
			errorContains: "checksum mismatch",
		},
		{
			name:        "empty file",
			fileContent: "",
			expectedMD5: "d41d8cd98f00b204e9800998ecf8427e",
			expectError: false,
		},
		{
			name:        "large content",
			fileContent: string(make([]byte, 1024*1024)), // 1MB of null bytes
			expectedMD5: "b6d81b360a5672d80c27430f39153e2c",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test file
			testFile := filepath.Join(tempDir, tt.name+".txt")
			if err := ioutil.WriteFile(testFile, []byte(tt.fileContent), 0644); err != nil {
				t.Fatalf("Failed to create test file: %v", err)
			}

			// Validate checksum
			err := validator.ValidateFile(testFile, tt.expectedMD5)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
				} else if tt.errorContains != "" && !containsString(err.Error(), tt.errorContains) {
					t.Errorf("Expected error containing '%s', got: %v", tt.errorContains, err)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
			}
		})
	}
}

func TestMD5Validator_ValidateFile_Errors(t *testing.T) {
	validator := NewMD5Validator()

	tests := []struct {
		name          string
		filePath      string
		checksum      string
		errorContains string
	}{
		{
			name:          "empty file path",
			filePath:      "",
			checksum:      "some_checksum",
			errorContains: "file path cannot be empty",
		},
		{
			name:          "empty checksum",
			filePath:      "/some/path",
			checksum:      "",
			errorContains: "expected checksum cannot be empty",
		},
		{
			name:          "non-existent file",
			filePath:      "/non/existent/file.txt",
			checksum:      "some_checksum",
			errorContains: "failed to open file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.ValidateFile(tt.filePath, tt.checksum)
			if err == nil {
				t.Errorf("Expected error but got none")
			} else if !containsString(err.Error(), tt.errorContains) {
				t.Errorf("Expected error containing '%s', got: %v", tt.errorContains, err)
			}
		})
	}
}

func TestMD5Validator_CalculateChecksum(t *testing.T) {
	validator := NewMD5Validator()
	tempDir := t.TempDir()

	tests := []struct {
		name        string
		fileContent string
		expectedMD5 string
	}{
		{
			name:        "simple text",
			fileContent: "Hello, World!",
			expectedMD5: "65a8e27d8879283831b664bd8b7f0ad4",
		},
		{
			name:        "empty file",
			fileContent: "",
			expectedMD5: "d41d8cd98f00b204e9800998ecf8427e",
		},
		{
			name:        "binary content",
			fileContent: string([]byte{0x00, 0x01, 0x02, 0x03, 0x04}),
			expectedMD5: "d05374dc381d9b52806446a71c8e79b1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test file
			testFile := filepath.Join(tempDir, tt.name+".txt")
			if err := ioutil.WriteFile(testFile, []byte(tt.fileContent), 0644); err != nil {
				t.Fatalf("Failed to create test file: %v", err)
			}

			// Calculate checksum
			checksum, err := validator.CalculateChecksum(testFile)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if checksum != tt.expectedMD5 {
				t.Errorf("Expected checksum %s, got %s", tt.expectedMD5, checksum)
			}
		})
	}
}

func TestMD5Validator_CalculateChecksum_Errors(t *testing.T) {
	validator := NewMD5Validator()

	// Test non-existent file
	_, err := validator.CalculateChecksum("/non/existent/file.txt")
	if err == nil {
		t.Errorf("Expected error for non-existent file")
	} else if !containsString(err.Error(), "failed to open file") {
		t.Errorf("Expected 'failed to open file' error, got: %v", err)
	}
}

func containsString(s, substr string) bool {
	if len(s) < len(substr) {
		return false
	}
	if s == substr {
		return true
	}
	if len(s) > 0 && containsString(s[1:], substr) {
		return true
	}
	if len(substr) > 0 && s[0] == substr[0] && containsString(s[1:], substr[1:]) {
		return true
	}
	return false
}
