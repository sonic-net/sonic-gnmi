// Package hostinfo provides utilities for detecting SONiC platform information
// from the host system by reading machine.conf.
package hostinfo

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/golang/glog"
	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/config"
	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/paths"
)

// getMachineConfPath returns the path to the machine.conf file based on current config.
func getMachineConfPath() string {
	return paths.ToHost("/machine.conf", config.Global.RootFS)
}

// PlatformInfo contains basic platform information from machine.conf.
type PlatformInfo struct {
	// ConfigMap contains raw key-value pairs from machine.conf
	ConfigMap map[string]string
	// Platform is the primary platform identifier
	Platform string
}

// GetPlatformInfo reads the machine.conf file and returns basic platform information.
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

	// Extract platform identifier - try multiple common fields
	platform := extractPlatform(configMap)
	glog.V(2).Infof("Platform detection complete: platform=%s", platform)

	return &PlatformInfo{
		ConfigMap: configMap,
		Platform:  platform,
	}, nil
}

// extractPlatform extracts the platform identifier from the config map.
func extractPlatform(configMap map[string]string) string {
	// Try common platform fields in order of preference
	platformFields := []string{
		"onie_platform",
		"aboot_platform",
		"onie_machine",
		"aboot_machine",
	}

	for _, field := range platformFields {
		if platform, exists := configMap[field]; exists && platform != "" {
			glog.V(3).Infof("Found platform from %s: %s", field, platform)
			return platform
		}
	}

	glog.V(2).Info("No platform field found, returning unknown")
	return "unknown"
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
			continue
		}

		// Split line by '=' and handle key-value pairs
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			configMap[key] = value
			glog.V(4).Infof("Parsed config line %d: %s=%s", lineCount, key, value)
		}
	}

	if err := scanner.Err(); err != nil {
		glog.Errorf("Error reading machine.conf file: %v", err)
		return nil, err
	}

	glog.V(3).Infof("Successfully parsed %d lines, extracted %d configuration entries", lineCount, len(configMap))
	return configMap, nil
}

// GetPlatformIdentifierString returns the platform identifier string.
func GetPlatformIdentifierString(info *PlatformInfo) string {
	if info == nil {
		glog.V(2).Info("Platform info is nil, returning unknown")
		return "unknown"
	}

	glog.V(2).Infof("Platform identifier: %s", info.Platform)
	return info.Platform
}
