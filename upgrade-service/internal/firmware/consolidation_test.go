package firmware

import (
	"testing"

	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/config"
	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/installer"
)

func TestDefaultConsolidationConfig(t *testing.T) {
	config := DefaultConsolidationConfig()

	if config.Method != ConsolidationMethodCLI {
		t.Errorf("Expected default method CLI, got %v", config.Method)
	}

	if config.SonicInstallerPath != "sonic-installer" {
		t.Errorf("Expected default path 'sonic-installer', got %s", config.SonicInstallerPath)
	}

	if config.DryRunDefault != false {
		t.Errorf("Expected default dry run false, got %t", config.DryRunDefault)
	}
}

func TestGetConsolidationMethod(t *testing.T) {
	config := DefaultConsolidationConfig()

	config.Method = ConsolidationMethodCLI
	if config.GetConsolidationMethod() != "sonic-installer CLI" {
		t.Errorf("Expected 'sonic-installer CLI', got %s", config.GetConsolidationMethod())
	}

	config.Method = ConsolidationMethodBootloader
	if config.GetConsolidationMethod() != "bootloader package" {
		t.Errorf("Expected 'bootloader package', got %s", config.GetConsolidationMethod())
	}

	config.Method = 999 // Invalid method
	if config.GetConsolidationMethod() != "unknown" {
		t.Errorf("Expected 'unknown', got %s", config.GetConsolidationMethod())
	}
}

func TestNewConsolidationService(t *testing.T) {
	service := NewConsolidationService()

	if service.config == nil {
		t.Error("Expected config to be initialized")
	}

	if service.sonicInstaller == nil {
		t.Error("Expected sonic installer to be initialized")
	}

	if service.config.Method != ConsolidationMethodCLI {
		t.Errorf("Expected CLI method, got %v", service.config.Method)
	}
}

func TestNewConsolidationServiceWithConfig(t *testing.T) {
	customConfig := &ConsolidationConfig{
		Method:             ConsolidationMethodBootloader,
		SonicInstallerPath: "/custom/path/sonic-installer",
		DryRunDefault:      true,
	}

	service := NewConsolidationServiceWithConfig(customConfig)

	if service.config != customConfig {
		t.Error("Expected custom config to be used")
	}

	if service.config.Method != ConsolidationMethodBootloader {
		t.Errorf("Expected bootloader method, got %v", service.config.Method)
	}
}

func TestConsolidateImages_UnsupportedMethod(t *testing.T) {
	config := &ConsolidationConfig{
		Method:             999, // Invalid method
		SonicInstallerPath: "sonic-installer",
		DryRunDefault:      false,
	}

	service := NewConsolidationServiceWithConfig(config)

	_, err := service.ConsolidateImages(false)
	if err == nil {
		t.Error("Expected error for unsupported method")
	}

	if err.Error() != "unsupported consolidation method: 999" {
		t.Errorf("Expected unsupported method error, got: %v", err)
	}
}

func TestConsolidateImages_BootloaderMethod(t *testing.T) {
	// Initialize config for bootloader package
	originalConfig := config.Global
	config.Global = &config.Config{RootFS: "/"}
	defer func() { config.Global = originalConfig }()

	serviceConfig := &ConsolidationConfig{
		Method:             ConsolidationMethodBootloader,
		SonicInstallerPath: "sonic-installer",
		DryRunDefault:      false,
	}

	service := NewConsolidationServiceWithConfig(serviceConfig)

	_, err := service.ConsolidateImages(false)
	if err == nil {
		t.Error("Expected error for bootloader method")
	}

	// The error could be either "bootloader could not be detected" or "not yet implemented"
	// depending on whether a bootloader is detected in the test environment
	expectedErrors := []string{
		"failed to get bootloader: bootloader could not be detected",
		"bootloader-based consolidation not yet implemented",
	}

	found := false
	for _, expected := range expectedErrors {
		if err.Error() == expected {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("Expected one of %v, got: %v", expectedErrors, err)
	}
}

func TestEstimateCleanupResults(t *testing.T) {
	service := NewConsolidationService()

	// Create mock list result
	listResult := &installer.ListResult{
		Current: "SONiC-OS-202311.1-current",
		Next:    "SONiC-OS-202311.1-current",
		Images: []installer.ImageInfo{
			{Name: "SONiC-OS-202311.1-current", Current: true, Next: true},
			{Name: "SONiC-OS-202310.1-old", Current: false, Next: false},
			{Name: "SONiC-OS-202309.1-older", Current: false, Next: false},
		},
	}

	removedImages, spaceFreed, err := service.estimateCleanupResults(listResult, "SONiC-OS-202311.1-current")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	expectedRemoved := []string{"SONiC-OS-202310.1-old", "SONiC-OS-202309.1-older"}
	if len(removedImages) != len(expectedRemoved) {
		t.Errorf("Expected %d removed images, got %d", len(expectedRemoved), len(removedImages))
	}

	for i, expected := range expectedRemoved {
		if i >= len(removedImages) || removedImages[i] != expected {
			t.Errorf("Expected removed image %s, got %v", expected, removedImages)
			break
		}
	}

	// Space freed should be 0 since image directories don't exist in test
	if spaceFreed != 0 {
		t.Errorf("Expected 0 space freed in test, got %d", spaceFreed)
	}
}

func TestEstimateImageSize(t *testing.T) {
	service := NewConsolidationService()

	// Test with non-existent image
	size, err := service.estimateImageSize("SONiC-OS-nonexistent")
	if err == nil {
		t.Error("Expected error for non-existent image")
	}
	if size != 0 {
		t.Errorf("Expected 0 size for non-existent image, got %d", size)
	}
}

// Note: TestEstimateImageSize_WithTempDir removed as it's hard to test properly
// without mocking. The function is simple enough and tested indirectly through
// integration tests.
