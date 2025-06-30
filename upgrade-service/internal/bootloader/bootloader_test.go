package bootloader

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/config"
)

// setupTestConfig initializes config for testing and returns cleanup function.
func setupTestConfig(t *testing.T) (tmpDir string, cleanup func()) {
	// Save original global config
	var originalRootFS string
	if config.Global != nil {
		originalRootFS = config.Global.RootFS
	}

	// Create temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "bootloader_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	// Initialize config if not already done
	if config.Global == nil {
		config.Global = &config.Config{}
	}
	config.Global.RootFS = tmpDir

	cleanup = func() {
		os.RemoveAll(tmpDir)
		if config.Global != nil {
			config.Global.RootFS = originalRootFS
		}
	}

	return tmpDir, cleanup
}

func TestGetBootloader(t *testing.T) {
	tmpDir, cleanup := setupTestConfig(t)
	defer cleanup()

	// Test case 1: No bootloader detected
	t.Run("NoBootloaderDetected", func(t *testing.T) {
		_, err := GetBootloader()
		if err == nil {
			t.Error("Expected error when no bootloader is detected")
		}
		if err.Error() != "bootloader could not be detected" {
			t.Errorf("Expected 'bootloader could not be detected', got: %v", err)
		}
	})

	// Test case 2: GRUB bootloader detected
	t.Run("GRUBDetected", func(t *testing.T) {
		// Create GRUB config file
		grubDir := filepath.Join(tmpDir, "host", "grub")
		if err := os.MkdirAll(grubDir, 0755); err != nil {
			t.Fatalf("Failed to create grub dir: %v", err)
		}
		grubConfig := filepath.Join(grubDir, "grub.cfg")
		if err := os.WriteFile(grubConfig, []byte("# GRUB config"), 0644); err != nil {
			t.Fatalf("Failed to create grub config: %v", err)
		}

		bootloader, err := GetBootloader()
		if err != nil {
			t.Fatalf("Expected GRUB to be detected, got error: %v", err)
		}

		if _, ok := bootloader.(*GrubBootloader); !ok {
			t.Errorf("Expected GrubBootloader, got: %T", bootloader)
		}

		// Clean up
		os.RemoveAll(grubDir)
	})

	// Test case 3: Aboot bootloader detected
	t.Run("AbootDetected", func(t *testing.T) {
		// Create Aboot indicator
		hostDir := filepath.Join(tmpDir, "host")
		if err := os.MkdirAll(hostDir, 0755); err != nil {
			t.Fatalf("Failed to create host dir: %v", err)
		}
		bootConfig := filepath.Join(hostDir, "boot-config")
		if err := os.WriteFile(bootConfig, []byte("# Aboot config"), 0644); err != nil {
			t.Fatalf("Failed to create boot-config: %v", err)
		}

		bootloader, err := GetBootloader()
		if err != nil {
			t.Fatalf("Expected Aboot to be detected, got error: %v", err)
		}

		if _, ok := bootloader.(*AbootBootloader); !ok {
			t.Errorf("Expected AbootBootloader, got: %T", bootloader)
		}

		// Clean up
		os.RemoveAll(hostDir)
	})
}

func TestListInstalledImages(t *testing.T) {
	tmpDir, cleanup := setupTestConfig(t)
	defer cleanup()

	// Test case: No bootloader available
	t.Run("NoBootloader", func(t *testing.T) {
		_, err := ListInstalledImages()
		if err == nil {
			t.Error("Expected error when no bootloader is available")
		}
	})

	// Test case: GRUB bootloader with images
	t.Run("GRUBWithImages", func(t *testing.T) {
		// Setup GRUB environment
		grubDir := filepath.Join(tmpDir, "host", "grub")
		if err := os.MkdirAll(grubDir, 0755); err != nil {
			t.Fatalf("Failed to create grub dir: %v", err)
		}

		grubConfig := `menuentry 'SONiC-OS-202311.1-12345' {
    linux /image-202311.1-12345/boot/vmlinuz
}
menuentry 'SONiC-OS-202311.2-67890' {
    linux /image-202311.2-67890/boot/vmlinuz
}`
		grubConfigPath := filepath.Join(grubDir, "grub.cfg")
		if err := os.WriteFile(grubConfigPath, []byte(grubConfig), 0644); err != nil {
			t.Fatalf("Failed to create grub config: %v", err)
		}

		// Create /proc/cmdline
		procDir := filepath.Join(tmpDir, "proc")
		if err := os.MkdirAll(procDir, 0755); err != nil {
			t.Fatalf("Failed to create proc dir: %v", err)
		}
		cmdlinePath := filepath.Join(procDir, "cmdline")
		cmdlineContent := "root=/dev/sda1 SONiC-OS-202311.1-12345"
		if err := os.WriteFile(cmdlinePath, []byte(cmdlineContent), 0644); err != nil {
			t.Fatalf("Failed to create cmdline: %v", err)
		}

		info, err := ListInstalledImages()
		if err != nil {
			t.Fatalf("Failed to list images: %v", err)
		}

		if len(info.Available) != 2 {
			t.Errorf("Expected 2 available images, got %d", len(info.Available))
		}

		expectedImages := []string{"SONiC-OS-202311.1-12345", "SONiC-OS-202311.2-67890"}
		for _, expected := range expectedImages {
			found := false
			for _, actual := range info.Available {
				if actual == expected {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("Expected image %s not found in available images", expected)
			}
		}

		if info.Current != "SONiC-OS-202311.1-12345" {
			t.Errorf("Expected current image 'SONiC-OS-202311.1-12345', got: %s", info.Current)
		}
	})
}

func TestImageListInfo(t *testing.T) {
	info := &ImageListInfo{
		Current:   "SONiC-OS-current-123",
		Next:      "SONiC-OS-next-456",
		Available: []string{"SONiC-OS-current-123", "SONiC-OS-next-456", "SONiC-OS-old-789"},
	}

	if info.Current != "SONiC-OS-current-123" {
		t.Errorf("Expected current 'SONiC-OS-current-123', got: %s", info.Current)
	}

	if info.Next != "SONiC-OS-next-456" {
		t.Errorf("Expected next 'SONiC-OS-next-456', got: %s", info.Next)
	}

	if len(info.Available) != 3 {
		t.Errorf("Expected 3 available images, got: %d", len(info.Available))
	}
}
