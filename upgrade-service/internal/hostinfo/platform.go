package hostinfo

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/golang/glog"
	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/config"
)

// PlatformInfoProvider defines the interface for getting platform information.
type PlatformInfoProvider interface {
	// GetPlatformInfo returns platform information
	GetPlatformInfo(ctx context.Context) (*PlatformInfo, error)

	// GetPlatformIdentifier returns the platform identifier string, vendor and model
	GetPlatformIdentifier(ctx context.Context, info *PlatformInfo) (platformIdentifier string, vendor string, model string)
}

// DefaultPlatformInfoProvider implements the PlatformInfoProvider interface.
type DefaultPlatformInfoProvider struct{}

// NewPlatformInfoProvider creates a new instance of DefaultPlatformInfoProvider.
func NewPlatformInfoProvider() PlatformInfoProvider {
	return &DefaultPlatformInfoProvider{}
}

// getMachineConfPath returns the path to the machine.conf file based on current config.
func getMachineConfPath() string {
	return config.GetHostPath("/host/machine.conf")
}

// PlatformInfo contains information about the platform.
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

// GetPlatformInfo reads the machine.conf file and returns platform information.
func GetPlatformInfo() (*PlatformInfo, error) {
	machineConfPath := getMachineConfPath()
	glog.V(2).Infof("Reading platform information from %s", machineConfPath)

	// Read the machine.conf file
	configMap, err := readMachineConf(machineConfPath)
	if err != nil {
		glog.Errorf("Failed to read machine.conf from %s: %v", machineConfPath, err)
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

// readMachineConf reads the machine.conf file and returns a map of key-value pairs.
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

// isMellanox checks if the platform is Mellanox based on the config.
func isMellanox(configMap map[string]string) bool {
	asic, exists := configMap["onie_switch_asic"]
	return exists && asic == "mlnx"
}

// isArista checks if the platform is Arista based on the config.
func isArista(configMap map[string]string) bool {
	_, exists := configMap["aboot_vendor"]
	return exists && configMap["aboot_vendor"] == "arista"
}

// isKVM checks if the platform is KVM (virtual switch) based on the config.
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

// extractMellanoxInfo extracts Mellanox-specific information.
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

// extractAristaInfo extracts Arista-specific information.
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

// extractKVMInfo extracts KVM virtual switch specific information.
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

// extractCommonInfo tries to extract common information for other platforms.
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

// inferAristaSwitchASIC infers the switch ASIC for Arista platforms.
func inferAristaSwitchASIC(platform string) string {
	// This is a simplification - in reality, this would need more logic
	if strings.Contains(platform, "7060") {
		return "broadcom" // Most 7060 models use Broadcom ASICs
	}
	return "unknown"
}

// ModelPattern defines how to match and extract model information from platform strings.
type ModelPattern struct {
	Keywords []string // Platform keywords to match
	Model    string   // Resulting model name
}

// VendorConfig contains the configuration for identifying a specific vendor's platforms.
type VendorConfig struct {
	Name       string                     // Vendor name for output
	Validators []func(*PlatformInfo) bool // Custom validation rules
	Models     []ModelPattern             // Model patterns to match
}

// platformConfigs defines the configuration for all supported platform vendors.
var platformConfigs = []VendorConfig{
	{
		Name: "Mellanox",
		Validators: []func(*PlatformInfo) bool{
			func(info *PlatformInfo) bool {
				return info.Vendor == "Mellanox" && info.SwitchASIC == "mlnx"
			},
		},
		Models: []ModelPattern{
			{Keywords: []string{"msn2700", "sn2700"}, Model: "sn2700"},
			{Keywords: []string{"msn3800", "sn3800"}, Model: "sn3800"},
			{Keywords: []string{"msn4600", "sn4600"}, Model: "sn4600"},
		},
	},
	{
		Name: "arista",
		Validators: []func(*PlatformInfo) bool{
			func(info *PlatformInfo) bool {
				return strings.ToLower(info.Vendor) == "arista"
			},
		},
		Models: []ModelPattern{
			{Keywords: []string{"7050"}, Model: "7050"},
			{Keywords: []string{"7060"}, Model: "7060"},
			{Keywords: []string{"7260"}, Model: "7260"},
		},
	},
	{
		Name: "dell",
		Validators: []func(*PlatformInfo) bool{
			func(info *PlatformInfo) bool {
				return strings.ToLower(info.Vendor) == "dell"
			},
		},
		Models: []ModelPattern{
			{Keywords: []string{"s6000", "s-6000"}, Model: "s6000"},
			{Keywords: []string{"s6100", "s-6100"}, Model: "s6100"},
		},
	},
	{
		Name: "cisco",
		Validators: []func(*PlatformInfo) bool{
			func(info *PlatformInfo) bool {
				return strings.ToLower(info.Vendor) == "cisco"
			},
		},
		Models: []ModelPattern{
			{Keywords: []string{"8101"}, Model: "8101"},
			{Keywords: []string{"8102"}, Model: "8102"},
			{Keywords: []string{"8111"}, Model: "8111"},
		},
	},
	{
		Name: "nokia",
		Validators: []func(*PlatformInfo) bool{
			func(info *PlatformInfo) bool {
				return strings.ToLower(info.Vendor) == "nokia"
			},
		},
		Models: []ModelPattern{
			{Keywords: []string{"7215"}, Model: "7215"},
		},
	},
	{
		Name: "celestica",
		Validators: []func(*PlatformInfo) bool{
			func(info *PlatformInfo) bool {
				return strings.ToLower(info.Vendor) == "celestica"
			},
		},
		Models: []ModelPattern{
			{Keywords: []string{"e1031", "e-1031"}, Model: "e1031"},
		},
	},
	{
		Name: "kvm",
		Validators: []func(*PlatformInfo) bool{
			func(info *PlatformInfo) bool {
				return strings.ToLower(info.Vendor) == "kvm"
			},
		},
		Models: []ModelPattern{
			// KVM uses platform identifier directly
		},
	},
}

// matchesVendor checks if the platform info matches all validator functions for a vendor.
func matchesVendor(info *PlatformInfo, validators []func(*PlatformInfo) bool) bool {
	for _, validator := range validators {
		if !validator(info) {
			return false
		}
	}
	return true
}

// findModel searches for a matching model pattern in the platform string.
func findModel(platformLower string, models []ModelPattern) string {
	for _, pattern := range models {
		for _, keyword := range pattern.Keywords {
			if strings.Contains(platformLower, keyword) {
				glog.V(4).Infof("Found model match: keyword=%s, model=%s", keyword, pattern.Model)
				return pattern.Model
			}
		}
	}
	// Fallback to generic extraction
	return extractModelFromPlatform(platformLower)
}

// handleUnknownPlatform processes platforms that don't match any known vendor configuration.
func handleUnknownPlatform(info *PlatformInfo) (string, string, string) {
	glog.V(3).Info("Processing unknown platform using fallback logic")

	vendor := info.Vendor
	if vendor == "" || vendor == "unknown" {
		vendor = "unknown"
	} else {
		vendor = strings.ToLower(vendor) // Normalize vendor name
	}

	// Extract model from platform if possible
	platformLower := strings.ToLower(info.Platform)
	model := extractModelFromPlatform(platformLower)

	platformIdentifier := "unknown"
	if model != "unknown" {
		platformIdentifier = vendor + "_" + model
	}

	glog.V(3).Infof("Unknown platform result: identifier=%s, vendor=%s, model=%s",
		platformIdentifier, vendor, model)
	return platformIdentifier, vendor, model
}

// inferVendorFromPlatform tries to infer the vendor from the platform string.
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

// GetPlatformInfo returns platform information from the host.
func (p *DefaultPlatformInfoProvider) GetPlatformInfo(ctx context.Context) (*PlatformInfo, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		// Continue with normal operation
	}
	return GetPlatformInfo()
}

// GetPlatformIdentifier returns the platform identifier, vendor and model as strings.
func (p *DefaultPlatformInfoProvider) GetPlatformIdentifier(
	ctx context.Context, info *PlatformInfo,
) (string, string, string) {
	select {
	case <-ctx.Done():
		return "unknown", "unknown", "unknown"
	default:
		// Continue with normal operation
	}
	return GetPlatformIdentifierString(info)
}

// GetPlatformIdentifierString determines the platform identifier, vendor and model strings
// based on the platform information using a config-driven approach.
func GetPlatformIdentifierString(info *PlatformInfo) (string, string, string) {
	glog.V(2).Info("Determining platform identifier from platform info")

	// If info is nil, return unknown
	if info == nil {
		glog.V(2).Info("Platform info is nil, returning unknown values")
		return "unknown", "unknown", "unknown"
	}

	glog.V(3).Infof("Starting platform identification: vendor=%s, platform=%s", info.Vendor, info.Platform)

	// Normalize platform string for consistent matching
	platformLower := strings.ToLower(info.Platform)
	glog.V(4).Infof("Platform string normalized: '%s' -> '%s'", info.Platform, platformLower)

	// Iterate through configured vendors to find a match
	for _, config := range platformConfigs {
		if matchesVendor(info, config.Validators) {
			glog.V(3).Infof("Processing %s platform identification", config.Name)

			// Handle special case for KVM
			if strings.ToLower(config.Name) == "kvm" {
				glog.V(3).Infof("KVM platform detected: %s", info.Platform)
				return info.Platform, "kvm", "unknown"
			}

			// Find model using configuration
			model := findModel(platformLower, config.Models)
			if model != "" {
				vendor := config.Name
				// Special case: Mellanox keeps original case, others are lowercase
				if strings.ToLower(config.Name) != "mellanox" {
					vendor = strings.ToLower(config.Name)
				}
				identifier := strings.ToLower(config.Name) + "_" + model
				glog.V(2).Infof("Platform identification complete: identifier=%s, vendor=%s, model=%s",
					identifier, vendor, model)
				return identifier, vendor, model
			}
		}
	}

	// No matching vendor configuration found, use fallback logic
	glog.V(3).Info("No vendor configuration matched, using fallback logic")
	return handleUnknownPlatform(info)
}

// extractModelFromPlatform tries to extract a model identifier from the platform string.
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
