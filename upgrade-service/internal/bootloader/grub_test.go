package bootloader

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGrubBootloader_Detect(t *testing.T) {
	tmpDir, cleanup := setupTestConfig(t)
	defer cleanup()
	grub := NewGrubBootloader()

	// Test case 1: No GRUB config exists
	t.Run("NoGRUBConfig", func(t *testing.T) {
		if grub.Detect() {
			t.Error("Expected GRUB not to be detected when config doesn't exist")
		}
	})

	// Test case 2: GRUB config exists
	t.Run("GRUBConfigExists", func(t *testing.T) {
		grubDir := filepath.Join(tmpDir, "host", "grub")
		if err := os.MkdirAll(grubDir, 0755); err != nil {
			t.Fatalf("Failed to create grub dir: %v", err)
		}
		grubConfig := filepath.Join(grubDir, "grub.cfg")
		if err := os.WriteFile(grubConfig, []byte("# GRUB config"), 0644); err != nil {
			t.Fatalf("Failed to create grub config: %v", err)
		}

		if !grub.Detect() {
			t.Error("Expected GRUB to be detected when config exists")
		}
	})
}

//nolint:cyclop // Comprehensive test with multiple scenarios
func TestGrubBootloader_GetInstalledImages(t *testing.T) {
	tmpDir, cleanup := setupTestConfig(t)
	defer cleanup()
	grub := NewGrubBootloader()

	// Test case 1: No GRUB config exists
	t.Run("NoGRUBConfig", func(t *testing.T) {
		_, err := grub.GetInstalledImages()
		if err == nil {
			t.Error("Expected error when GRUB config doesn't exist")
		}
	})

	// Test case 2: GRUB config with SONiC images
	t.Run("GRUBConfigWithImages", func(t *testing.T) {
		grubDir := filepath.Join(tmpDir, "host", "grub")
		if err := os.MkdirAll(grubDir, 0755); err != nil {
			t.Fatalf("Failed to create grub dir: %v", err)
		}

		grubConfig := `# GRUB config file
set default=0
set timeout=5

menuentry 'SONiC-OS-202311.1-12345' {
    linux /image-202311.1-12345/boot/vmlinuz
    initrd /image-202311.1-12345/boot/initrd.img
}

menuentry 'SONiC-OS-202311.2-67890' {
    linux /image-202311.2-67890/boot/vmlinuz
    initrd /image-202311.2-67890/boot/initrd.img
}

menuentry 'Ubuntu Recovery' {
    linux /recovery/vmlinuz
}`

		grubConfigPath := filepath.Join(grubDir, "grub.cfg")
		if err := os.WriteFile(grubConfigPath, []byte(grubConfig), 0644); err != nil {
			t.Fatalf("Failed to create grub config: %v", err)
		}

		images, err := grub.GetInstalledImages()
		if err != nil {
			t.Fatalf("Failed to get installed images: %v", err)
		}

		expectedImages := []string{"SONiC-OS-202311.1-12345", "SONiC-OS-202311.2-67890"}
		if len(images) != len(expectedImages) {
			t.Errorf("Expected %d images, got %d", len(expectedImages), len(images))
		}

		for _, expected := range expectedImages {
			found := false
			for _, actual := range images {
				if actual == expected {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("Expected image %s not found in result", expected)
			}
		}
	})

	// Test case 3: GRUB config with no SONiC images
	t.Run("GRUBConfigNoImages", func(t *testing.T) {
		grubDir := filepath.Join(tmpDir, "host", "grub")
		if err := os.MkdirAll(grubDir, 0755); err != nil {
			t.Fatalf("Failed to create grub dir: %v", err)
		}

		grubConfig := `# GRUB config file
menuentry 'Ubuntu' {
    linux /boot/vmlinuz
}
menuentry 'Windows' {
    chainloader +1
}`

		grubConfigPath := filepath.Join(grubDir, "grub.cfg")
		if err := os.WriteFile(grubConfigPath, []byte(grubConfig), 0644); err != nil {
			t.Fatalf("Failed to create grub config: %v", err)
		}

		images, err := grub.GetInstalledImages()
		if err != nil {
			t.Fatalf("Failed to get installed images: %v", err)
		}

		if len(images) != 0 {
			t.Errorf("Expected 0 images, got %d", len(images))
		}
	})
}

func TestGrubBootloader_GetCurrentImage(t *testing.T) {
	tmpDir, cleanup := setupTestConfig(t)
	defer cleanup()
	grub := NewGrubBootloader()

	// Test case 1: No /proc/cmdline
	t.Run("NoCmdline", func(t *testing.T) {
		_, err := grub.GetCurrentImage()
		if err == nil {
			t.Error("Expected error when /proc/cmdline doesn't exist")
		}
	})

	// Test case 2: /proc/cmdline with SONiC image
	t.Run("CmdlineWithImage", func(t *testing.T) {
		procDir := filepath.Join(tmpDir, "proc")
		if err := os.MkdirAll(procDir, 0755); err != nil {
			t.Fatalf("Failed to create proc dir: %v", err)
		}

		cmdlineContent := "root=/dev/sda1 SONiC-OS-202311.1-12345 ro quiet"
		cmdlinePath := filepath.Join(procDir, "cmdline")
		if err := os.WriteFile(cmdlinePath, []byte(cmdlineContent), 0644); err != nil {
			t.Fatalf("Failed to create cmdline: %v", err)
		}

		current, err := grub.GetCurrentImage()
		if err != nil {
			t.Fatalf("Failed to get current image: %v", err)
		}

		expected := "SONiC-OS-202311.1-12345"
		if current != expected {
			t.Errorf("Expected current image %s, got %s", expected, current)
		}
	})

	// Test case 3: /proc/cmdline without SONiC image, fallback to available images
	t.Run("CmdlineNoImageWithFallback", func(t *testing.T) {
		// Create GRUB config with images
		grubDir := filepath.Join(tmpDir, "host", "grub")
		if err := os.MkdirAll(grubDir, 0755); err != nil {
			t.Fatalf("Failed to create grub dir: %v", err)
		}

		grubConfig := `menuentry 'SONiC-OS-202311.1-12345' {
    linux /image-202311.1-12345/boot/vmlinuz
}`
		grubConfigPath := filepath.Join(grubDir, "grub.cfg")
		if err := os.WriteFile(grubConfigPath, []byte(grubConfig), 0644); err != nil {
			t.Fatalf("Failed to create grub config: %v", err)
		}

		// Create /proc/cmdline without SONiC image
		procDir := filepath.Join(tmpDir, "proc")
		if err := os.MkdirAll(procDir, 0755); err != nil {
			t.Fatalf("Failed to create proc dir: %v", err)
		}

		cmdlineContent := "root=/dev/sda1 ro quiet"
		cmdlinePath := filepath.Join(procDir, "cmdline")
		if err := os.WriteFile(cmdlinePath, []byte(cmdlineContent), 0644); err != nil {
			t.Fatalf("Failed to create cmdline: %v", err)
		}

		current, err := grub.GetCurrentImage()
		if err != nil {
			t.Fatalf("Failed to get current image: %v", err)
		}

		expected := "SONiC-OS-202311.1-12345"
		if current != expected {
			t.Errorf("Expected current image %s, got %s", expected, current)
		}
	})
}

func TestGrubBootloader_GetNextImage(t *testing.T) {
	tmpDir, cleanup := setupTestConfig(t)
	defer cleanup()
	grub := NewGrubBootloader()

	// Setup GRUB environment
	grubDir := filepath.Join(tmpDir, "host", "grub")
	if err := os.MkdirAll(grubDir, 0755); err != nil {
		t.Fatalf("Failed to create grub dir: %v", err)
	}

	grubConfig := `set default=0
menuentry 'SONiC-OS-202311.1-12345' {
    linux /image-202311.1-12345/boot/vmlinuz
}
menuentry 'SONiC-OS-202311.2-67890' {
    linux /image-202311.2-67890/boot/vmlinuz
}`
	grubConfigPath := filepath.Join(grubDir, "grub.cfg")
	if err := os.WriteFile(grubConfigPath, []byte(grubConfig), 0644); err != nil {
		t.Fatalf("Failed to create grub config: %v", err)
	}

	// Test case 1: No grubenv file, should return default entry
	t.Run("NoGrubenv", func(t *testing.T) {
		next, err := grub.GetNextImage()
		if err != nil {
			t.Fatalf("Failed to get next image: %v", err)
		}

		expected := "SONiC-OS-202311.1-12345" // First entry (default=0)
		if next != expected {
			t.Errorf("Expected next image %s, got %s", expected, next)
		}
	})

	// Test case 2: grubenv with saved_entry
	t.Run("GrubenvWithSavedEntry", func(t *testing.T) {
		grubenvContent := `# GRUB Environment Block
saved_entry=SONiC-OS-202311.2-67890
`
		grubenvPath := filepath.Join(grubDir, "grubenv")
		if err := os.WriteFile(grubenvPath, []byte(grubenvContent), 0644); err != nil {
			t.Fatalf("Failed to create grubenv: %v", err)
		}

		next, err := grub.GetNextImage()
		if err != nil {
			t.Fatalf("Failed to get next image: %v", err)
		}

		expected := "SONiC-OS-202311.2-67890"
		if next != expected {
			t.Errorf("Expected next image %s, got %s", expected, next)
		}

		// Clean up
		os.Remove(grubenvPath)
	})
}
