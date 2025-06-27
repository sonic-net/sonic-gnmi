package firmware

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestGetBinaryImageVersion(t *testing.T) {
	t.Run("Valid images", func(t *testing.T) {
		testValidImages(t)
	})
	t.Run("Invalid images", func(t *testing.T) {
		testInvalidImages(t)
	})
}

func testValidImages(t *testing.T) {
	tests := []struct {
		name         string
		setupFunc    func(string) error
		expectedVer  string
		expectedType string
		filename     string
	}{
		{
			name: "ONIE image with .bin extension",
			setupFunc: func(path string) error {
				return createTestOnieImage(path, "202311.1-abcd1234")
			},
			expectedVer:  "202311.1-abcd1234",
			expectedType: "onie",
			filename:     "test-image.bin",
		},
		{
			name: "Aboot image with .swi extension",
			setupFunc: func(path string) error {
				return createTestAbootImage(path, "202311.2-efgh5678")
			},
			expectedVer:  "202311.2-efgh5678",
			expectedType: "aboot",
			filename:     "test-image.swi",
		},
		{
			name: "ONIE image without extension",
			setupFunc: func(path string) error {
				return createTestOnieImage(path, "202311.3-ijkl9012")
			},
			expectedVer:  "202311.3-ijkl9012",
			expectedType: "onie",
			filename:     "test-image",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := t.TempDir()
			imagePath := filepath.Join(tempDir, tt.filename)

			if err := tt.setupFunc(imagePath); err != nil {
				t.Fatalf("Failed to setup test file: %v", err)
			}

			result, err := GetBinaryImageVersion(imagePath)
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if result.Version != tt.expectedVer {
				t.Errorf("Expected version %s, got %s", tt.expectedVer, result.Version)
			}

			expectedFullVersion := ImagePrefix + tt.expectedVer
			if result.FullVersion != expectedFullVersion {
				t.Errorf("Expected full version %s, got %s", expectedFullVersion, result.FullVersion)
			}

			if result.ImageType != tt.expectedType {
				t.Errorf("Expected image type %s, got %s", tt.expectedType, result.ImageType)
			}
		})
	}
}

func testInvalidImages(t *testing.T) {
	tests := []struct {
		name      string
		setupFunc func(string) error
	}{
		{
			name: "Invalid ONIE image (no version)",
			setupFunc: func(path string) error {
				return os.WriteFile(path, []byte("#!/bin/bash\necho 'Invalid image'\n"), 0644)
			},
		},
		{
			name: "Non-existent file",
			setupFunc: func(path string) error {
				return nil // Don't create the file
			},
		},
		{
			name: "Empty file",
			setupFunc: func(path string) error {
				return os.WriteFile(path, []byte{}, 0644)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := t.TempDir()
			imagePath := filepath.Join(tempDir, "test-image.bin")

			if tt.name != "Non-existent file" {
				if err := tt.setupFunc(imagePath); err != nil {
					t.Fatalf("Failed to setup test file: %v", err)
				}
			}

			result, err := GetBinaryImageVersion(imagePath)
			if err == nil {
				t.Errorf("Expected error but got none, result: %+v", result)
			}
		})
	}
}

func TestDetectImageType(t *testing.T) {
	tests := []struct {
		name         string
		filename     string
		content      []byte
		expectedType string
	}{
		{
			name:         "SWI extension",
			filename:     "test.swi",
			expectedType: "aboot",
		},
		{
			name:         "BIN extension",
			filename:     "test.bin",
			expectedType: "onie",
		},
		{
			name:         "ZIP content with unknown extension",
			filename:     "test.img",
			content:      []byte("PK\x03\x04"), // ZIP signature
			expectedType: "aboot",
		},
		{
			name:         "Non-ZIP content with unknown extension",
			filename:     "test.img",
			content:      []byte("#!/bin/bash\necho test"),
			expectedType: "onie",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := t.TempDir()
			filePath := filepath.Join(tempDir, tt.filename)

			if tt.content != nil {
				if err := os.WriteFile(filePath, tt.content, 0644); err != nil {
					t.Fatalf("Failed to create test file: %v", err)
				}
			}

			imageType, err := detectImageType(filePath)
			if err != nil && tt.content != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if imageType != tt.expectedType {
				t.Errorf("Expected image type %s, got %s", tt.expectedType, imageType)
			}
		})
	}
}

func TestExtractOnieVersionPure(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		expectedVer string
		expectError bool
	}{
		{
			name: "Valid version in script",
			content: `#!/bin/bash
# SONiC installer script
image_version="202311.1-build123"
echo "Installing SONiC"
`,
			expectedVer: "202311.1-build123",
			expectError: false,
		},
		{
			name: "Version with special characters",
			content: `#!/bin/bash
image_version="202311.1-RC1.build-456"
`,
			expectedVer: "202311.1-RC1.build-456",
			expectError: false,
		},
		{
			name: "No version found",
			content: `#!/bin/bash
echo "No version here"
`,
			expectError: true,
		},
		{
			name: "Malformed version line",
			content: `#!/bin/bash
image_version=202311.1-build123
`,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := t.TempDir()
			filePath := filepath.Join(tempDir, "test.bin")

			if err := os.WriteFile(filePath, []byte(tt.content), 0644); err != nil {
				t.Fatalf("Failed to create test file: %v", err)
			}

			version, err := extractOnieVersionPure(filePath)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if version != tt.expectedVer {
				t.Errorf("Expected version %s, got %s", tt.expectedVer, version)
			}
		})
	}
}

func TestExtractAbootVersion(t *testing.T) {
	tests := []struct {
		name        string
		version     string
		expectError bool
	}{
		{
			name:        "Valid Aboot image",
			version:     "202311.1-aboot123",
			expectError: false,
		},
		{
			name:        "Version with spaces",
			version:     "202311.2-build 456",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := t.TempDir()
			filePath := filepath.Join(tempDir, "test.swi")

			if err := createTestAbootImage(filePath, tt.version); err != nil {
				t.Fatalf("Failed to create test Aboot image: %v", err)
			}

			version, err := extractAbootVersion(filePath)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if version != tt.version {
				t.Errorf("Expected version %s, got %s", tt.version, version)
			}
		})
	}
}

// Helper function to create a test ONIE image file.
func createTestOnieImage(path, version string) error {
	content := `#!/bin/bash
# SONiC ONIE installer
set -e

image_version="` + version + `"
build_date="2023-11-15T10:30:00"

echo "Installing SONiC $image_version"
# More installer content would follow...
exit_marker
BINARY_DATA_FOLLOWS_HERE...
`
	return os.WriteFile(path, []byte(content), 0644)
}

// Helper function to create a test Aboot image file (ZIP format).
func createTestAbootImage(path, version string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	zipWriter := zip.NewWriter(file)
	defer zipWriter.Close()

	// Create .imagehash file with version
	imagehashWriter, err := zipWriter.Create(".imagehash")
	if err != nil {
		return err
	}
	if _, err := imagehashWriter.Write([]byte(version)); err != nil {
		return err
	}

	// Create a dummy boot file to make it look more realistic
	bootWriter, err := zipWriter.Create("boot0")
	if err != nil {
		return err
	}
	if _, err := bootWriter.Write([]byte("dummy boot content")); err != nil {
		return err
	}

	return nil
}
