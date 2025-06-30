package server

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/config"
	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/firmware"
	pb "github.com/sonic-net/sonic-gnmi/upgrade-service/proto"
)

func TestNewFirmwareManagementServer(t *testing.T) {
	server := NewFirmwareManagementServer()
	if server == nil {
		t.Error("Expected non-nil server")
	}
	if _, ok := server.(*firmwareManagementServer); !ok {
		t.Error("Expected server to be of type *firmwareManagementServer")
	}
}

func TestFirmwareManagementServer_CleanupOldFirmware(t *testing.T) {
	// Set up config to avoid nil pointer
	tempDir := t.TempDir()
	originalConfig := config.Global
	config.Global = &config.Config{RootFS: tempDir}
	defer func() { config.Global = originalConfig }()

	// Create host directory
	hostDir := filepath.Join(tempDir, "host")
	if err := os.MkdirAll(hostDir, 0755); err != nil {
		t.Fatalf("Failed to create host directory: %v", err)
	}

	server := NewFirmwareManagementServer()
	ctx := context.Background()
	req := &pb.CleanupOldFirmwareRequest{}

	resp, err := server.CleanupOldFirmware(ctx, req)
	if err != nil {
		t.Errorf("CleanupOldFirmware returned unexpected error: %v", err)
	}

	if resp == nil {
		t.Error("Expected non-nil response")
	}

	// The actual cleanup is done by the firmware package which is already well tested
	// Here we just verify the server properly returns the response
	if resp.FilesDeleted < 0 {
		t.Error("Expected non-negative FilesDeleted")
	}
	if resp.SpaceFreedBytes < 0 {
		t.Error("Expected non-negative SpaceFreedBytes")
	}
	if resp.DeletedFiles == nil {
		t.Error("Expected non-nil DeletedFiles slice")
	}
	if resp.Errors == nil {
		t.Error("Expected non-nil Errors slice")
	}
}

func TestFirmwareManagementServer_ListFirmwareImages(t *testing.T) {
	t.Run("DefaultDirectories", testListFirmwareImagesDefault)
	t.Run("CustomDirectories", testListFirmwareImagesCustom)
	t.Run("VersionPatternFilter", testListFirmwareImagesWithPattern)
	t.Run("InvalidRegexPattern", testListFirmwareImagesInvalidPattern)
	t.Run("NonexistentDirectory", testListFirmwareImagesNonexistent)
}

func testListFirmwareImagesDefault(t *testing.T) {
	setupListFirmwareTest(t)

	server := NewFirmwareManagementServer()
	ctx := context.Background()
	req := &pb.ListFirmwareImagesRequest{}

	resp, err := server.ListFirmwareImages(ctx, req)
	if err != nil {
		t.Fatalf("ListFirmwareImages failed: %v", err)
	}

	if resp == nil {
		t.Fatal("Expected non-nil response")
	}

	if len(resp.Images) != 2 {
		t.Errorf("Expected 2 images, got %d", len(resp.Images))
	}

	// Verify response structure
	for _, img := range resp.Images {
		validateFirmwareImageInfo(t, img)
	}
}

func testListFirmwareImagesCustom(t *testing.T) {
	// Set up config to avoid nil pointer
	if config.Global == nil {
		config.Global = &config.Config{
			RootFS:          "/",
			Addr:            ":50051",
			ShutdownTimeout: 10 * time.Second,
			TLSEnabled:      false,
		}
	}

	// Create custom test directories
	tempDir1 := t.TempDir()
	tempDir2 := t.TempDir()

	// Create test images in custom directories
	createTestImageInDir(t, tempDir1, "custom1.bin", "202311.10-custom1")
	createTestImageInDir(t, tempDir2, "custom2.swi", "202311.11-custom2")

	server := NewFirmwareManagementServer()
	ctx := context.Background()
	req := &pb.ListFirmwareImagesRequest{
		SearchDirectories: []string{tempDir1, tempDir2},
	}

	resp, err := server.ListFirmwareImages(ctx, req)
	if err != nil {
		t.Fatalf("ListFirmwareImages failed: %v", err)
	}

	if len(resp.Images) != 2 {
		t.Errorf("Expected 2 images from custom directories, got %d", len(resp.Images))
	}

	// Verify we got images from custom directories
	foundVersions := make(map[string]bool)
	for _, img := range resp.Images {
		foundVersions[img.Version] = true
	}

	if !foundVersions["202311.10-custom1"] {
		t.Error("Missing image from first custom directory")
	}
	if !foundVersions["202311.11-custom2"] {
		t.Error("Missing image from second custom directory")
	}
}

func testListFirmwareImagesWithPattern(t *testing.T) {
	setupListFirmwareTest(t)

	server := NewFirmwareManagementServer()
	ctx := context.Background()

	// Test cases for regex patterns
	testCases := []struct {
		pattern       string
		expectedCount int
		description   string
	}{
		{"202311\\.4.*", 1, "Specific version pattern"},
		{".*-test.*", 2, "Contains test"},
		{"^202311\\.5.*", 0, "No matches"},
		{".*", 2, "Match all"},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			req := &pb.ListFirmwareImagesRequest{
				VersionPattern: tc.pattern,
			}

			resp, err := server.ListFirmwareImages(ctx, req)
			if err != nil {
				t.Fatalf("ListFirmwareImages failed: %v", err)
			}

			if len(resp.Images) != tc.expectedCount {
				t.Errorf("Pattern %s: expected %d images, got %d",
					tc.pattern, tc.expectedCount, len(resp.Images))
			}
		})
	}
}

func testListFirmwareImagesInvalidPattern(t *testing.T) {
	// Set up minimal config
	if config.Global == nil {
		config.Global = &config.Config{
			RootFS:          "/",
			Addr:            ":50051",
			ShutdownTimeout: 10 * time.Second,
			TLSEnabled:      false,
		}
	}

	server := NewFirmwareManagementServer()
	ctx := context.Background()
	req := &pb.ListFirmwareImagesRequest{
		VersionPattern: "[invalid(regex", // Invalid regex
	}

	resp, err := server.ListFirmwareImages(ctx, req)
	if err == nil {
		t.Error("Expected error for invalid regex pattern")
	}
	if resp != nil {
		t.Error("Expected nil response for invalid regex")
	}
}

func testListFirmwareImagesNonexistent(t *testing.T) {
	// Set up minimal config
	if config.Global == nil {
		config.Global = &config.Config{
			RootFS:          "/",
			Addr:            ":50051",
			ShutdownTimeout: 10 * time.Second,
			TLSEnabled:      false,
		}
	}

	server := NewFirmwareManagementServer()
	ctx := context.Background()
	req := &pb.ListFirmwareImagesRequest{
		SearchDirectories: []string{"/this/does/not/exist"},
	}

	resp, err := server.ListFirmwareImages(ctx, req)
	if err != nil {
		t.Fatalf("ListFirmwareImages should not fail for nonexistent directory: %v", err)
	}

	if len(resp.Images) != 0 {
		t.Errorf("Expected 0 images from nonexistent directory, got %d", len(resp.Images))
	}

	// Should have an error in the errors field
	if len(resp.Errors) == 0 {
		t.Error("Expected error message for nonexistent directory")
	}
}

// Helper functions for ListFirmwareImages tests

func setupListFirmwareTest(t *testing.T) {
	// Save original values
	originalConfig := config.Global
	originalDirs := firmware.DefaultSearchDirectories

	// Set up cleanup
	t.Cleanup(func() {
		config.Global = originalConfig
		firmware.DefaultSearchDirectories = originalDirs
	})

	// Create a test root directory
	rootDir := t.TempDir()

	// Set up config with test root
	config.Global = &config.Config{
		RootFS:          rootDir,
		Addr:            ":50051",
		ShutdownTimeout: 10 * time.Second,
		TLSEnabled:      false,
	}

	// Create subdirectory for firmware images
	firmwareDir := filepath.Join(rootDir, "tmp")
	if err := os.MkdirAll(firmwareDir, 0755); err != nil {
		t.Fatalf("Failed to create firmware directory: %v", err)
	}

	// Set firmware search directory to /tmp (which will be transformed to rootDir/tmp)
	firmware.DefaultSearchDirectories = []string{"/tmp"}

	// Create test images in the firmware directory
	createTestImageInDir(t, firmwareDir, "image1.bin", "202311.3-test123")
	createTestImageInDir(t, firmwareDir, "image2.swi", "202311.4-test456")
}

func createTestImageInDir(t *testing.T, dir, filename, version string) {
	path := filepath.Join(dir, filename)
	var err error

	if filepath.Ext(filename) == ".swi" {
		err = createTestAbootImage(path, version)
	} else {
		err = createTestOnieImage(path, version)
	}

	if err != nil {
		t.Fatalf("Failed to create test image %s: %v", filename, err)
	}
}

func validateFirmwareImageInfo(t *testing.T, img *pb.FirmwareImageInfo) {
	if img.FilePath == "" {
		t.Error("FirmwareImageInfo missing file path")
	}
	if img.Version == "" {
		t.Error("FirmwareImageInfo missing version")
	}
	if img.FullVersion == "" {
		t.Error("FirmwareImageInfo missing full version")
	}
	if img.ImageType == "" {
		t.Error("FirmwareImageInfo missing image type")
	}
	if img.FileSizeBytes <= 0 {
		t.Error("FirmwareImageInfo has invalid file size")
	}
}

// Helper functions copied from version_test.go for creating test images.
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

func TestFirmwareManagementServer_ConsolidateImages(t *testing.T) {
	// Set up config to avoid nil pointer
	tempDir := t.TempDir()
	originalConfig := config.Global
	config.Global = &config.Config{RootFS: tempDir}
	defer func() { config.Global = originalConfig }()

	// Create host directory for space estimation
	hostDir := filepath.Join(tempDir, "host")
	if err := os.MkdirAll(hostDir, 0755); err != nil {
		t.Fatalf("Failed to create host directory: %v", err)
	}

	server := NewFirmwareManagementServer()
	ctx := context.Background()

	// Test dry run
	t.Run("DryRun", func(t *testing.T) {
		req := &pb.ConsolidateImagesRequest{
			DryRun: true,
		}

		// This will fail because sonic-installer is not available,
		// but we're testing the server layer integration
		resp, err := server.ConsolidateImages(ctx, req)
		if err == nil {
			// If somehow sonic-installer is available in test environment
			if resp == nil {
				t.Error("Expected non-nil response for successful consolidation")
			}
			if resp.Executed {
				t.Error("Expected dry run to have Executed=false")
			}
		} else {
			// Expected case: sonic-installer not available
			if resp != nil {
				t.Error("Expected nil response when consolidation fails")
			}
		}
	})

	// Test non-dry run (will likely fail due to no sonic-installer)
	t.Run("Execute", func(t *testing.T) {
		req := &pb.ConsolidateImagesRequest{
			DryRun: false,
		}

		// This will fail because sonic-installer is not available
		resp, err := server.ConsolidateImages(ctx, req)
		if err == nil {
			// If somehow sonic-installer is available in test environment
			if resp == nil {
				t.Error("Expected non-nil response for successful consolidation")
			}
			if !resp.Executed {
				t.Error("Expected non-dry run to have Executed=true")
			}
		} else {
			// Expected case: sonic-installer not available
			if resp != nil {
				t.Error("Expected nil response when consolidation fails")
			}
		}
	})
}

func TestFirmwareManagementServer_ListImages(t *testing.T) {
	// Set up config to avoid nil pointer
	tempDir := t.TempDir()
	originalConfig := config.Global
	config.Global = &config.Config{RootFS: tempDir}
	defer func() { config.Global = originalConfig }()

	server := NewFirmwareManagementServer()
	ctx := context.Background()

	t.Run("ListImages", func(t *testing.T) {
		req := &pb.ListImagesRequest{}

		// This will fail because sonic-installer is not available in test environment,
		// but we're testing the server layer integration
		resp, err := server.ListImages(ctx, req)
		if err != nil {
			// Expected case: sonic-installer not available
			if resp != nil {
				t.Error("Expected nil response when list fails")
			}
			return
		}

		// If somehow sonic-installer is available in test environment
		if resp == nil {
			t.Error("Expected non-nil response for successful list")
		}
		if resp.Images == nil {
			t.Error("Expected non-nil images slice")
		}
		if resp.Warnings == nil {
			t.Error("Expected non-nil warnings slice")
		}
	})
}
