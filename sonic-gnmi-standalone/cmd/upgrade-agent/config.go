package main

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// YAMLConfig represents the YAML configuration for upgrade-agent.
// This implements the upgrade.Config interface for YAML-based configurations.
type YAMLConfig struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Package struct {
			URL      string `yaml:"url"`
			Filename string `yaml:"filename"`
			MD5      string `yaml:"md5"`
			Version  string `yaml:"version,omitempty"`
			Activate bool   `yaml:"activate,omitempty"`
		} `yaml:"package"`
		Server struct {
			Address string `yaml:"address"`
			TLS     bool   `yaml:"tls,omitempty"`
		} `yaml:"server"`
	} `yaml:"spec"`
}

// LoadConfigFromFile loads a YAML configuration file and returns a YAMLConfig.
func LoadConfigFromFile(path string) (*YAMLConfig, error) {
	// Validate file exists and is readable
	if err := validateConfigFile(path); err != nil {
		return nil, err
	}

	// Read file content
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Parse YAML
	var config YAMLConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	// Validate YAML structure
	if err := validateYAMLStructure(&config); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &config, nil
}

// validateConfigFile validates that the config file exists and is readable.
func validateConfigFile(path string) error {
	if path == "" {
		return fmt.Errorf("config file path cannot be empty")
	}

	// Check if file exists
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("config file '%s' does not exist", path)
		}
		return fmt.Errorf("cannot access config file '%s': %w", path, err)
	}

	// Check if it's a regular file
	if !info.Mode().IsRegular() {
		return fmt.Errorf("config file '%s' is not a regular file", path)
	}

	// Check if we can read it
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("cannot read config file '%s': %w", path, err)
	}
	file.Close()

	return nil
}

// validateYAMLStructure validates the YAML structure and required fields.
func validateYAMLStructure(config *YAMLConfig) error {
	// Check required fields
	if config.APIVersion == "" {
		return fmt.Errorf("apiVersion is required")
	}
	if config.APIVersion != "sonic.net/v1" {
		return fmt.Errorf("unsupported apiVersion: %s (expected sonic.net/v1)", config.APIVersion)
	}
	if config.Kind != "PackageConfig" {
		return fmt.Errorf("invalid kind: %s (expected PackageConfig)", config.Kind)
	}
	if config.Metadata.Name == "" {
		return fmt.Errorf("metadata.name is required")
	}

	// Validate package spec
	if config.Spec.Package.URL == "" {
		return fmt.Errorf("spec.package.url is required")
	}
	if config.Spec.Package.Filename == "" {
		return fmt.Errorf("spec.package.filename is required")
	}
	if config.Spec.Package.MD5 == "" {
		return fmt.Errorf("spec.package.md5 is required")
	}

	// Validate server spec
	if config.Spec.Server.Address == "" {
		return fmt.Errorf("spec.server.address is required")
	}

	return nil
}

// GetPackageURL implements upgrade.Config interface.
func (c *YAMLConfig) GetPackageURL() string {
	return c.Spec.Package.URL
}

// GetFilename implements upgrade.Config interface.
func (c *YAMLConfig) GetFilename() string {
	return c.Spec.Package.Filename
}

// GetMD5 implements upgrade.Config interface.
func (c *YAMLConfig) GetMD5() string {
	return c.Spec.Package.MD5
}

// GetVersion implements upgrade.Config interface.
func (c *YAMLConfig) GetVersion() string {
	return c.Spec.Package.Version
}

// GetActivate implements upgrade.Config interface.
func (c *YAMLConfig) GetActivate() bool {
	return c.Spec.Package.Activate
}

// GetServerAddress implements upgrade.Config interface.
func (c *YAMLConfig) GetServerAddress() string {
	return c.Spec.Server.Address
}

// GetTLS implements upgrade.Config interface.
func (c *YAMLConfig) GetTLS() bool {
	return c.Spec.Server.TLS
}
