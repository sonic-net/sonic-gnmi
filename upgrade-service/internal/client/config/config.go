package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config represents the complete upgrade configuration.
type Config struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Metadata   Metadata `yaml:"metadata"`
	Spec       Spec     `yaml:"spec"`
}

// Metadata contains metadata about the configuration.
type Metadata struct {
	Name string `yaml:"name"`
}

// Spec contains the upgrade specification.
type Spec struct {
	Firmware FirmwareSpec `yaml:"firmware"`
	Download DownloadSpec `yaml:"download,omitempty"`
	Server   ServerSpec   `yaml:"server"`
}

// FirmwareSpec defines the firmware upgrade parameters.
type FirmwareSpec struct {
	DesiredVersion string `yaml:"desiredVersion"`
	DownloadURL    string `yaml:"downloadUrl"`
	SavePath       string `yaml:"savePath,omitempty"`
	ExpectedMD5    string `yaml:"expectedMd5,omitempty"`
}

// DownloadSpec defines download behavior.
type DownloadSpec struct {
	ConnectTimeout int `yaml:"connectTimeout,omitempty"`
	TotalTimeout   int `yaml:"totalTimeout,omitempty"`
}

// ServerSpec defines the upgrade server connection parameters.
type ServerSpec struct {
	Address     string `yaml:"address"`
	TLSEnabled  *bool  `yaml:"tlsEnabled,omitempty"`
	TLSCertFile string `yaml:"tlsCertFile,omitempty"`
	TLSKeyFile  string `yaml:"tlsKeyFile,omitempty"`
}

// SetDefaults applies default values to optional fields.
func (c *Config) SetDefaults() {
	// Default save path
	if c.Spec.Firmware.SavePath == "" {
		c.Spec.Firmware.SavePath = "/host"
	}

	// Default timeouts - reduced for better UX
	if c.Spec.Download.ConnectTimeout == 0 {
		c.Spec.Download.ConnectTimeout = 5 // Reduced from 30 to 5 seconds
	}
	if c.Spec.Download.TotalTimeout == 0 {
		c.Spec.Download.TotalTimeout = 300
	}

	// Default TLS enabled
	if c.Spec.Server.TLSEnabled == nil {
		enabled := true
		c.Spec.Server.TLSEnabled = &enabled
	}
}

// Validate checks if the configuration is valid.
func (c *Config) Validate() error {
	// Check required fields
	if c.APIVersion == "" {
		return fmt.Errorf("apiVersion is required")
	}
	if c.APIVersion != "sonic.net/v1" {
		return fmt.Errorf("unsupported apiVersion: %s (expected sonic.net/v1)", c.APIVersion)
	}
	if c.Kind != "UpgradeConfig" {
		return fmt.Errorf("invalid kind: %s (expected UpgradeConfig)", c.Kind)
	}
	if c.Metadata.Name == "" {
		return fmt.Errorf("metadata.name is required")
	}

	// Validate firmware spec
	if c.Spec.Firmware.DesiredVersion == "" {
		return fmt.Errorf("spec.firmware.desiredVersion is required")
	}
	if c.Spec.Firmware.DownloadURL == "" {
		return fmt.Errorf("spec.firmware.downloadUrl is required")
	}

	// Validate server spec
	if c.Spec.Server.Address == "" {
		return fmt.Errorf("spec.server.address is required")
	}

	// Validate timeouts
	if c.Spec.Download.ConnectTimeout < 0 {
		return fmt.Errorf("spec.download.connectTimeout must be positive")
	}
	if c.Spec.Download.TotalTimeout < 0 {
		return fmt.Errorf("spec.download.totalTimeout must be positive")
	}
	if c.Spec.Download.ConnectTimeout > c.Spec.Download.TotalTimeout {
		return fmt.Errorf("connectTimeout cannot be greater than totalTimeout")
	}

	return nil
}

// LoadFromFile loads configuration from a YAML file.
func LoadFromFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	// Apply defaults
	config.SetDefaults()

	// Validate
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &config, nil
}

// GetConnectTimeout returns the connect timeout as a Duration.
func (c *Config) GetConnectTimeout() time.Duration {
	return time.Duration(c.Spec.Download.ConnectTimeout) * time.Second
}

// GetTotalTimeout returns the total timeout as a Duration.
func (c *Config) GetTotalTimeout() time.Duration {
	return time.Duration(c.Spec.Download.TotalTimeout) * time.Second
}
