package diskspace

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNew(t *testing.T) {
	monitor := New()
	if monitor == nil {
		t.Fatal("expected non-nil monitor")
	}
}

func TestMonitor_Get(t *testing.T) {
	monitor := New()

	// Test with root filesystem (should always exist)
	info, err := monitor.Get("/")
	if err != nil {
		t.Fatalf("failed to get disk space for /: %v", err)
	}

	if info.Path != "/" {
		t.Errorf("expected path /, got %s", info.Path)
	}

	if info.TotalMB == 0 {
		t.Errorf("expected non-zero total MB")
	}

	if info.AvailableMB > info.TotalMB {
		t.Errorf("available MB (%d) cannot be greater than total MB (%d)",
			info.AvailableMB, info.TotalMB)
	}

	// Test with current directory (should also work)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	info2, err := monitor.Get(wd)
	if err != nil {
		t.Fatalf("failed to get disk space for %s: %v", wd, err)
	}

	if info2.TotalMB == 0 {
		t.Errorf("expected non-zero total MB for working directory")
	}
}

func TestMonitor_GetErrors(t *testing.T) {
	monitor := New()

	tests := []struct {
		name string
		path string
	}{
		{
			name: "empty path",
			path: "",
		},
		{
			name: "non-existent path",
			path: "/this/path/should/not/exist/12345",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := monitor.Get(tt.path)
			if err == nil {
				t.Errorf("expected error for path %s", tt.path)
			}
		})
	}
}

func TestMonitor_IsValidPath(t *testing.T) {
	monitor := New()

	tests := []struct {
		name     string
		path     string
		expected bool
	}{
		{
			name:     "root path",
			path:     "/",
			expected: true,
		},
		{
			name:     "tmp path",
			path:     "/tmp",
			expected: true,
		},
		{
			name:     "empty path",
			path:     "",
			expected: false,
		},
		{
			name:     "non-existent path",
			path:     "/this/does/not/exist",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := monitor.IsValidPath(tt.path)
			if result != tt.expected {
				t.Errorf("expected %v for path %s, got %v", tt.expected, tt.path, result)
			}
		})
	}
}

func TestMonitor_GetMultiple(t *testing.T) {
	monitor := New()

	// Test with valid paths
	paths := []string{"/", "/tmp"}
	results, err := monitor.GetMultiple(paths)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if len(results) != len(paths) {
		t.Errorf("expected %d results, got %d", len(paths), len(results))
	}

	// Test with mix of valid and invalid paths
	invalidPaths := []string{"/", "/this/does/not/exist"}
	results, err = monitor.GetMultiple(invalidPaths)

	if err == nil {
		t.Errorf("expected error for invalid paths")
	}

	// Should still get results for valid paths
	if len(results) != 1 {
		t.Errorf("expected 1 result for valid path, got %d", len(results))
	}
}

func TestFormatInfo(t *testing.T) {
	// Test with nil info
	result := FormatInfo(nil)
	expected := "No disk space information available"
	if result != expected {
		t.Errorf("expected %s, got %s", expected, result)
	}

	// Test with valid info
	info := &Info{
		Path:        "/test",
		TotalMB:     1000,
		AvailableMB: 600,
	}

	result = FormatInfo(info)
	// Should contain key information
	if !containsAll(result, []string{"/test", "1000", "600", "400", "40.0"}) {
		t.Errorf("formatted string missing expected values: %s", result)
	}
}

// TestMonitor_WithTempDir tests disk space functionality with a temporary directory
func TestMonitor_WithTempDir(t *testing.T) {
	monitor := New()

	// Create a temporary directory
	tempDir, err := ioutil.TempDir("", "disk_space_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Test disk space for temp directory
	info, err := monitor.Get(tempDir)
	if err != nil {
		t.Fatalf("failed to get disk space for temp dir: %v", err)
	}

	if info.TotalMB == 0 {
		t.Errorf("expected non-zero total MB for temp dir")
	}

	// Verify path is cleaned
	expectedPath := filepath.Clean(tempDir)
	if info.Path != expectedPath {
		t.Errorf("expected path %s, got %s", expectedPath, info.Path)
	}
}

// Helper function to check if a string contains all required substrings
func containsAll(str string, substrings []string) bool {
	for _, substr := range substrings {
		if !contains(str, substr) {
			return false
		}
	}
	return true
}

// Simple string contains check (for compatibility)
func contains(str, substr string) bool {
	return strings.Contains(str, substr)
}
