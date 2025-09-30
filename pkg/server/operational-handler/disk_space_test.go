package operationalhandler

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
)

func TestDiskSpaceHandler_SupportedPaths(t *testing.T) {
	handler := NewDiskSpaceHandler()
	paths := handler.SupportedPaths()

	expected := []string{"filesystem/disk-space"}
	if len(paths) != len(expected) {
		t.Errorf("expected %d paths, got %d", len(expected), len(paths))
	}

	for i, path := range paths {
		if path != expected[i] {
			t.Errorf("expected path %s, got %s", expected[i], path)
		}
	}
}

func TestDiskSpaceHandler_ExtractFilesystemPath(t *testing.T) {
	handler := NewDiskSpaceHandler()

	tests := []struct {
		name     string
		path     *gnmipb.Path
		expected string
		wantErr  bool
	}{
		{
			name: "valid path with root filesystem",
			path: &gnmipb.Path{
				Elem: []*gnmipb.PathElem{
					{Name: "sonic"},
					{Name: "system"},
					{
						Name: "filesystem",
						Key:  map[string]string{"path": "/"},
					},
					{Name: "disk-space"},
				},
			},
			expected: "/",
			wantErr:  false,
		},
		{
			name: "valid path with tmp filesystem",
			path: &gnmipb.Path{
				Elem: []*gnmipb.PathElem{
					{Name: "sonic"},
					{Name: "system"},
					{
						Name: "filesystem",
						Key:  map[string]string{"path": "/tmp"},
					},
					{Name: "disk-space"},
				},
			},
			expected: "/tmp",
			wantErr:  false,
		},
		{
			name:     "nil path",
			path:     nil,
			expected: "",
			wantErr:  true,
		},
		{
			name: "empty path elements",
			path: &gnmipb.Path{
				Elem: []*gnmipb.PathElem{},
			},
			expected: "",
			wantErr:  true,
		},
		{
			name: "no filesystem element",
			path: &gnmipb.Path{
				Elem: []*gnmipb.PathElem{
					{Name: "sonic"},
					{Name: "system"},
					{Name: "disk-space"},
				},
			},
			expected: "",
			wantErr:  true,
		},
		{
			name: "filesystem element without path key",
			path: &gnmipb.Path{
				Elem: []*gnmipb.PathElem{
					{Name: "sonic"},
					{Name: "system"},
					{
						Name: "filesystem",
						Key:  map[string]string{"other": "value"},
					},
					{Name: "disk-space"},
				},
			},
			expected: "",
			wantErr:  true,
		},
		{
			name: "filesystem element with empty path",
			path: &gnmipb.Path{
				Elem: []*gnmipb.PathElem{
					{Name: "sonic"},
					{Name: "system"},
					{
						Name: "filesystem",
						Key:  map[string]string{"path": ""},
					},
					{Name: "disk-space"},
				},
			},
			expected: "",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := handler.extractFilesystemPath(tt.path)

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if result != tt.expected {
					t.Errorf("expected %s, got %s", tt.expected, result)
				}
			}
		})
	}
}

func TestDiskSpaceHandler_HandleGet(t *testing.T) {
	handler := NewDiskSpaceHandler()

	// Create a valid gNMI path
	path := &gnmipb.Path{
		Elem: []*gnmipb.PathElem{
			{Name: "sonic"},
			{Name: "system"},
			{
				Name: "filesystem",
				Key:  map[string]string{"path": "/"},
			},
			{Name: "disk-space"},
		},
	}

	data, err := handler.HandleGet(path)
	if err != nil {
		t.Fatalf("HandleGet failed: %v", err)
	}

	// Verify JSON structure
	var diskSpace DiskSpaceInfo
	err = json.Unmarshal(data, &diskSpace)
	if err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}

	if diskSpace.Path != "/" {
		t.Errorf("expected path /, got %s", diskSpace.Path)
	}

	if diskSpace.TotalMB == 0 {
		t.Errorf("expected non-zero total MB")
	}

	if diskSpace.AvailableMB > diskSpace.TotalMB {
		t.Errorf("available MB cannot be greater than total MB")
	}
}

func TestDiskSpaceHandler_IsValidPath(t *testing.T) {
	handler := NewDiskSpaceHandler()

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
			result := handler.IsValidPath(tt.path)
			if result != tt.expected {
				t.Errorf("expected %v for path %s, got %v", tt.expected, tt.path, result)
			}
		})
	}
}

func TestDiskSpaceHandler_FormatDiskSpaceInfo(t *testing.T) {
	handler := NewDiskSpaceHandler()

	// Test with nil info
	result := handler.FormatDiskSpaceInfo(nil)
	expected := "No disk space information available"
	if result != expected {
		t.Errorf("expected %s, got %s", expected, result)
	}

	// Test with valid info
	info := &DiskSpaceInfo{
		Path:        "/test",
		TotalMB:     1000,
		AvailableMB: 600,
	}

	result = handler.FormatDiskSpaceInfo(info)
	// Should contain key information
	if !containsAll(result, []string{"/test", "1000", "600", "400", "40.0"}) {
		t.Errorf("formatted string missing expected values: %s", result)
	}
}

func TestDiskSpaceHandler_GetDiskSpaceForMultiplePaths(t *testing.T) {
	handler := NewDiskSpaceHandler()

	// Test with valid paths
	paths := []string{"/", "/tmp"}
	results, err := handler.GetDiskSpaceForMultiplePaths(paths)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if len(results) != len(paths) {
		t.Errorf("expected %d results, got %d", len(paths), len(results))
	}

	// Test with mix of valid and invalid paths
	invalidPaths := []string{"/", "/this/does/not/exist"}
	results, err = handler.GetDiskSpaceForMultiplePaths(invalidPaths)

	if err == nil {
		t.Errorf("expected error for invalid paths")
	}

	// Should still get results for valid paths
	if len(results) != 1 {
		t.Errorf("expected 1 result for valid path, got %d", len(results))
	}
}

// TestDiskSpaceHandler_WithTempDir tests disk space functionality with a temporary directory
func TestDiskSpaceHandler_WithTempDir(t *testing.T) {
	handler := NewDiskSpaceHandler()

	// Create a temporary directory
	tempDir, err := ioutil.TempDir("", "disk_space_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a gNMI path for the temp directory
	path := &gnmipb.Path{
		Elem: []*gnmipb.PathElem{
			{Name: "sonic"},
			{Name: "system"},
			{
				Name: "filesystem",
				Key:  map[string]string{"path": tempDir},
			},
			{Name: "disk-space"},
		},
	}

	// Test HandleGet with temp directory
	data, err := handler.HandleGet(path)
	if err != nil {
		t.Fatalf("failed to get disk space for temp dir: %v", err)
	}

	var diskSpace DiskSpaceInfo
	err = json.Unmarshal(data, &diskSpace)
	if err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}

	if diskSpace.TotalMB == 0 {
		t.Errorf("expected non-zero total MB for temp dir")
	}

	// Verify path is cleaned
	expectedPath := filepath.Clean(tempDir)
	if diskSpace.Path != expectedPath {
		t.Errorf("expected path %s, got %s", expectedPath, diskSpace.Path)
	}
}

// Helper function to check if a string contains all required substrings
func containsAll(str string, substrings []string) bool {
	for _, substr := range substrings {
		if !strings.Contains(str, substr) {
			return false
		}
	}
	return true
}
