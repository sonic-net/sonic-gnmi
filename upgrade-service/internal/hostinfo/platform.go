package hostinfo

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/golang/glog"
)

// PlatformInfoProvider defines the interface for getting platform information
type PlatformInfoProvider interface {
	// GetPlatformInfo returns platform information
	GetPlatformInfo(ctx context.Context) (*PlatformInfo, error)

	// GetPlatformIdentifier returns the platform identifier string, vendor and model
	GetPlatformIdentifier(ctx context.Context, info *PlatformInfo) (platformIdentifier string, vendor string, model string)
}

// DefaultPlatformInfoProvider implements the PlatformInfoProvider interface
type DefaultPlatformInfoProvider struct{}

// NewPlatformInfoProvider creates a new instance of DefaultPlatformInfoProvider
func NewPlatformInfoProvider() PlatformInfoProvider {
	return &DefaultPlatformInfoProvider{}
}

const (
	// MachineConfPath is the path to the machine.conf file
	MachineConfPath = "/host/machine.conf"
)

// PlatformInfo contains information about the platform
type PlatformInfo struct {
	// Raw key-value pairs from machine.conf
	ConfigMap map[string]string

	// Common fields across different platforms
	Vendor       string
	Platform     string
	MachineID    string
	Architecture string
	SwitchASIC   string
}

// GetPlatformInfo reads the machine.conf file and returns platform information
func GetPlatformInfo() (*PlatformInfo, error) {
	glog.V(2).Infof("Reading platform information from %s", MachineConfPath)
	
	// Read the machine.conf file
	configMap, err := readMachineConf(MachineConfPath)
	if err != nil {
		glog.Errorf("Failed to read machine.conf from %s: %v", MachineConfPath, err)
		return nil, fmt.Errorf("failed to read machine.conf: %w", err)
	}

	glog.V(3).Infof("Successfully read %d configuration entries from machine.conf", len(configMap))
	for key, value := range configMap {
		glog.V(3).Infof("Config: %s=%s", key, value)
	}

	// Initialize platform info with the raw config
	info := &PlatformInfo{
		ConfigMap: configMap,
	}

	// Determine vendor and extract relevant information
	if isMellanox(configMap) {
		glog.V(2).Info("Detected Mellanox platform")
		extractMellanoxInfo(configMap, info)
	} else if isArista(configMap) {
		glog.V(2).Info("Detected Arista platform")
		extractAristaInfo(configMap, info)
	} else if isKVM(configMap) {
		glog.V(2).Info("Detected KVM virtual switch platform")
		extractKVMInfo(configMap, info)
	} else {
		glog.V(2).Info("Platform not specifically recognized, using common extraction")
		// For other platforms, try to extract common information
		extractCommonInfo(configMap, info)
	}

	glog.V(2).Infof("Platform detection complete: vendor=%s, platform=%s, machine=%s", 
		info.Vendor, info.Platform, info.MachineID)

	return info, nil
}

// readMachineConf reads the machine.conf file and returns a map of key-value pairs
func readMachineConf(path string) (map[string]string, error) {
	glog.V(3).Infof("Opening machine.conf file at %s", path)
	file, err := os.Open(path)
	if err != nil {
		glog.Errorf("Failed to open machine.conf file at %s: %v", path, err)
		return nil, err
	}
	defer file.Close()

	configMap := make(map[string]string)
	scanner := bufio.NewScanner(file)
	lineCount := 0

	for scanner.Scan() {
		lineCount++
		line := scanner.Text()
		glog.V(4).Infof("Reading line %d: %s", lineCount, line)
		
		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			glog.V(4).Infof("Skipping line %d (empty or comment)", lineCount)
			continue
		}

		// Split line by '=' and handle key-value pairs
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			configMap[key] = value
			glog.V(4).Infof("Parsed config line %d: %s=%s", lineCount, key, value)
		} else {
			glog.V(3).Infof("Skipping malformed line %d: %s", lineCount, line)
		}
	}

	if err := scanner.Err(); err != nil {
		glog.Errorf("Error reading machine.conf file: %v", err)
		return nil, err
	}

	glog.V(3).Infof("Successfully parsed %d lines, extracted %d configuration entries", lineCount, len(configMap))
	return configMap, nil
}

// isMellanox checks if the platform is Mellanox based on the config
func isMellanox(configMap map[string]string) bool {
	asic, exists := configMap["onie_switch_asic"]
	return exists && asic == "mlnx"
}

// isArista checks if the platform is Arista based on the config
func isArista(configMap map[string]string) bool {
	_, exists := configMap["aboot_vendor"]
	return exists && configMap["aboot_vendor"] == "arista"
}

// isKVM checks if the platform is KVM (virtual switch) based on the config
func isKVM(configMap map[string]string) bool {
	// Check for KVM indicators in the platform or machine configuration
	platform, platformExists := configMap["onie_platform"]
	machine, machineExists := configMap["onie_machine"]
	asic, asicExists := configMap["onie_switch_asic"]
	
	// KVM virtual switch typically has:
	// - onie_platform containing "kvm"
	// - onie_machine containing "kvm"
	// - onie_switch_asic = "qemu"
	if platformExists && strings.Contains(strings.ToLower(platform), "kvm") {
		return true
	}
	if machineExists && strings.Contains(strings.ToLower(machine), "kvm") {
		return true
	}
	if asicExists && asic == "qemu" {
		return true
	}
	
	return false
}

// extractMellanoxInfo extracts Mellanox-specific information
func extractMellanoxInfo(configMap map[string]string, info *PlatformInfo) {
	glog.V(3).Info("Extracting Mellanox-specific platform information")
	info.Vendor = "Mellanox"
	info.Platform = configMap["onie_platform"]
	info.MachineID = configMap["onie_machine"]
	info.Architecture = configMap["onie_arch"]
	info.SwitchASIC = configMap["onie_switch_asic"]
	
	glog.V(3).Infof("Mellanox info extracted: platform=%s, machine=%s, arch=%s, asic=%s", 
		info.Platform, info.MachineID, info.Architecture, info.SwitchASIC)
}

// extractAristaInfo extracts Arista-specific information
func extractAristaInfo(configMap map[string]string, info *PlatformInfo) {
	glog.V(3).Info("Extracting Arista-specific platform information")
	info.Vendor = configMap["aboot_vendor"]
	info.Platform = configMap["aboot_platform"]
	info.MachineID = configMap["aboot_machine"]
	info.Architecture = configMap["aboot_arch"]
	// Arista doesn't have switch_asic field in machine.conf
	// We can infer it based on the platform if needed
	info.SwitchASIC = inferAristaSwitchASIC(configMap["aboot_platform"])
	
	glog.V(3).Infof("Arista info extracted: platform=%s, machine=%s, arch=%s, inferred_asic=%s", 
		info.Platform, info.MachineID, info.Architecture, info.SwitchASIC)
}

// extractKVMInfo extracts KVM virtual switch specific information
func extractKVMInfo(configMap map[string]string, info *PlatformInfo) {
	glog.V(3).Info("Extracting KVM virtual switch platform information")
	info.Vendor = "KVM"
	info.Platform = configMap["onie_platform"]
	info.MachineID = configMap["onie_machine"]
	info.Architecture = configMap["onie_arch"]
	info.SwitchASIC = configMap["onie_switch_asic"]
	
	glog.V(3).Infof("KVM info extracted: platform=%s, machine=%s, arch=%s, asic=%s", 
		info.Platform, info.MachineID, info.Architecture, info.SwitchASIC)
}

// extractCommonInfo tries to extract common information for other platforms
func extractCommonInfo(configMap map[string]string, info *PlatformInfo) {
	glog.V(3).Info("Extracting common platform information using ONIE fields")
	
	// Try common ONIE fields first
	if platform, exists := configMap["onie_platform"]; exists {
		info.Platform = platform
		glog.V(4).Infof("Found onie_platform: %s", platform)
	}
	if machine, exists := configMap["onie_machine"]; exists {
		info.MachineID = machine
		glog.V(4).Infof("Found onie_machine: %s", machine)
	}
	if arch, exists := configMap["onie_arch"]; exists {
		info.Architecture = arch
		glog.V(4).Infof("Found onie_arch: %s", arch)
	}
	if asic, exists := configMap["onie_switch_asic"]; exists {
		info.SwitchASIC = asic
		glog.V(4).Infof("Found onie_switch_asic: %s", asic)
	}

	// If we couldn't determine the vendor, try to infer it
	inferredVendor := inferVendorFromPlatform(info.Platform)
	info.Vendor = inferredVendor
	glog.V(3).Infof("Inferred vendor from platform '%s': %s", info.Platform, inferredVendor)
	
	glog.V(3).Infof("Common info extracted: vendor=%s, platform=%s, machine=%s, arch=%s, asic=%s", 
		info.Vendor, info.Platform, info.MachineID, info.Architecture, info.SwitchASIC)
}

// inferAristaSwitchASIC infers the switch ASIC for Arista platforms
func inferAristaSwitchASIC(platform string) string {
	// This is a simplification - in reality, this would need more logic
	if strings.Contains(platform, "7060") {
		return "broadcom" // Most 7060 models use Broadcom ASICs
	}
	return "unknown"
}

// inferVendorFromPlatform tries to infer the vendor from the platform string
func inferVendorFromPlatform(platform string) string {
	if platform == "" {
		return "unknown"
	}

	platformLower := strings.ToLower(platform)
	if strings.Contains(platformLower, "mlnx") {
		return "Mellanox"
	}
	if strings.Contains(platformLower, "arista") {
		return "Arista"
	}
	if strings.Contains(platformLower, "dell") {
		return "Dell"
	}
	if strings.Contains(platformLower, "cisco") {
		return "Cisco"
	}
	if strings.Contains(platformLower, "celestica") {
		return "Celestica"
	}
	if strings.Contains(platformLower, "nokia") {
		return "Nokia"
	}

	return "unknown"
}

// GetPlatformInfo returns platform information from the host
func (p *DefaultPlatformInfoProvider) GetPlatformInfo(ctx context.Context) (*PlatformInfo, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		// Continue with normal operation
	}
	return GetPlatformInfo()
}

// GetPlatformIdentifier returns the platform identifier, vendor and model as strings
func (p *DefaultPlatformInfoProvider) GetPlatformIdentifier(ctx context.Context, info *PlatformInfo) (string, string, string) {
	select {
	case <-ctx.Done():
		return "unknown", "unknown", "unknown"
	default:
		// Continue with normal operation
	}
	return GetPlatformIdentifierString(info)
}

// GetPlatformIdentifierString determines the platform identifier, vendor and model strings
// based on the platform information.
func GetPlatformIdentifierString(info *PlatformInfo) (string, string, string) {
	glog.V(2).Info("Determining platform identifier from platform info")
	
	// If info is nil, return unknown
	if info == nil {
		glog.V(2).Info("Platform info is nil, returning unknown values")
		return "unknown", "unknown", "unknown"
	}

	// Initialize default values
	platformIdentifier := "unknown"
	vendor := info.Vendor
	model := "unknown"

	glog.V(3).Infof("Starting platform identification: vendor=%s, platform=%s", vendor, info.Platform)

	// Normalize platform string for more consistent matching
	platformLower := ""
	if info.Platform != "" {
		platformLower = strings.ToLower(info.Platform)
		glog.V(4).Infof("Platform string normalized: '%s' -> '%s'", info.Platform, platformLower)
	}

	// Process based on vendor
	if info.Vendor == "Mellanox" && info.SwitchASIC == "mlnx" {
		glog.V(3).Info("Processing Mellanox platform identification")
		// Check for specific Mellanox models
		if strings.Contains(platformLower, "msn2700") || strings.Contains(platformLower, "sn2700") {
			model = "sn2700"
			platformIdentifier = "mellanox_sn2700"
			glog.V(3).Info("Identified as Mellanox SN2700")
		} else if strings.Contains(platformLower, "msn3800") || strings.Contains(platformLower, "sn3800") {
			model = "sn3800"
			platformIdentifier = "mellanox_sn3800"
			glog.V(3).Info("Identified as Mellanox SN3800")
		} else if strings.Contains(platformLower, "msn4600") || strings.Contains(platformLower, "sn4600") {
			model = "sn4600"
			platformIdentifier = "mellanox_sn4600"
			glog.V(3).Info("Identified as Mellanox SN4600")
		} else {
			// Generic Mellanox identifier with the platform name
			model = extractModelFromPlatform(platformLower)
			platformIdentifier = "mellanox_" + model
			glog.V(3).Infof("Generic Mellanox identification: model=%s, identifier=%s", model, platformIdentifier)
		}
	} else if info.Vendor == "arista" || info.Vendor == "Arista" {
		vendor = "arista" // Normalize vendor name
		// Check for specific model numbers
		if strings.Contains(platformLower, "7050") {
			model = "7050"
			platformIdentifier = "arista_7050"
		} else if strings.Contains(platformLower, "7060") {
			model = "7060"
			platformIdentifier = "arista_7060"
		} else if strings.Contains(platformLower, "7260") {
			model = "7260"
			platformIdentifier = "arista_7260"
		} else {
			// Generic Arista identifier with the model number extracted from platform
			model = extractModelFromPlatform(platformLower)
			platformIdentifier = "arista_" + model
		}
	} else if info.Vendor == "Dell" {
		vendor = "dell" // Normalize vendor name
		if strings.Contains(platformLower, "s6000") || strings.Contains(platformLower, "s-6000") {
			model = "s6000"
			platformIdentifier = "dell_s6000"
		} else if strings.Contains(platformLower, "s6100") || strings.Contains(platformLower, "s-6100") {
			model = "s6100"
			platformIdentifier = "dell_s6100"
		} else {
			// Generic Dell identifier with the model number extracted from platform
			model = extractModelFromPlatform(platformLower)
			platformIdentifier = "dell_" + model
		}
	} else if info.Vendor == "Cisco" {
		vendor = "cisco" // Normalize vendor name
		if strings.Contains(platformLower, "8101") {
			model = "8101"
			platformIdentifier = "cisco_8101"
		} else if strings.Contains(platformLower, "8102") {
			model = "8102"
			platformIdentifier = "cisco_8102"
		} else if strings.Contains(platformLower, "8111") {
			model = "8111"
			platformIdentifier = "cisco_8111"
		} else {
			// Generic Cisco identifier with the model number extracted from platform
			model = extractModelFromPlatform(platformLower)
			platformIdentifier = "cisco_" + model
		}
	} else if info.Vendor == "Nokia" {
		vendor = "nokia" // Normalize vendor name
		if strings.Contains(platformLower, "7215") {
			model = "7215"
			platformIdentifier = "nokia_7215"
		} else {
			// Generic Nokia identifier with the model number extracted from platform
			model = extractModelFromPlatform(platformLower)
			platformIdentifier = "nokia_" + model
		}
	} else if info.Vendor == "Celestica" {
		vendor = "celestica" // Normalize vendor name
		if strings.Contains(platformLower, "e1031") || strings.Contains(platformLower, "e-1031") {
			model = "e1031"
			platformIdentifier = "celestica_e1031"
		} else {
			// Generic Celestica identifier with the model number extracted from platform
			model = extractModelFromPlatform(platformLower)
			platformIdentifier = "celestica_" + model
		}
	} else if info.Vendor == "KVM" {
		glog.V(3).Info("Processing KVM virtual switch platform identification")
		vendor = "kvm" // Normalize vendor name
		
		// KVM virtual switch identification - use platform from machine.conf
		model = "unknown"
		platformIdentifier = info.Platform // Use the actual platform identifier from machine.conf
		glog.V(3).Infof("KVM platform detected: %s", platformIdentifier)
	} else {
		// If we couldn't determine the vendor or it's a new one, use what we have
		if vendor == "" || vendor == "unknown" {
			vendor = "unknown"
		} else {
			vendor = strings.ToLower(vendor) // Normalize vendor name
		}

		// Extract model from platform if possible
		model = extractModelFromPlatform(platformLower)
		if model != "unknown" {
			platformIdentifier = vendor + "_" + model
		}
	}

	glog.V(2).Infof("Platform identification complete: identifier=%s, vendor=%s, model=%s", 
		platformIdentifier, vendor, model)
	return platformIdentifier, vendor, model
}

// extractModelFromPlatform tries to extract a model identifier from the platform string
func extractModelFromPlatform(platformLower string) string {
	// This is a simplistic implementation that can be enhanced based on actual platform naming patterns

	// Try to find a model number in the platform string
	// Common patterns include digits followed by letters or other digits
	for _, pattern := range []string{
		"[0-9]+[a-z][0-9]+", // e.g., "7050x"
		"[0-9]+",            // e.g., "7050"
		"[a-z][0-9]+",       // e.g., "s6100"
	} {
		// In a real implementation, you would use proper regex matching here
		// For simplicity, we're just doing basic contains checks
		if strings.Contains(platformLower, pattern) {
			return pattern // This is simplified, would return the actual match
		}
	}

	// If we couldn't extract a model, extract some meaningful part of the platform name
	// This is a very simplistic approach and would need refinement in production
	parts := strings.Split(platformLower, "-")
	for _, part := range parts {
		if part != "x86_64" && part != "r0" && part != "" {
			return part
		}
	}

	return "unknown"
}
