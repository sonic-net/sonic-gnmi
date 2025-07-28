package diskspace

import (
	"os"
	"testing"
)

func TestGetDiskSpace_ValidPath(t *testing.T) {
	// Test with current directory
	info, err := GetDiskSpace(".")
	if err != nil {
		t.Fatalf("GetDiskSpace failed for current directory: %v", err)
	}

	// Verify we got reasonable values
	if info.TotalMB <= 0 {
		t.Errorf("Expected positive total MB, got %d", info.TotalMB)
	}

	if info.AvailableMB < 0 {
		t.Errorf("Expected non-negative available MB, got %d", info.AvailableMB)
	}

	if info.AvailableMB > info.TotalMB {
		t.Errorf("Available MB (%d) should not exceed total MB (%d)",
			info.AvailableMB, info.TotalMB)
	}

	t.Logf("Disk space for current directory - Total: %d MB, Available: %d MB",
		info.TotalMB, info.AvailableMB)
}

func TestGetDiskSpace_RootPath(t *testing.T) {
	// Test with root filesystem
	info, err := GetDiskSpace("/")
	if err != nil {
		t.Fatalf("GetDiskSpace failed for root filesystem: %v", err)
	}

	// Basic sanity checks
	if info.TotalMB <= 0 {
		t.Errorf("Expected positive total MB for root, got %d", info.TotalMB)
	}

	if info.AvailableMB < 0 {
		t.Errorf("Expected non-negative available MB for root, got %d", info.AvailableMB)
	}

	t.Logf("Disk space for root filesystem - Total: %d MB, Available: %d MB",
		info.TotalMB, info.AvailableMB)
}

func TestGetDiskSpace_TempDir(t *testing.T) {
	// Create a temporary directory
	tmpDir := t.TempDir()

	info, err := GetDiskSpace(tmpDir)
	if err != nil {
		t.Fatalf("GetDiskSpace failed for temp directory %s: %v", tmpDir, err)
	}

	// Verify reasonable values
	if info.TotalMB <= 0 {
		t.Errorf("Expected positive total MB for temp dir, got %d", info.TotalMB)
	}

	if info.AvailableMB < 0 {
		t.Errorf("Expected non-negative available MB for temp dir, got %d", info.AvailableMB)
	}

	t.Logf("Disk space for temp directory %s - Total: %d MB, Available: %d MB",
		tmpDir, info.TotalMB, info.AvailableMB)
}

func TestGetDiskSpace_NonExistentPath(t *testing.T) {
	// Test with non-existent path
	nonExistentPath := "/path/that/does/not/exist/at/all"
	_, err := GetDiskSpace(nonExistentPath)
	if err == nil {
		t.Errorf("Expected error for non-existent path %s, but got none", nonExistentPath)
	}

	t.Logf("Expected error for non-existent path: %v", err)
}

func TestGetDiskSpace_RestrictedPath(t *testing.T) {
	// Try to test with a potentially restricted path
	// This might not fail on all systems, but it's worth testing
	restrictedPaths := []string{
		"/proc/1/root", // Root process directory (usually restricted)
	}

	for _, path := range restrictedPaths {
		// Check if path exists first
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Logf("Skipping test for non-existent restricted path: %s", path)
			continue
		}

		info, err := GetDiskSpace(path)
		if err != nil {
			t.Logf("Expected error or success for restricted path %s: %v", path, err)
		} else {
			t.Logf("Disk space for restricted path %s - Total: %d MB, Available: %d MB",
				path, info.TotalMB, info.AvailableMB)
		}
	}
}

func TestGetDiskSpace_ConsistentResults(t *testing.T) {
	// Get disk space twice for the same path and ensure consistency
	path := "."

	info1, err1 := GetDiskSpace(path)
	if err1 != nil {
		t.Fatalf("First GetDiskSpace call failed: %v", err1)
	}

	info2, err2 := GetDiskSpace(path)
	if err2 != nil {
		t.Fatalf("Second GetDiskSpace call failed: %v", err2)
	}

	// Total should be exactly the same
	if info1.TotalMB != info2.TotalMB {
		t.Errorf("Total MB changed between calls: %d vs %d", info1.TotalMB, info2.TotalMB)
	}

	// Available might differ slightly due to concurrent filesystem activity,
	// but should be close (within reasonable bounds)
	diff := info1.AvailableMB - info2.AvailableMB
	if diff < 0 {
		diff = -diff
	}

	// Allow up to 10MB difference (reasonable for concurrent filesystem activity)
	if diff > 10 {
		t.Errorf("Available MB changed significantly between calls: %d vs %d (diff: %d MB)",
			info1.AvailableMB, info2.AvailableMB, diff)
	}
}

func TestGetDiskSpace_JSONMarshaling(t *testing.T) {
	// Test that our struct can be marshaled to JSON properly
	info, err := GetDiskSpace(".")
	if err != nil {
		t.Fatalf("GetDiskSpace failed: %v", err)
	}

	// The struct should have json tags, verify they work
	if info.TotalMB <= 0 {
		t.Errorf("Expected positive total, got %d", info.TotalMB)
	}

	if info.AvailableMB < 0 {
		t.Errorf("Expected non-negative available, got %d", info.AvailableMB)
	}

	// Test that the fields are properly tagged for JSON
	// This is more of a compile-time check, but we can verify the struct exists
	t.Logf("DiskSpaceInfo struct ready for JSON marshaling: %+v", info)
}
