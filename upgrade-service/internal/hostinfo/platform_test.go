package hostinfo

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/config"
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
	}{
		{
			name:               "Nil info",
			info:               nil,
			expectedIdentifier: "unknown",
		},
		{
			name: "Mellanox SN4600",
			info: &PlatformInfo{
				Vendor:     "Mellanox",
				Platform:   "x86_64-mlnx_msn4600c-r0",
				SwitchASIC: "mlnx",
			},
			expectedIdentifier: "mellanox_sn4600",
		},
		{
			name: "Arista 7060",
			info: &PlatformInfo{
				Vendor:   "arista",
				Platform: "x86_64-arista_7060x6_64pe",
			},
			expectedIdentifier: "arista_7060",
		},
		{
			name: "Unknown platform",
			info: &PlatformInfo{
				Vendor:   "unknown",
				Platform: "unknown",
			},
			expectedIdentifier: "unknown",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Test GetPlatformIdentifierString directly
			identifier := GetPlatformIdentifierString(test.info)
			if identifier != test.expectedIdentifier {
				t.Errorf("GetPlatformIdentifierString identifier: Expected %v, got %v", test.expectedIdentifier, identifier)
			}
		})
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

func TestIsKVM(t *testing.T) {
	tests := []struct {
		name      string
		configMap map[string]string
		expected  bool
	}{
		{
			name: "KVM with platform containing kvm",
			configMap: map[string]string{
				"onie_platform": "x86_64-kvm_x86_64-r0",
			},
			expected: true,
		},
		{
			name: "KVM with machine containing kvm",
			configMap: map[string]string{
				"onie_machine": "kvm_x86_64",
			},
			expected: true,
		},
		{
			name: "KVM with qemu ASIC",
			configMap: map[string]string{
				"onie_switch_asic": "qemu",
			},
			expected: true,
		},
		{
			name: "Non-KVM platform",
			configMap: map[string]string{
				"onie_platform":    "x86_64-mlnx_msn4600c-r0",
				"onie_machine":     "mlnx_msn4600c",
				"onie_switch_asic": "mlnx",
			},
			expected: false,
		},
		{
			name:      "Empty config",
			configMap: map[string]string{},
			expected:  false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := isKVM(test.configMap)
			if result != test.expected {
				t.Errorf("Expected %v, got %v", test.expected, result)
			}
		})
	}
}

func TestExtractKVMInfo(t *testing.T) {
	configMap := map[string]string{
		"onie_platform":    "x86_64-kvm_x86_64-r0",
		"onie_machine":     "kvm_x86_64",
		"onie_arch":        "x86_64",
		"onie_switch_asic": "qemu",
	}

	info := &PlatformInfo{
		ConfigMap: configMap,
	}

	extractKVMInfo(configMap, info)

	if info.Vendor != "KVM" {
		t.Errorf("Expected Vendor to be 'KVM', got '%s'", info.Vendor)
	}
	if info.Platform != "x86_64-kvm_x86_64-r0" {
		t.Errorf("Expected Platform to be 'x86_64-kvm_x86_64-r0', got '%s'", info.Platform)
	}
	if info.MachineID != "kvm_x86_64" {
		t.Errorf("Expected MachineID to be 'kvm_x86_64', got '%s'", info.MachineID)
	}
	if info.Architecture != "x86_64" {
		t.Errorf("Expected Architecture to be 'x86_64', got '%s'", info.Architecture)
	}
	if info.SwitchASIC != "qemu" {
		t.Errorf("Expected SwitchASIC to be 'qemu', got '%s'", info.SwitchASIC)
	}
}

func TestExtractCommonInfo(t *testing.T) {
	configMap := map[string]string{
		"onie_platform":    "x86_64-dell_s6100-r0",
		"onie_machine":     "dell_s6100",
		"onie_arch":        "x86_64",
		"onie_switch_asic": "broadcom",
	}

	info := &PlatformInfo{
		ConfigMap: configMap,
	}

	extractCommonInfo(configMap, info)

	if info.Platform != "x86_64-dell_s6100-r0" {
		t.Errorf("Expected Platform to be 'x86_64-dell_s6100-r0', got '%s'", info.Platform)
	}
	if info.MachineID != "dell_s6100" {
		t.Errorf("Expected MachineID to be 'dell_s6100', got '%s'", info.MachineID)
	}
	if info.Architecture != "x86_64" {
		t.Errorf("Expected Architecture to be 'x86_64', got '%s'", info.Architecture)
	}
	if info.SwitchASIC != "broadcom" {
		t.Errorf("Expected SwitchASIC to be 'broadcom', got '%s'", info.SwitchASIC)
	}
	// Vendor should be inferred
	if info.Vendor != "Dell" {
		t.Errorf("Expected Vendor to be 'Dell', got '%s'", info.Vendor)
	}
}

func TestInferVendorFromPlatform(t *testing.T) {
	tests := []struct {
		platform string
		expected string
	}{
		{"x86_64-mlnx_msn4600c-r0", "Mellanox"},
		{"x86_64-arista_7060x6_64pe", "Arista"},
		{"x86_64-dell_s6100-r0", "Dell"},
		{"x86_64-cisco_8101-r0", "Cisco"},
		{"x86_64-nokia_7215-r0", "Nokia"},
		{"x86_64-celestica_e1031-r0", "Celestica"},
		{"x86_64-kvm_x86_64-r0", "unknown"},
		{"unknown_platform", "unknown"},
		{"", "unknown"},
	}

	for _, test := range tests {
		t.Run(test.platform, func(t *testing.T) {
			result := inferVendorFromPlatform(test.platform)
			if result != test.expected {
				t.Errorf("For platform '%s', expected vendor '%s', got '%s'", test.platform, test.expected, result)
			}
		})
	}
}

func TestGetPlatformInfo_FullFlow(t *testing.T) {
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
onie_switch_asic=mlnx
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

	// Verify results
	if info.Vendor != "Mellanox" {
		t.Errorf("Expected Vendor 'Mellanox', got '%s'", info.Vendor)
	}
	if info.Platform != "x86_64-mlnx_msn4600c-r0" {
		t.Errorf("Expected Platform 'x86_64-mlnx_msn4600c-r0', got '%s'", info.Platform)
	}
	if info.MachineID != "mlnx_msn4600c" {
		t.Errorf("Expected MachineID 'mlnx_msn4600c', got '%s'", info.MachineID)
	}
	if info.Architecture != "x86_64" {
		t.Errorf("Expected Architecture 'x86_64', got '%s'", info.Architecture)
	}
	if info.SwitchASIC != "mlnx" {
		t.Errorf("Expected SwitchASIC 'mlnx', got '%s'", info.SwitchASIC)
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

func TestReadMachineConf_ScannerError(t *testing.T) {
	// This test is tricky to implement as we need to simulate a scanner error
	// For now, we'll test the edge case with malformed lines
	content := `key1=value1
malformed line without equals
key2=value2
=missing_key
key3=`

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

	// Should have parsed valid lines only (including the empty value for key3)
	if len(configMap) != 4 {
		t.Errorf("Expected 4 valid entries, got %d", len(configMap))
	}
	if configMap["key1"] != "value1" {
		t.Errorf("Expected key1=value1, got key1=%s", configMap["key1"])
	}
	if configMap["key2"] != "value2" {
		t.Errorf("Expected key2=value2, got key2=%s", configMap["key2"])
	}
	if configMap["key3"] != "" {
		t.Errorf("Expected key3='', got key3=%s", configMap["key3"])
	}
	// The line "=missing_key" creates a key with empty name
	if _, exists := configMap[""]; !exists {
		t.Error("Expected empty key from '=missing_key' line")
	}
}

func TestGetPlatformInfo_KVMPlatform(t *testing.T) {
	// Create temp directory and machine.conf file
	tempDir := t.TempDir()
	machineConfPath := filepath.Join(tempDir, "host", "machine.conf")

	// Create directory
	err := os.MkdirAll(filepath.Dir(machineConfPath), 0755)
	if err != nil {
		t.Fatalf("Failed to create directory: %v", err)
	}

	// Write test machine.conf for KVM platform
	content := `onie_platform=x86_64-kvm_x86_64-r0
onie_machine=kvm_x86_64
onie_arch=x86_64
onie_switch_asic=qemu`

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

	// Verify results
	if info.Vendor != "KVM" {
		t.Errorf("Expected Vendor 'KVM', got '%s'", info.Vendor)
	}
	if info.Platform != "x86_64-kvm_x86_64-r0" {
		t.Errorf("Expected Platform 'x86_64-kvm_x86_64-r0', got '%s'", info.Platform)
	}
	if info.SwitchASIC != "qemu" {
		t.Errorf("Expected SwitchASIC 'qemu', got '%s'", info.SwitchASIC)
	}
}

func TestGetPlatformInfo_UnknownPlatform(t *testing.T) {
	// Create temp directory and machine.conf file
	tempDir := t.TempDir()
	machineConfPath := filepath.Join(tempDir, "host", "machine.conf")

	// Create directory
	err := os.MkdirAll(filepath.Dir(machineConfPath), 0755)
	if err != nil {
		t.Fatalf("Failed to create directory: %v", err)
	}

	// Write test machine.conf for unknown platform
	content := `onie_platform=x86_64-generic_platform-r0
onie_machine=generic_platform
onie_arch=x86_64
onie_switch_asic=unknown`

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

	// Verify results - should extract common info
	if info.Platform != "x86_64-generic_platform-r0" {
		t.Errorf("Expected Platform 'x86_64-generic_platform-r0', got '%s'", info.Platform)
	}
	if info.MachineID != "generic_platform" {
		t.Errorf("Expected MachineID 'generic_platform', got '%s'", info.MachineID)
	}
}

func TestInferAristaSwitchASIC_NoMatch(t *testing.T) {
	// Test with platform that doesn't match any known pattern
	asic := inferAristaSwitchASIC("x86_64-arista_unknown-r0")
	if asic != "unknown" {
		t.Errorf("Expected 'unknown' ASIC for unknown platform, got '%s'", asic)
	}
}

func TestExtractModelFromPlatform(t *testing.T) {
	tests := []struct {
		platform string
		expected string
	}{
		{"x86_64-mlnx_msn4600c-r0", "mlnx_msn4600c"},
		{"x86_64-dell_s6100-r0", "dell_s6100"},
		{"x86_64-arista_7060x6_64pe", "arista_7060x6_64pe"},
		{"", "unknown"},
		{"x86_64-r0", "unknown"},
	}

	for _, test := range tests {
		t.Run(test.platform, func(t *testing.T) {
			result := extractModelFromPlatform(test.platform)
			if result != test.expected {
				t.Errorf("For platform '%s', expected model '%s', got '%s'", test.platform, test.expected, result)
			}
		})
	}
}

func TestHandleUnknownPlatform(t *testing.T) {
	tests := []struct {
		name               string
		info               *PlatformInfo
		expectedIdentifier string
	}{
		{
			name: "Unknown vendor with valid platform",
			info: &PlatformInfo{
				Vendor:   "custom_vendor",
				Platform: "x86_64-custom_model-r0",
			},
			expectedIdentifier: "custom_vendor_custom_model",
		},
		{
			name: "Empty vendor",
			info: &PlatformInfo{
				Vendor:   "",
				Platform: "x86_64-some_model-r0",
			},
			expectedIdentifier: "unknown_some_model",
		},
		{
			name: "Unknown vendor string",
			info: &PlatformInfo{
				Vendor:   "unknown",
				Platform: "some_platform",
			},
			expectedIdentifier: "unknown_some_platform",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			identifier := handleUnknownPlatform(test.info)
			if identifier != test.expectedIdentifier {
				t.Errorf("Expected identifier '%s', got '%s'", test.expectedIdentifier, identifier)
			}
		})
	}
}

func TestGetPlatformIdentifierString_AllVendors(t *testing.T) {
	tests := []struct {
		name               string
		info               *PlatformInfo
		expectedIdentifier string
	}{
		{
			name: "Cisco platform",
			info: &PlatformInfo{
				Vendor:   "cisco",
				Platform: "x86_64-cisco_8101-r0",
			},
			expectedIdentifier: "cisco_8101",
		},
		{
			name: "Nokia platform",
			info: &PlatformInfo{
				Vendor:   "nokia",
				Platform: "x86_64-nokia_7215-r0",
			},
			expectedIdentifier: "nokia_7215",
		},
		{
			name: "Celestica platform",
			info: &PlatformInfo{
				Vendor:   "celestica",
				Platform: "x86_64-celestica_e1031-r0",
			},
			expectedIdentifier: "celestica_e1031",
		},
		{
			name: "KVM platform",
			info: &PlatformInfo{
				Vendor:   "KVM",
				Platform: "x86_64-kvm_x86_64-r0",
			},
			expectedIdentifier: "x86_64-kvm_x86_64-r0",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			identifier := GetPlatformIdentifierString(test.info)
			if identifier != test.expectedIdentifier {
				t.Errorf("Expected identifier '%s', got '%s'", test.expectedIdentifier, identifier)
			}
		})
	}
}
