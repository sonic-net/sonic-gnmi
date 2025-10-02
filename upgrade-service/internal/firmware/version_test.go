package firmware

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/config"
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

// setupTestConfig initializes a test configuration.
func setupTestConfig(t *testing.T) {
	if config.Global == nil {
		config.Global = &config.Config{
			RootFS:          "/", // Use root for tests
			Addr:            ":50051",
			ShutdownTimeout: 10 * time.Second,
			TLSEnabled:      false,
		}
	}
}

func TestFindImagesByVersion(t *testing.T) {
	t.Run("ExactVersionMatch", testFindImagesByVersionExactMatch)
	t.Run("SingleMatch", testFindImagesByVersionSingleMatch)
	t.Run("FullVersionPrefix", testFindImagesByVersionWithPrefix)
	t.Run("NoMatches", testFindImagesByVersionNoMatches)
}

func testFindImagesByVersionExactMatch(t *testing.T) {
	setupSearchTest(t)

	results, err := FindImagesByVersion("202311.1-test123")
	if err != nil {
		t.Fatalf("FindImagesByVersion failed: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Expected 2 matches, got %d", len(results))
	}

	for _, result := range results {
		validateSearchResult(t, result, "202311.1-test123")
	}
}

func testFindImagesByVersionSingleMatch(t *testing.T) {
	setupSearchTest(t)

	results, err := FindImagesByVersion("202311.2-test456")
	if err != nil {
		t.Fatalf("FindImagesByVersion failed: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Expected 1 match, got %d", len(results))
	}

	if len(results) > 0 {
		validateSearchResult(t, results[0], "202311.2-test456")
	}
}

func testFindImagesByVersionWithPrefix(t *testing.T) {
	setupSearchTest(t)

	results, err := FindImagesByVersion("SONiC-OS-202311.3-test789")
	if err != nil {
		t.Fatalf("FindImagesByVersion failed: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Expected 1 match, got %d", len(results))
	}

	if len(results) > 0 {
		validateSearchResult(t, results[0], "SONiC-OS-202311.3-test789")
	}
}

func testFindImagesByVersionNoMatches(t *testing.T) {
	setupSearchTest(t)

	results, err := FindImagesByVersion("202311.99-nonexistent")
	if err != nil {
		t.Fatalf("FindImagesByVersion failed: %v", err)
	}

	if len(results) != 0 {
		t.Errorf("Expected 0 matches, got %d", len(results))
	}
}

func setupSearchTest(t *testing.T) string {
	setupTestConfig(t)

	originalDirs := DefaultSearchDirectories
	t.Cleanup(func() { DefaultSearchDirectories = originalDirs })

	tempDir := t.TempDir()
	DefaultSearchDirectories = []string{tempDir}

	// Create test images
	createTestOnieImage(filepath.Join(tempDir, "image1.bin"), "202311.1-test123")
	createTestAbootImage(filepath.Join(tempDir, "image2.swi"), "202311.2-test456")
	createTestOnieImage(filepath.Join(tempDir, "image3.bin"), "202311.1-test123")
	createTestAbootImage(filepath.Join(tempDir, "image4.swi"), "202311.3-test789")

	return tempDir
}

func validateSearchResult(t *testing.T, result *ImageSearchResult, targetVersion string) {
	if result.VersionInfo == nil {
		t.Error("Result missing version info")
		return
	}
	if result.FilePath == "" {
		t.Error("Result missing file path")
	}
	if result.FileSize == 0 {
		t.Error("Result has zero file size")
	}

	// Check version match
	if result.VersionInfo.Version != targetVersion &&
		result.VersionInfo.FullVersion != targetVersion {
		t.Errorf("Version mismatch: expected %s, got %s or %s",
			targetVersion, result.VersionInfo.Version, result.VersionInfo.FullVersion)
	}
}

func TestFindAllImages(t *testing.T) {
	t.Run("FindsAllValidImages", testFindAllImagesValid)
	t.Run("IgnoresNonImageFiles", testFindAllImagesIgnoresNonImages)
}

func testFindAllImagesValid(t *testing.T) {
	setupFindAllTest(t)

	results, err := FindAllImages()
	if err != nil {
		t.Fatalf("FindAllImages failed: %v", err)
	}

	if len(results) != 3 {
		t.Errorf("Expected 3 images, got %d", len(results))
	}

	// Verify all results have required fields
	for i, result := range results {
		validateAllImagesResult(t, result, i)
	}
}

func testFindAllImagesIgnoresNonImages(t *testing.T) {
	tempDir := setupFindAllTest(t)

	// Create some non-image files (should be ignored)
	nonImageFiles := []string{"readme.txt", "config.json", "somefile.rpm"}
	for _, filename := range nonImageFiles {
		path := filepath.Join(tempDir, filename)
		if err := os.WriteFile(path, []byte("not an image"), 0644); err != nil {
			t.Fatalf("Failed to create non-image file %s: %v", filename, err)
		}
	}

	results, err := FindAllImages()
	if err != nil {
		t.Fatalf("FindAllImages failed: %v", err)
	}

	// Should still only find the 3 image files, not the other files
	if len(results) != 3 {
		t.Errorf("Expected 3 images (ignoring non-image files), got %d", len(results))
	}
}

func setupFindAllTest(t *testing.T) string {
	setupTestConfig(t)

	originalDirs := DefaultSearchDirectories
	t.Cleanup(func() { DefaultSearchDirectories = originalDirs })

	tempDir := t.TempDir()
	DefaultSearchDirectories = []string{tempDir}

	// Create test images
	createTestOnieImage(filepath.Join(tempDir, "image1.bin"), "202311.1-test123")
	createTestAbootImage(filepath.Join(tempDir, "image2.swi"), "202311.2-test456")
	createTestOnieImage(filepath.Join(tempDir, "image3.bin"), "202311.3-test789")

	return tempDir
}

func validateAllImagesResult(t *testing.T, result *ImageSearchResult, index int) {
	if result.VersionInfo == nil {
		t.Errorf("Result %d missing version info", index)
		return
	}
	if result.FilePath == "" {
		t.Errorf("Result %d missing file path", index)
	}
	if result.FileSize == 0 {
		t.Errorf("Result %d has zero file size", index)
	}
	if result.VersionInfo.Version == "" {
		t.Errorf("Result %d has empty version", index)
	}
	if result.VersionInfo.ImageType == "" {
		t.Errorf("Result %d has empty image type", index)
	}
}

func TestFindImagesInNonexistentDirectory(t *testing.T) {
	// Initialize config for testing
	setupTestConfig(t)

	// Save original config
	originalDirs := DefaultSearchDirectories
	defer func() { DefaultSearchDirectories = originalDirs }()

	// Use a directory that doesn't exist
	DefaultSearchDirectories = []string{"/this/directory/does/not/exist"}

	results, err := FindImagesByVersion("any-version")
	if err != nil {
		t.Errorf("FindImagesByVersion should not fail for nonexistent directories: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("Expected 0 results for nonexistent directory, got %d", len(results))
	}

	allResults, err := FindAllImages()
	if err != nil {
		t.Errorf("FindAllImages should not fail for nonexistent directories: %v", err)
	}
	if len(allResults) != 0 {
		t.Errorf("Expected 0 results for nonexistent directory, got %d", len(allResults))
	}
}

func TestSearchWithMultipleDirectories(t *testing.T) {
	// Initialize config for testing
	setupTestConfig(t)

	// Save original config
	originalDirs := DefaultSearchDirectories
	defer func() { DefaultSearchDirectories = originalDirs }()

	tempDir1 := t.TempDir()
	tempDir2 := t.TempDir()
	DefaultSearchDirectories = []string{tempDir1, tempDir2}

	// Create images in both directories
	image1Path := filepath.Join(tempDir1, "image1.bin")
	if err := createTestOnieImage(image1Path, "202311.1-dir1"); err != nil {
		t.Fatalf("Failed to create image in dir1: %v", err)
	}

	image2Path := filepath.Join(tempDir2, "image2.swi")
	if err := createTestAbootImage(image2Path, "202311.2-dir2"); err != nil {
		t.Fatalf("Failed to create image in dir2: %v", err)
	}

	results, err := FindAllImages()
	if err != nil {
		t.Fatalf("FindAllImages failed: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Expected 2 images from multiple directories, got %d", len(results))
	}

	// Verify we got images from both directories
	foundPaths := make(map[string]bool)
	for _, result := range results {
		foundPaths[result.FilePath] = true
	}

	if !foundPaths[image1Path] {
		t.Error("Image from first directory not found")
	}
	if !foundPaths[image2Path] {
		t.Error("Image from second directory not found")
	}
}
