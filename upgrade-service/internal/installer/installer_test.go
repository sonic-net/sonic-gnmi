package installer

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNewSonicInstaller(t *testing.T) {
	installer := NewSonicInstaller()
	if installer == nil {
		t.Error("Expected non-nil installer")
	}
}

func TestParseListOutput(t *testing.T) {
	installer := NewSonicInstaller()

	// Test case 1: Normal output with current and next images
	t.Run("NormalOutput", func(t *testing.T) {
		output := `Available images:
--------
SONiC-OS-202311.1-build123 (Current)
SONiC-OS-202311.2-build456 (Next)
SONiC-OS-202310.1-build789`

		result, err := installer.parseListOutput(output)
		if err != nil {
			t.Fatalf("Failed to parse output: %v", err)
		}

		if len(result.Images) != 3 {
			t.Errorf("Expected 3 images, got %d", len(result.Images))
		}

		if result.Current != "SONiC-OS-202311.1-build123" {
			t.Errorf("Expected current image 'SONiC-OS-202311.1-build123', got %s", result.Current)
		}

		if result.Next != "SONiC-OS-202311.2-build456" {
			t.Errorf("Expected next image 'SONiC-OS-202311.2-build456', got %s", result.Next)
		}

		// Check individual image info
		expectedImages := []ImageInfo{
			{Name: "SONiC-OS-202311.1-build123", Current: true, Next: false},
			{Name: "SONiC-OS-202311.2-build456", Current: false, Next: true},
			{Name: "SONiC-OS-202310.1-build789", Current: false, Next: false},
		}

		for i, expected := range expectedImages {
			if i >= len(result.Images) {
				t.Errorf("Missing image at index %d", i)
				continue
			}
			actual := result.Images[i]
			if actual.Name != expected.Name {
				t.Errorf("Image %d: expected name %s, got %s", i, expected.Name, actual.Name)
			}
			if actual.Current != expected.Current {
				t.Errorf("Image %d: expected current %t, got %t", i, expected.Current, actual.Current)
			}
			if actual.Next != expected.Next {
				t.Errorf("Image %d: expected next %t, got %t", i, expected.Next, actual.Next)
			}
		}
	})

	// Test case 2: Single image
	t.Run("SingleImage", func(t *testing.T) {
		output := `Available images:
--------
SONiC-OS-202311.1-build123 (Current)`

		result, err := installer.parseListOutput(output)
		if err != nil {
			t.Fatalf("Failed to parse output: %v", err)
		}

		if len(result.Images) != 1 {
			t.Errorf("Expected 1 image, got %d", len(result.Images))
		}

		if result.Current != "SONiC-OS-202311.1-build123" {
			t.Errorf("Expected current image 'SONiC-OS-202311.1-build123', got %s", result.Current)
		}

		if result.Next != "" {
			t.Errorf("Expected no next image, got %s", result.Next)
		}
	})

	// Test case 3: Empty output
	t.Run("EmptyOutput", func(t *testing.T) {
		output := `Available images:
--------`

		result, err := installer.parseListOutput(output)
		if err != nil {
			t.Fatalf("Failed to parse output: %v", err)
		}

		if len(result.Images) != 0 {
			t.Errorf("Expected 0 images, got %d", len(result.Images))
		}
	})
}

func TestParseCleanupOutput(t *testing.T) {
	installer := NewSonicInstaller()

	// Test case 1: Normal cleanup with removed images
	t.Run("ImagesRemoved", func(t *testing.T) {
		output := `Removing image SONiC-OS-202310.1-build789
Removing image SONiC-OS-202310.2-build012
Image removed
Image removed`

		result, err := installer.parseCleanupOutput(output)
		if err != nil {
			t.Fatalf("Failed to parse output: %v", err)
		}

		expectedImages := []string{
			"SONiC-OS-202310.1-build789",
			"SONiC-OS-202310.2-build012",
		}

		if len(result.RemovedImages) != len(expectedImages) {
			t.Errorf("Expected %d removed images, got %d", len(expectedImages), len(result.RemovedImages))
		}

		for i, expected := range expectedImages {
			if i >= len(result.RemovedImages) {
				t.Errorf("Missing removed image at index %d", i)
				continue
			}
			if result.RemovedImages[i] != expected {
				t.Errorf("Removed image %d: expected %s, got %s", i, expected, result.RemovedImages[i])
			}
		}
	})

	// Test case 2: No images to remove
	t.Run("NoImagesToRemove", func(t *testing.T) {
		output := `No image(s) to remove`

		result, err := installer.parseCleanupOutput(output)
		if err != nil {
			t.Fatalf("Failed to parse output: %v", err)
		}

		if len(result.RemovedImages) != 0 {
			t.Errorf("Expected 0 removed images, got %d", len(result.RemovedImages))
		}

		if result.Message != "No image(s) to remove" {
			t.Errorf("Expected message 'No image(s) to remove', got %s", result.Message)
		}
	})
}

// Integration tests (these require sonic-installer to be available).
func TestIntegration(t *testing.T) {
	// Skip integration tests if sonic-installer is not available
	if _, err := exec.LookPath("sonic-installer"); err != nil {
		t.Skip("sonic-installer not available, skipping integration tests")
	}

	installer := NewSonicInstaller()

	t.Run("List", func(t *testing.T) {
		result, err := installer.List()
		if err != nil {
			t.Logf("List integration test failed (this is expected on non-SONiC systems): %v", err)
			return
		}

		// If we get here, we're on a SONiC system
		t.Logf("Found %d images", len(result.Images))
		t.Logf("Current: %s", result.Current)
		t.Logf("Next: %s", result.Next)
	})
}

// Test with mock binary for controlled testing.
func TestWithMockBinary(t *testing.T) {
	// Create a temporary directory for our mock binary
	tmpDir, err := os.MkdirTemp("", "installer_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create mock sonic-installer script with the exact name
	mockBinary := filepath.Join(tmpDir, "sonic-installer")
	mockScript := `#!/bin/bash
case "$1" in
    "list")
        echo "Available images:"
        echo "--------"
        echo "SONiC-OS-202311.1-build123 (Current)"
        echo "SONiC-OS-202311.2-build456 (Next)"
        echo "SONiC-OS-202310.1-build789"
        ;;
    "cleanup")
        echo "Removing image SONiC-OS-202310.1-build789"
        echo "Image removed"
        ;;
    "set-default")
        echo "Default image set to $2"
        ;;
    *)
        echo "Unknown command: $1"
        exit 1
        ;;
esac
`

	if err := os.WriteFile(mockBinary, []byte(mockScript), 0755); err != nil {
		t.Fatalf("Failed to create mock binary: %v", err)
	}

	// Save original PATH and prepend our temp directory
	origPath := os.Getenv("PATH")
	os.Setenv("PATH", tmpDir+":"+origPath)
	defer os.Setenv("PATH", origPath)

	installer := NewSonicInstaller()

	// Test List
	t.Run("MockList", func(t *testing.T) {
		result, err := installer.List()
		if err != nil {
			t.Fatalf("Failed to execute list: %v", err)
		}

		if len(result.Images) != 3 {
			t.Errorf("Expected 3 images, got %d", len(result.Images))
		}

		if result.Current != "SONiC-OS-202311.1-build123" {
			t.Errorf("Expected current image 'SONiC-OS-202311.1-build123', got %s", result.Current)
		}
	})

	// Test SetDefault
	t.Run("MockSetDefault", func(t *testing.T) {
		result, err := installer.SetDefault("SONiC-OS-202311.1-build123")
		if err != nil {
			t.Fatalf("Failed to execute set-default: %v", err)
		}

		if result.DefaultImage != "SONiC-OS-202311.1-build123" {
			t.Errorf("Expected default image 'SONiC-OS-202311.1-build123', got %s", result.DefaultImage)
		}
	})

	// Test Cleanup
	t.Run("MockCleanup", func(t *testing.T) {
		result, err := installer.Cleanup()
		if err != nil {
			t.Fatalf("Failed to execute cleanup: %v", err)
		}

		if len(result.RemovedImages) != 1 {
			t.Errorf("Expected 1 removed image, got %d", len(result.RemovedImages))
		}

		if result.RemovedImages[0] != "SONiC-OS-202310.1-build789" {
			t.Errorf("Expected removed image 'SONiC-OS-202310.1-build789', got %s", result.RemovedImages[0])
		}
	})
}
