package bootloader

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAbootBootloader_Detect(t *testing.T) {
	tmpDir, cleanup := setupTestConfig(t)
	defer cleanup()
	aboot := NewAbootBootloader()

	// Test case 1: No Aboot indicators
	t.Run("NoAbootIndicators", func(t *testing.T) {
		if aboot.Detect() {
			t.Error("Expected Aboot not to be detected when no indicators exist")
		}
	})

	// Test case 2: boot-config exists
	t.Run("BootConfigExists", func(t *testing.T) {
		hostDir := filepath.Join(tmpDir, "host")
		if err := os.MkdirAll(hostDir, 0755); err != nil {
			t.Fatalf("Failed to create host dir: %v", err)
		}
		bootConfig := filepath.Join(hostDir, "boot-config")
		if err := os.WriteFile(bootConfig, []byte("# Aboot config"), 0644); err != nil {
			t.Fatalf("Failed to create boot-config: %v", err)
		}

		if !aboot.Detect() {
			t.Error("Expected Aboot to be detected when boot-config exists")
		}

		// Clean up
		os.Remove(bootConfig)
	})

	// Test case 3: .aboot directory exists
	t.Run("AbootDirExists", func(t *testing.T) {
		hostDir := filepath.Join(tmpDir, "host")
		if err := os.MkdirAll(hostDir, 0755); err != nil {
			t.Fatalf("Failed to create host dir: %v", err)
		}
		abootDir := filepath.Join(hostDir, ".aboot")
		if err := os.MkdirAll(abootDir, 0755); err != nil {
			t.Fatalf("Failed to create .aboot dir: %v", err)
		}

		if !aboot.Detect() {
			t.Error("Expected Aboot to be detected when .aboot directory exists")
		}

		// Clean up
		os.RemoveAll(abootDir)
	})

	// Test case 4: Image directories exist
	t.Run("ImageDirsExist", func(t *testing.T) {
		hostDir := filepath.Join(tmpDir, "host")
		if err := os.MkdirAll(hostDir, 0755); err != nil {
			t.Fatalf("Failed to create host dir: %v", err)
		}
		imageDir := filepath.Join(hostDir, "image-SONiC-OS-202311.1-12345")
		if err := os.MkdirAll(imageDir, 0755); err != nil {
			t.Fatalf("Failed to create image dir: %v", err)
		}

		if !aboot.Detect() {
			t.Error("Expected Aboot to be detected when image directories exist")
		}

		// Clean up
		os.RemoveAll(imageDir)
	})
}

func TestAbootBootloader_GetInstalledImages(t *testing.T) {
	// Test case 1: No host directory
	t.Run("NoHostDir", func(t *testing.T) {
		_, cleanup := setupTestConfig(t)
		defer cleanup()
		aboot := NewAbootBootloader()

		_, err := aboot.GetInstalledImages()
		if err == nil {
			t.Error("Expected error when host directory doesn't exist")
		}
	})

	// Test case 2: Host directory with image directories
	t.Run("ImageDirsExist", func(t *testing.T) {
		tmpDir, cleanup := setupTestConfig(t)
		defer cleanup()
		aboot := NewAbootBootloader()

		hostDir := filepath.Join(tmpDir, "host")
		if err := os.MkdirAll(hostDir, 0755); err != nil {
			t.Fatalf("Failed to create host dir: %v", err)
		}

		// Create image directories
		imageDirs := []string{
			"image-SONiC-OS-202311.1-12345",
			"image-SONiC-OS-202311.2-67890",
			"image-SONiC-OS-202310.1-11111",
		}

		for _, imageDir := range imageDirs {
			if err := os.MkdirAll(filepath.Join(hostDir, imageDir), 0755); err != nil {
				t.Fatalf("Failed to create image dir %s: %v", imageDir, err)
			}
		}

		// Also create some non-image directories that should be ignored
		otherDirs := []string{"boot", "tmp", "regular-dir"}
		for _, otherDir := range otherDirs {
			if err := os.MkdirAll(filepath.Join(hostDir, otherDir), 0755); err != nil {
				t.Fatalf("Failed to create other dir %s: %v", otherDir, err)
			}
		}

		images, err := aboot.GetInstalledImages()
		if err != nil {
			t.Fatalf("Failed to get installed images: %v", err)
		}

		expectedImages := []string{
			"SONiC-OS-202311.1-12345",
			"SONiC-OS-202311.2-67890",
			"SONiC-OS-202310.1-11111",
		}

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

	// Test case 3: Host directory with no image directories
	t.Run("NoImageDirs", func(t *testing.T) {
		tmpDir, cleanup := setupTestConfig(t)
		defer cleanup()
		aboot := NewAbootBootloader()

		hostDir := filepath.Join(tmpDir, "host")
		if err := os.MkdirAll(hostDir, 0755); err != nil {
			t.Fatalf("Failed to create host dir: %v", err)
		}

		// Create non-image directories
		otherDirs := []string{"boot", "tmp", "regular-dir"}
		for _, otherDir := range otherDirs {
			if err := os.MkdirAll(filepath.Join(hostDir, otherDir), 0755); err != nil {
				t.Fatalf("Failed to create other dir %s: %v", otherDir, err)
			}
		}

		images, err := aboot.GetInstalledImages()
		if err != nil {
			t.Fatalf("Failed to get installed images: %v", err)
		}

		if len(images) != 0 {
			t.Errorf("Expected 0 images, got %d", len(images))
		}
	})
}

func TestAbootBootloader_GetCurrentImage(t *testing.T) {
	// Test case 1: No /proc/cmdline, no installed images
	t.Run("NoCmdlineNoImages", func(t *testing.T) {
		_, cleanup := setupTestConfig(t)
		defer cleanup()
		aboot := NewAbootBootloader()

		_, err := aboot.GetCurrentImage()
		if err == nil {
			t.Error("Expected error when no cmdline and no images available")
		}
	})

	// Test case 2: No /proc/cmdline, with installed images (fallback)
	t.Run("NoCmdlineWithImages", func(t *testing.T) {
		tmpDir, cleanup := setupTestConfig(t)
		defer cleanup()
		aboot := NewAbootBootloader()

		// Create host directory with image
		hostDir := filepath.Join(tmpDir, "host")
		if err := os.MkdirAll(hostDir, 0755); err != nil {
			t.Fatalf("Failed to create host dir: %v", err)
		}
		imageDir := filepath.Join(hostDir, "image-SONiC-OS-202311.1-12345")
		if err := os.MkdirAll(imageDir, 0755); err != nil {
			t.Fatalf("Failed to create image dir: %v", err)
		}

		// Create /proc/cmdline that doesn't contain SONiC image for fallback test
		procDir := filepath.Join(tmpDir, "proc")
		if err := os.MkdirAll(procDir, 0755); err != nil {
			t.Fatalf("Failed to create proc dir: %v", err)
		}
		cmdlineContent := "root=/dev/sda1 ro quiet"
		cmdlinePath := filepath.Join(procDir, "cmdline")
		if err := os.WriteFile(cmdlinePath, []byte(cmdlineContent), 0644); err != nil {
			t.Fatalf("Failed to create cmdline: %v", err)
		}

		current, err := aboot.GetCurrentImage()
		if err != nil {
			t.Fatalf("Failed to get current image: %v", err)
		}

		expected := "SONiC-OS-202311.1-12345"
		if current != expected {
			t.Errorf("Expected current image %s, got %s", expected, current)
		}
	})

	// Test case 3: /proc/cmdline with SONiC image
	t.Run("CmdlineWithImage", func(t *testing.T) {
		tmpDir, cleanup := setupTestConfig(t)
		defer cleanup()
		aboot := NewAbootBootloader()

		procDir := filepath.Join(tmpDir, "proc")
		if err := os.MkdirAll(procDir, 0755); err != nil {
			t.Fatalf("Failed to create proc dir: %v", err)
		}

		cmdlineContent := "root=/dev/loop0 SONiC-OS-202311.1-12345 ro"
		cmdlinePath := filepath.Join(procDir, "cmdline")
		if err := os.WriteFile(cmdlinePath, []byte(cmdlineContent), 0644); err != nil {
			t.Fatalf("Failed to create cmdline: %v", err)
		}

		current, err := aboot.GetCurrentImage()
		if err != nil {
			t.Fatalf("Failed to get current image: %v", err)
		}

		expected := "SONiC-OS-202311.1-12345"
		if current != expected {
			t.Errorf("Expected current image %s, got %s", expected, current)
		}
	})
}

func TestAbootBootloader_GetNextImage(t *testing.T) {
	// Test case 1: No boot-config, should fallback to current image
	t.Run("NoBootConfig", func(t *testing.T) {
		tmpDir, cleanup := setupTestConfig(t)
		defer cleanup()
		aboot := NewAbootBootloader()

		// Setup basic environment
		hostDir := filepath.Join(tmpDir, "host")
		if err := os.MkdirAll(hostDir, 0755); err != nil {
			t.Fatalf("Failed to create host dir: %v", err)
		}
		imageDir := filepath.Join(hostDir, "image-SONiC-OS-202311.1-12345")
		if err := os.MkdirAll(imageDir, 0755); err != nil {
			t.Fatalf("Failed to create image dir: %v", err)
		}

		// Create /proc/cmdline for current image determination
		procDir := filepath.Join(tmpDir, "proc")
		if err := os.MkdirAll(procDir, 0755); err != nil {
			t.Fatalf("Failed to create proc dir: %v", err)
		}

		cmdlineContent := "root=/dev/loop0 SONiC-OS-202311.1-12345 ro"
		cmdlinePath := filepath.Join(procDir, "cmdline")
		if err := os.WriteFile(cmdlinePath, []byte(cmdlineContent), 0644); err != nil {
			t.Fatalf("Failed to create cmdline: %v", err)
		}

		next, err := aboot.GetNextImage()
		if err != nil {
			t.Fatalf("Failed to get next image: %v", err)
		}

		expected := "SONiC-OS-202311.1-12345"
		if next != expected {
			t.Errorf("Expected next image %s, got %s", expected, next)
		}
	})

	// Test case 2: boot-config with SONiC image
	t.Run("BootConfigWithImage", func(t *testing.T) {
		tmpDir, cleanup := setupTestConfig(t)
		defer cleanup()
		aboot := NewAbootBootloader()

		// Setup basic environment
		hostDir := filepath.Join(tmpDir, "host")
		if err := os.MkdirAll(hostDir, 0755); err != nil {
			t.Fatalf("Failed to create host dir: %v", err)
		}

		bootConfigContent := `# Aboot boot configuration
image=SONiC-OS-202311.2-67890
`
		bootConfigPath := filepath.Join(hostDir, "boot-config")
		if err := os.WriteFile(bootConfigPath, []byte(bootConfigContent), 0644); err != nil {
			t.Fatalf("Failed to create boot-config: %v", err)
		}

		next, err := aboot.GetNextImage()
		if err != nil {
			t.Fatalf("Failed to get next image: %v", err)
		}

		expected := "SONiC-OS-202311.2-67890"
		if next != expected {
			t.Errorf("Expected next image %s, got %s", expected, next)
		}
	})

	// Test case 3: boot-config without SONiC image, fallback to current
	t.Run("BootConfigNoImage", func(t *testing.T) {
		tmpDir, cleanup := setupTestConfig(t)
		defer cleanup()
		aboot := NewAbootBootloader()

		// Setup basic environment
		hostDir := filepath.Join(tmpDir, "host")
		if err := os.MkdirAll(hostDir, 0755); err != nil {
			t.Fatalf("Failed to create host dir: %v", err)
		}
		imageDir := filepath.Join(hostDir, "image-SONiC-OS-202311.1-12345")
		if err := os.MkdirAll(imageDir, 0755); err != nil {
			t.Fatalf("Failed to create image dir: %v", err)
		}

		// Create /proc/cmdline for fallback to current image
		procDir := filepath.Join(tmpDir, "proc")
		if err := os.MkdirAll(procDir, 0755); err != nil {
			t.Fatalf("Failed to create proc dir: %v", err)
		}
		cmdlineContent := "root=/dev/sda1 ro quiet"
		cmdlinePath := filepath.Join(procDir, "cmdline")
		if err := os.WriteFile(cmdlinePath, []byte(cmdlineContent), 0644); err != nil {
			t.Fatalf("Failed to create cmdline: %v", err)
		}

		bootConfigContent := `# Aboot boot configuration
timeout=5
`
		bootConfigPath := filepath.Join(hostDir, "boot-config")
		if err := os.WriteFile(bootConfigPath, []byte(bootConfigContent), 0644); err != nil {
			t.Fatalf("Failed to create boot-config: %v", err)
		}

		next, err := aboot.GetNextImage()
		if err != nil {
			t.Fatalf("Failed to get next image: %v", err)
		}

		// Should fallback to current image
		expected := "SONiC-OS-202311.1-12345" // From installed images
		if next != expected {
			t.Errorf("Expected next image %s, got %s", expected, next)
		}
	})
}

func TestAbootBootloader_extractImageNameFromConfig(t *testing.T) {
	aboot := &AbootBootloader{}

	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Simple image line",
			input:    "image=SONiC-OS-202311.1-12345",
			expected: "SONiC-OS-202311.1-12345",
		},
		{
			name:     "Image line with quotes",
			input:    `image="SONiC-OS-202311.1-12345"`,
			expected: "SONiC-OS-202311.1-12345",
		},
		{
			name:     "Image line with path",
			input:    "kernel=/image-SONiC-OS-202311.1-12345/boot/vmlinuz SONiC-OS-202311.1-12345",
			expected: "SONiC-OS-202311.1-12345",
		},
		{
			name:     "No image in line",
			input:    "timeout=5",
			expected: "",
		},
		{
			name:     "Empty line",
			input:    "",
			expected: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := aboot.extractImageNameFromConfig(tc.input)
			if result != tc.expected {
				t.Errorf("Expected %s, got %s", tc.expected, result)
			}
		})
	}
}
