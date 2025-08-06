// Package steps contains workflow step implementations like download, push-config, etc.
package steps

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/client/config"
	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/client/gnoi"
	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/workflow"
)

const (
	// DownloadStepType is the step type identifier for download operations.
	DownloadStepType = "download"

	// MD5Length is the expected length of an MD5 checksum in hexadecimal.
	MD5Length = 32

	// MD5Pattern is the regex pattern for valid MD5 checksums.
	MD5Pattern = "^[a-fA-F0-9]+$"
)

// DownloadStep implements package download and installation via gNOI System.SetPackage.
//
// This step downloads a package from an HTTP/HTTPS URL and installs it on the target
// device using the gNOI System service. It supports MD5 checksum validation and
// optional package activation after installation.
//
// YAML configuration example:
//
//   - name: download-sonic-image
//     type: download
//     params:
//     url: "https://example.com/sonic-image.bin"
//     filename: "/tmp/sonic-image.bin"
//     md5: "d41d8cd98f00b204e9800998ecf8427e"
//     version: "4.0.0"        # optional
//     activate: false         # optional, default false
//
// Required parameters:
//   - url: HTTP/HTTPS URL to download the package from
//   - filename: Absolute path where the package will be saved on the device
//   - md5: Expected MD5 checksum of the package (32 hex characters)
//
// Optional parameters:
//   - version: Package version string for tracking and logging
//   - activate: Whether to activate the package after installation (default: false)
type DownloadStep struct {
	name     string
	URL      string
	Filename string
	MD5      string
	Version  string
	Activate bool
}

// DownloadStepParams represents the expected parameters for download step configuration.
// This struct is used internally for YAML parameter parsing and validation.
type DownloadStepParams struct {
	URL      string `yaml:"url"`
	Filename string `yaml:"filename"`
	MD5      string `yaml:"md5"`
	Version  string `yaml:"version,omitempty"`
	Activate bool   `yaml:"activate,omitempty"`
}

// NewDownloadStep creates a new download step from raw YAML parameters.
// This function serves as the factory function for the download step type.
//
// It validates that all required parameters are present and have the correct types,
// but defers detailed validation (URL format, MD5 format, etc.) to the Validate method.
func NewDownloadStep(name string, params map[string]interface{}) (workflow.Step, error) {
	step := &DownloadStep{name: name}

	// Extract and validate required string parameters
	var ok bool
	step.URL, ok = params["url"].(string)
	if !ok || step.URL == "" {
		return nil, fmt.Errorf("url parameter is required and must be a non-empty string")
	}

	step.Filename, ok = params["filename"].(string)
	if !ok || step.Filename == "" {
		return nil, fmt.Errorf("filename parameter is required and must be a non-empty string")
	}

	step.MD5, ok = params["md5"].(string)
	if !ok || step.MD5 == "" {
		return nil, fmt.Errorf("md5 parameter is required and must be a non-empty string")
	}

	// Extract optional parameters with defaults
	if version, exists := params["version"]; exists {
		if step.Version, ok = version.(string); !ok {
			return nil, fmt.Errorf("version parameter must be a string")
		}
	}

	if activate, exists := params["activate"]; exists {
		if step.Activate, ok = activate.(bool); !ok {
			return nil, fmt.Errorf("activate parameter must be a boolean")
		}
	}

	return step, nil
}

// GetName returns the human-readable name of this step.
func (s *DownloadStep) GetName() string {
	return s.name
}

// GetType returns the step type identifier.
func (s *DownloadStep) GetType() string {
	return DownloadStepType
}

// Validate performs comprehensive validation of the download step parameters.
//
// Validation includes:
//   - URL format and scheme validation (must be http/https)
//   - Filename must be an absolute path
//   - MD5 must be exactly 32 hexadecimal characters
//   - All required fields are non-empty
//
// This method should be called before Execute to ensure the step configuration is valid.
func (s *DownloadStep) Validate() error {
	// Validate URL format and scheme
	if err := s.validateURL(); err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	// Validate filename is absolute path
	if !strings.HasPrefix(s.Filename, "/") {
		return fmt.Errorf("filename must be an absolute path, got: %s", s.Filename)
	}

	// Validate MD5 format
	if err := s.validateMD5(); err != nil {
		return fmt.Errorf("invalid MD5 checksum: %w", err)
	}

	return nil
}

// Execute performs the package download and installation operation.
//
// The client parameter must be a struct containing the necessary configuration
// for creating gNOI connections (server address, TLS settings, etc.).
//
// Execution steps:
//  1. Extract connection configuration from client parameter
//  2. Create gNOI System client
//  3. Execute SetPackage RPC with download parameters
//  4. Handle and wrap any errors with context
//
// The method expects the client to have the following structure:
//
//	type Client struct {
//		ServerAddr string
//		UseTLS     bool
//	}
func (s *DownloadStep) Execute(ctx context.Context, client interface{}) error {
	// Extract client configuration
	clientConfig, err := s.extractClientConfig(client)
	if err != nil {
		return fmt.Errorf("invalid client configuration: %w", err)
	}

	// Create gNOI client
	gnoiClient, err := gnoi.NewSystemClient(clientConfig)
	if err != nil {
		return fmt.Errorf("failed to create gNOI client: %w", err)
	}
	defer gnoiClient.Close()

	// Prepare SetPackage parameters
	params := &gnoi.SetPackageParams{
		URL:      s.URL,
		Filename: s.Filename,
		MD5:      s.MD5,
		Version:  s.Version,
		Activate: s.Activate,
	}

	// Execute the download and installation
	fmt.Printf("  Downloading: %s -> %s\n", s.URL, s.Filename)
	if s.Version != "" {
		fmt.Printf("  Version: %s\n", s.Version)
	}
	fmt.Printf("  Activate: %v\n", s.Activate)

	if err := gnoiClient.SetPackage(ctx, params); err != nil {
		return fmt.Errorf("package download and installation failed: %w", err)
	}

	return nil
}

// validateURL validates the URL format and scheme.
func (s *DownloadStep) validateURL() error {
	if s.URL == "" {
		return fmt.Errorf("URL cannot be empty")
	}

	parsedURL, err := url.Parse(s.URL)
	if err != nil {
		return fmt.Errorf("invalid URL format '%s': %w", s.URL, err)
	}

	if parsedURL.Scheme == "" {
		return fmt.Errorf("URL must include scheme (http/https), got: '%s'", s.URL)
	}

	if parsedURL.Host == "" {
		return fmt.Errorf("URL must include host, got: '%s'", s.URL)
	}

	// Check for supported schemes
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return fmt.Errorf("unsupported URL scheme '%s', must be 'http' or 'https'", parsedURL.Scheme)
	}

	return nil
}

// validateMD5 validates the MD5 checksum format (32 hex characters).
func (s *DownloadStep) validateMD5() error {
	if s.MD5 == "" {
		return fmt.Errorf("MD5 checksum cannot be empty")
	}

	// MD5 should be exactly 32 hex characters
	if len(s.MD5) != MD5Length {
		return fmt.Errorf("MD5 checksum must be %d characters, got %d", MD5Length, len(s.MD5))
	}

	// Check if all characters are valid hex
	matched, err := regexp.MatchString(MD5Pattern, s.MD5)
	if err != nil {
		return fmt.Errorf("failed to validate MD5 format: %w", err)
	}

	if !matched {
		return fmt.Errorf("MD5 checksum contains invalid characters, must be hexadecimal")
	}

	return nil
}

// extractClientConfig extracts gNOI client configuration from the generic client interface.
// This method expects the client to be a struct with ServerAddr and UseTLS fields.
func (s *DownloadStep) extractClientConfig(client interface{}) (*config.Config, error) {
	// Use type assertion to extract configuration
	// This is a simplified approach - in a production system, you might want
	// to define a proper interface for client configuration
	switch c := client.(type) {
	case map[string]interface{}:
		// Handle configuration passed as a map
		serverAddr, ok := c["server_addr"].(string)
		if !ok || serverAddr == "" {
			return nil, fmt.Errorf("server_addr is required in client configuration")
		}

		useTLS, _ := c["use_tls"].(bool) // defaults to false if not present

		return &config.Config{
			Address: serverAddr,
			TLS:     useTLS,
		}, nil

	default:
		return nil, fmt.Errorf("unsupported client configuration type: %T", client)
	}
}
