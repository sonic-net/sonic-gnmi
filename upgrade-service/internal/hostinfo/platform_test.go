package hostinfo

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/config"
)

func TestExtractPlatform(t *testing.T) {
	tests := []struct {
		name      string
		configMap map[string]string
		expected  string
	}{
		{
			name: "ONIE platform",
			configMap: map[string]string{
				"onie_platform": "x86_64-mlnx_msn4600c-r0",
			},
			expected: "x86_64-mlnx_msn4600c-r0",
		},
		{
			name: "Aboot platform",
			configMap: map[string]string{
				"aboot_platform": "x86_64-arista_7060x6_64pe",
			},
			expected: "x86_64-arista_7060x6_64pe",
		},
		{
			name: "ONIE machine fallback",
			configMap: map[string]string{
				"onie_machine": "mlnx_msn4600c",
			},
			expected: "mlnx_msn4600c",
		},
		{
			name: "Aboot machine fallback",
			configMap: map[string]string{
				"aboot_machine": "arista_7060x6_64pe",
			},
			expected: "arista_7060x6_64pe",
		},
		{
			name: "Prefer platform over machine",
			configMap: map[string]string{
				"onie_platform": "x86_64-mlnx_msn4600c-r0",
				"onie_machine":  "mlnx_msn4600c",
			},
			expected: "x86_64-mlnx_msn4600c-r0",
		},
		{
			name:      "No platform fields",
			configMap: map[string]string{},
			expected:  "unknown",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := extractPlatform(test.configMap)
			if result != test.expected {
				t.Errorf("Expected %s, got %s", test.expected, result)
			}
		})
	}
}

func TestReadMachineConf(t *testing.T) {
	content := `onie_platform=x86_64-mlnx_msn4600c-r0
onie_machine=mlnx_msn4600c
# This is a comment
onie_arch=x86_64

key_with_spaces=value with spaces`

	tmpFile, err := os.CreateTemp("", "machine.conf")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(content); err != nil {
		t.Fatalf("Failed to write to temp file: %v", err)
	}
	if err := tmpFile.Close(); err != nil {
		t.Fatalf("Failed to close temp file: %v", err)
	}

	configMap, err := readMachineConf(tmpFile.Name())
	if err != nil {
		t.Fatalf("Failed to read machine.conf: %v", err)
	}

	expected := map[string]string{
		"onie_platform":   "x86_64-mlnx_msn4600c-r0",
		"onie_machine":    "mlnx_msn4600c",
		"onie_arch":       "x86_64",
		"key_with_spaces": "value with spaces",
	}

	if len(configMap) != len(expected) {
		t.Errorf("Expected %d entries, got %d", len(expected), len(configMap))
	}

	for key, expectedValue := range expected {
		if value, exists := configMap[key]; !exists {
			t.Errorf("Expected key '%s' to exist", key)
		} else if value != expectedValue {
			t.Errorf("Expected value for key '%s' to be '%s', got '%s'", key, expectedValue, value)
		}
	}
}

func TestReadMachineConf_FileNotFound(t *testing.T) {
	_, err := readMachineConf("/nonexistent/file")
	if err == nil {
		t.Error("Expected error for nonexistent file, got nil")
	}
}

func TestGetPlatformIdentifierString(t *testing.T) {
	tests := []struct {
		name     string
		info     *PlatformInfo
		expected string
	}{
		{
			name:     "Nil info",
			info:     nil,
			expected: "unknown",
		},
		{
			name: "Valid platform",
			info: &PlatformInfo{
				Platform: "x86_64-mlnx_msn4600c-r0",
			},
			expected: "x86_64-mlnx_msn4600c-r0",
		},
		{
			name: "Empty platform",
			info: &PlatformInfo{
				Platform: "",
			},
			expected: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := GetPlatformIdentifierString(test.info)
			if result != test.expected {
				t.Errorf("Expected %s, got %s", test.expected, result)
			}
		})
	}
}

func TestGetPlatformInfo(t *testing.T) {
	// Create temp directory and machine.conf file
	tempDir := t.TempDir()
	machineConfPath := filepath.Join(tempDir, "host", "machine.conf")

	// Create directory
	err := os.MkdirAll(filepath.Dir(machineConfPath), 0755)
	if err != nil {
		t.Fatalf("Failed to create directory: %v", err)
	}

	// Write test machine.conf
	content := `onie_platform=x86_64-mlnx_msn4600c-r0
onie_machine=mlnx_msn4600c
onie_arch=x86_64
# This is a comment
onie_version=2020.11`

	err = os.WriteFile(machineConfPath, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to write machine.conf: %v", err)
	}

	// Mock config
	originalConfig := config.Global
	config.Global = &config.Config{RootFS: tempDir}
	defer func() { config.Global = originalConfig }()

	// Test GetPlatformInfo
	info, err := GetPlatformInfo()
	if err != nil {
		t.Fatalf("GetPlatformInfo failed: %v", err)
	}

	if info.Platform != "x86_64-mlnx_msn4600c-r0" {
		t.Errorf("Expected Platform 'x86_64-mlnx_msn4600c-r0', got '%s'", info.Platform)
	}

	if len(info.ConfigMap) == 0 {
		t.Error("Expected non-empty ConfigMap")
	}

	if info.ConfigMap["onie_platform"] != "x86_64-mlnx_msn4600c-r0" {
		t.Errorf("Expected ConfigMap['onie_platform'] to be 'x86_64-mlnx_msn4600c-r0', got '%s'",
			info.ConfigMap["onie_platform"])
	}
}

func TestGetPlatformInfo_FileNotFound(t *testing.T) {
	// Mock config with non-existent path
	originalConfig := config.Global
	config.Global = &config.Config{RootFS: "/non-existent-path"}
	defer func() { config.Global = originalConfig }()

	info, err := GetPlatformInfo()
	if err == nil {
		t.Error("Expected error for non-existent file, got nil")
	}
	if info != nil {
		t.Error("Expected nil info for error case")
	}
}

func TestGetMachineConfPath(t *testing.T) {
	// Save original config
	originalConfig := config.Global
	defer func() { config.Global = originalConfig }()

	// Test with custom RootFS
	config.Global = &config.Config{
		RootFS: "/test/root",
	}

	path := getMachineConfPath()
	expected := "/test/root/host/machine.conf"
	if path != expected {
		t.Errorf("Expected path %s, got %s", expected, path)
	}
}
