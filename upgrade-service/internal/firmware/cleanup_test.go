package firmware

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/config"
)

func TestCleanupOldFirmware_NoFiles(t *testing.T) {
	// Create temp directory for testing
	tempDir := t.TempDir()

	// Mock config to use temp directory
	originalConfig := config.Global
	config.Global = &config.Config{
		RootFS: tempDir,
	}
	defer func() { config.Global = originalConfig }()

	result := CleanupOldFirmware()

	if result.FilesDeleted != 0 {
		t.Errorf("Expected 0 files deleted, got %d", result.FilesDeleted)
	}
	if len(result.DeletedFiles) != 0 {
		t.Errorf("Expected empty deleted files list, got %v", result.DeletedFiles)
	}
	if len(result.Errors) != 0 {
		t.Errorf("Expected no errors, got %v", result.Errors)
	}
	if result.SpaceFreedBytes != 0 {
		t.Errorf("Expected 0 bytes freed, got %d", result.SpaceFreedBytes)
	}
}

func TestCleanupOldFirmware_WithFiles(t *testing.T) {
	tempDir, hostDir, tmpDir := setupTestDirs(t)

	// Create test files
	createTestFile(t, filepath.Join(hostDir, "sonic.bin"), "binary content")
	createTestFile(t, filepath.Join(hostDir, "installer.swi"), "switch image")
	createTestFile(t, filepath.Join(tmpDir, "package.rpm"), "rpm package")
	keepFile := filepath.Join(hostDir, "keep.txt")
	createTestFile(t, keepFile, "should not be deleted")

	// Mock config and run cleanup
	result := runCleanupWithConfig(t, tempDir)

	// Verify results
	if result.FilesDeleted != 3 {
		t.Errorf("Expected 3 files deleted, got %d", result.FilesDeleted)
	}
	if len(result.DeletedFiles) != 3 {
		t.Errorf("Expected 3 deleted files, got %d: %v", len(result.DeletedFiles), result.DeletedFiles)
	}
	if len(result.Errors) != 0 {
		t.Errorf("Expected no errors, got %v", result.Errors)
	}
	if result.SpaceFreedBytes == 0 {
		t.Errorf("Expected some bytes freed, got 0")
	}

	// Verify .txt file is not deleted
	if _, err := os.Stat(keepFile); os.IsNotExist(err) {
		t.Errorf("File %s should not have been deleted", keepFile)
	}
}

func setupTestDirs(t *testing.T) (string, string, string) {
	tempDir := t.TempDir()
	hostDir := filepath.Join(tempDir, "host")
	tmpDir := filepath.Join(tempDir, "tmp")

	if err := os.MkdirAll(hostDir, 0755); err != nil {
		t.Fatalf("Failed to create host dir: %v", err)
	}
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		t.Fatalf("Failed to create tmp dir: %v", err)
	}

	return tempDir, hostDir, tmpDir
}

func createTestFile(t *testing.T, path, content string) {
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create test file %s: %v", path, err)
	}
}

func runCleanupWithConfig(t *testing.T, tempDir string) *CleanupResult {
	originalConfig := config.Global
	config.Global = &config.Config{RootFS: tempDir}
	defer func() { config.Global = originalConfig }()

	return CleanupOldFirmware()
}

func TestCleanupOldFirmware_FileDeleteError(t *testing.T) {
	// Create temp directory
	tempDir := t.TempDir()
	hostDir := filepath.Join(tempDir, "host")
	err := os.MkdirAll(hostDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create host dir: %v", err)
	}

	// Create a file and then make it read-only to simulate delete error
	testFile := filepath.Join(hostDir, "test.bin")
	err = os.WriteFile(testFile, []byte("test"), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Make file read-only
	err = os.Chmod(testFile, 0444)
	if err != nil {
		t.Fatalf("Failed to make file read-only: %v", err)
	}

	// Make parent directory read-only to prevent deletion
	err = os.Chmod(hostDir, 0555)
	if err != nil {
		t.Fatalf("Failed to make directory read-only: %v", err)
	}

	// Mock config
	originalConfig := config.Global
	config.Global = &config.Config{
		RootFS: tempDir,
	}
	defer func() {
		config.Global = originalConfig
		// Cleanup: restore permissions
		os.Chmod(hostDir, 0755)
		os.Chmod(testFile, 0644)
	}()

	result := CleanupOldFirmware()

	// Should have errors but not crash
	if len(result.Errors) == 0 {
		t.Errorf("Expected errors due to read-only directory, got none")
	}
	if result.FilesDeleted != 0 {
		t.Errorf("Expected 0 files deleted due to permissions, got %d", result.FilesDeleted)
	}
}

func TestDeleteFile(t *testing.T) {
	// Create temp file
	tempFile := filepath.Join(t.TempDir(), "test.bin")
	content := "test content"
	err := os.WriteFile(tempFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	result := &CleanupResult{
		DeletedFiles: make([]string, 0),
		Errors:       make([]string, 0),
	}

	err = deleteFile(tempFile, result)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if result.FilesDeleted != 1 {
		t.Errorf("Expected 1 file deleted, got %d", result.FilesDeleted)
	}
	if len(result.DeletedFiles) != 1 {
		t.Errorf("Expected 1 deleted file, got %d", len(result.DeletedFiles))
	}
	if result.DeletedFiles[0] != tempFile {
		t.Errorf("Expected deleted file %s, got %s", tempFile, result.DeletedFiles[0])
	}
	if result.SpaceFreedBytes != int64(len(content)) {
		t.Errorf("Expected %d bytes freed, got %d", len(content), result.SpaceFreedBytes)
	}

	// Verify file is actually deleted
	if _, err := os.Stat(tempFile); !os.IsNotExist(err) {
		t.Errorf("File should have been deleted")
	}
}

func TestCleanupOldFirmwareWithConfig(t *testing.T) {
	tempDir, hostDir, bootDir := setupCustomTestDirs(t)

	// Create test files
	imgFile1 := filepath.Join(hostDir, "firmware.img")
	imgFile2 := filepath.Join(bootDir, "kernel.img")
	binFile := filepath.Join(hostDir, "config.bin")
	shFile := filepath.Join(hostDir, "script.sh")

	createTestFile(t, imgFile1, "image content")
	createTestFile(t, imgFile2, "kernel image")
	createTestFile(t, binFile, "should not be deleted")
	createTestFile(t, shFile, "should not be deleted")

	// Custom config - only clean *.img files from /host and /boot
	customConfig := &CleanupConfig{
		Directories: []string{"/host", "/boot"},
		Extensions:  []string{"*.img"},
	}

	// Mock config and run cleanup with custom config
	originalConfig := config.Global
	config.Global = &config.Config{RootFS: tempDir}
	defer func() { config.Global = originalConfig }()

	result := CleanupOldFirmwareWithConfig(customConfig)

	// Verify results - should only delete .img files
	if result.FilesDeleted != 2 {
		t.Errorf("Expected 2 files deleted, got %d", result.FilesDeleted)
	}
	if len(result.DeletedFiles) != 2 {
		t.Errorf("Expected 2 deleted files, got %d: %v", len(result.DeletedFiles), result.DeletedFiles)
	}
	if len(result.Errors) != 0 {
		t.Errorf("Expected no errors, got %v", result.Errors)
	}

	// Verify files
	verifyFileDeleted(t, imgFile1)
	verifyFileDeleted(t, imgFile2)
	verifyFileExists(t, binFile)
	verifyFileExists(t, shFile)
}

func setupCustomTestDirs(t *testing.T) (string, string, string) {
	tempDir := t.TempDir()
	hostDir := filepath.Join(tempDir, "host")
	bootDir := filepath.Join(tempDir, "boot")

	if err := os.MkdirAll(hostDir, 0755); err != nil {
		t.Fatalf("Failed to create host dir: %v", err)
	}
	if err := os.MkdirAll(bootDir, 0755); err != nil {
		t.Fatalf("Failed to create boot dir: %v", err)
	}

	return tempDir, hostDir, bootDir
}

func verifyFileDeleted(t *testing.T, filePath string) {
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Errorf("File %s should have been deleted", filePath)
	}
}

func verifyFileExists(t *testing.T, filePath string) {
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Errorf("File %s should not have been deleted", filePath)
	}
}

func TestDefaultCleanupConfig(t *testing.T) {
	config := DefaultCleanupConfig()

	expectedDirs := []string{"/host", "/tmp"}
	expectedExts := []string{"*.bin", "*.swi", "*.rpm"}

	if len(config.Directories) != len(expectedDirs) {
		t.Errorf("Expected %d directories, got %d", len(expectedDirs), len(config.Directories))
	}

	for i, dir := range expectedDirs {
		if config.Directories[i] != dir {
			t.Errorf("Expected directory %s, got %s", dir, config.Directories[i])
		}
	}

	if len(config.Extensions) != len(expectedExts) {
		t.Errorf("Expected %d extensions, got %d", len(expectedExts), len(config.Extensions))
	}

	for i, ext := range expectedExts {
		if config.Extensions[i] != ext {
			t.Errorf("Expected extension %s, got %s", ext, config.Extensions[i])
		}
	}
}
