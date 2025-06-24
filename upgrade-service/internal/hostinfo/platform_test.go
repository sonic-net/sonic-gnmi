package hostinfo

import (
	"os"
	"testing"
)

func TestIsMellanox(t *testing.T) {
	tests := []struct {
		name      string
		configMap map[string]string
		expected  bool
	}{
		{
			name: "Mellanox platform",
			configMap: map[string]string{
				"onie_switch_asic": "mlnx",
			},
			expected: true,
		},
		{
			name: "Non-Mellanox platform",
			configMap: map[string]string{
				"onie_switch_asic": "broadcom",
			},
			expected: false,
		},
		{
			name:      "Missing ASIC field",
			configMap: map[string]string{},
			expected:  false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := isMellanox(test.configMap)
			if result != test.expected {
				t.Errorf("Expected %v, got %v", test.expected, result)
			}
		})
	}
}

func TestIsArista(t *testing.T) {
	tests := []struct {
		name      string
		configMap map[string]string
		expected  bool
	}{
		{
			name: "Arista platform",
			configMap: map[string]string{
				"aboot_vendor": "arista",
			},
			expected: true,
		},
		{
			name: "Non-Arista platform",
			configMap: map[string]string{
				"aboot_vendor": "other",
			},
			expected: false,
		},
		{
			name:      "Missing vendor field",
			configMap: map[string]string{},
			expected:  false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := isArista(test.configMap)
			if result != test.expected {
				t.Errorf("Expected %v, got %v", test.expected, result)
			}
		})
	}
}

func TestExtractMellanoxInfo(t *testing.T) {
	configMap := map[string]string{
		"onie_switch_asic": "mlnx",
		"onie_platform":    "x86_64-mlnx_msn4600c-r0",
		"onie_machine":     "mlnx_msn4600c",
		"onie_arch":        "x86_64",
	}

	info := &PlatformInfo{
		ConfigMap: configMap,
	}

	extractMellanoxInfo(configMap, info)

	if info.Vendor != "Mellanox" {
		t.Errorf("Expected Vendor to be 'Mellanox', got '%s'", info.Vendor)
	}

	if info.Platform != "x86_64-mlnx_msn4600c-r0" {
		t.Errorf("Expected Platform to be 'x86_64-mlnx_msn4600c-r0', got '%s'", info.Platform)
	}

	if info.MachineID != "mlnx_msn4600c" {
		t.Errorf("Expected MachineID to be 'mlnx_msn4600c', got '%s'", info.MachineID)
	}

	if info.Architecture != "x86_64" {
		t.Errorf("Expected Architecture to be 'x86_64', got '%s'", info.Architecture)
	}

	if info.SwitchASIC != "mlnx" {
		t.Errorf("Expected SwitchASIC to be 'mlnx', got '%s'", info.SwitchASIC)
	}
}

func TestExtractAristaInfo(t *testing.T) {
	configMap := map[string]string{
		"aboot_vendor":   "arista",
		"aboot_platform": "x86_64-arista_7060x6_64pe",
		"aboot_machine":  "arista_7060x6_64pe",
		"aboot_arch":     "x86_64",
	}

	info := &PlatformInfo{
		ConfigMap: configMap,
	}

	extractAristaInfo(configMap, info)

	if info.Vendor != "arista" {
		t.Errorf("Expected Vendor to be 'arista', got '%s'", info.Vendor)
	}

	if info.Platform != "x86_64-arista_7060x6_64pe" {
		t.Errorf("Expected Platform to be 'x86_64-arista_7060x6_64pe', got '%s'", info.Platform)
	}

	if info.MachineID != "arista_7060x6_64pe" {
		t.Errorf("Expected MachineID to be 'arista_7060x6_64pe', got '%s'", info.MachineID)
	}

	if info.Architecture != "x86_64" {
		t.Errorf("Expected Architecture to be 'x86_64', got '%s'", info.Architecture)
	}

	// Arista should infer broadcom for 7060 platform
	if info.SwitchASIC != "broadcom" {
		t.Errorf("Expected SwitchASIC to be 'broadcom', got '%s'", info.SwitchASIC)
	}
}

func TestReadMachineConf_FileNotFound(t *testing.T) {
	// Test with a file that doesn't exist
	_, err := readMachineConf("/nonexistent/file")
	if err == nil {
		t.Error("Expected error for nonexistent file, got nil")
	}
}

func TestReadMachineConf_ValidFile(t *testing.T) {
	// Create a temporary file with test content
	content := `key1=value1
key2=value2
# This is a comment
key3=value with spaces

key4=value4`

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
		"key1": "value1",
		"key2": "value2",
		"key3": "value with spaces",
		"key4": "value4",
	}

	for key, expectedValue := range expected {
		value, exists := configMap[key]
		if !exists {
			t.Errorf("Expected key '%s' to exist in config map", key)
			continue
		}
		if value != expectedValue {
			t.Errorf("Expected value for key '%s' to be '%s', got '%s'", key, expectedValue, value)
		}
	}
}

func TestGetPlatformIdentifier(t *testing.T) {
	tests := []struct {
		name               string
		info               *PlatformInfo
		expectedIdentifier string
		expectedVendor     string
		expectedModel      string
	}{
		{
			name:               "Nil info",
			info:               nil,
			expectedIdentifier: "unknown",
			expectedVendor:     "unknown",
			expectedModel:      "unknown",
		},
		{
			name: "Mellanox SN4600",
			info: &PlatformInfo{
				Vendor:     "Mellanox",
				Platform:   "x86_64-mlnx_msn4600c-r0",
				SwitchASIC: "mlnx",
			},
			expectedIdentifier: "mellanox_sn4600",
			expectedVendor:     "Mellanox",
			expectedModel:      "sn4600",
		},
		{
			name: "Arista 7060",
			info: &PlatformInfo{
				Vendor:   "arista",
				Platform: "x86_64-arista_7060x6_64pe",
			},
			expectedIdentifier: "arista_7060",
			expectedVendor:     "arista",
			expectedModel:      "7060",
		},
		{
			name: "Unknown platform",
			info: &PlatformInfo{
				Vendor:   "unknown",
				Platform: "unknown",
			},
			expectedIdentifier: "unknown",
			expectedVendor:     "unknown",
			expectedModel:      "unknown",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Test GetPlatformIdentifierString directly
			identifier, vendor, model := GetPlatformIdentifierString(test.info)
			if identifier != test.expectedIdentifier {
				t.Errorf("GetPlatformIdentifierString identifier: Expected %v, got %v", test.expectedIdentifier, identifier)
			}
			if vendor != test.expectedVendor {
				t.Errorf("GetPlatformIdentifierString vendor: Expected %v, got %v", test.expectedVendor, vendor)
			}
			if model != test.expectedModel {
				t.Errorf("GetPlatformIdentifierString model: Expected %v, got %v", test.expectedModel, model)
			}
		})
	}
}
