package upgrade

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/client/config"
	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/client/gnoi"
)

const (
	// MD5Length is the expected length of an MD5 checksum in hexadecimal.
	MD5Length = 32

	// MD5Pattern is the regex pattern for valid MD5 checksums.
	MD5Pattern = "^[a-fA-F0-9]+$"
)

// ApplyConfig performs a package upgrade using the provided configuration.
// This is the main entry point for config-driven upgrades (e.g., from YAML files).
//
// The function will:
//  1. Validate the configuration for required fields and correct formats
//  2. Validate the server address format (host:port)
//  3. Create a gNOI client connection (with or without TLS)
//  4. Execute the SetPackage RPC with the specified parameters
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - cfg: Configuration implementing the Config interface (validated before use)
//   - serverAddr: Target device address in "host:port" format
//   - useTLS: Whether to use TLS for the gRPC connection
//
// Returns:
//   - nil on success
//   - Error with context about what failed (validation, connection, or RPC)
//
// Example:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
//	defer cancel()
//	err := ApplyConfig(ctx, config, "device:50055", true)
func ApplyConfig(ctx context.Context, cfg Config, serverAddr string, useTLS bool) error {
	// Validate configuration
	if err := ValidateConfig(cfg); err != nil {
		return fmt.Errorf("configuration validation failed: %w", err)
	}

	// Validate server address
	if err := validateServerAddress(serverAddr); err != nil {
		return fmt.Errorf("invalid server address: %w", err)
	}

	// Create gNOI client
	clientConfig := &config.Config{
		Address: serverAddr,
		TLS:     useTLS,
	}

	client, err := gnoi.NewSystemClient(clientConfig)
	if err != nil {
		return fmt.Errorf("failed to create gNOI client: %w", err)
	}
	defer client.Close()

	// Execute SetPackage operation
	params := &gnoi.SetPackageParams{
		URL:      cfg.GetPackageURL(),
		Filename: cfg.GetFilename(),
		MD5:      cfg.GetMD5(),
		Version:  cfg.GetVersion(),
		Activate: cfg.GetActivate(),
	}

	if err := client.SetPackage(ctx, params); err != nil {
		return fmt.Errorf("package installation failed: %w", err)
	}

	return nil
}

// DownloadPackage performs a direct package download with the specified options.
// This is the entry point for command-line driven upgrades (e.g., with flags).
func DownloadPackage(ctx context.Context, opts *DownloadOptions, serverAddr string, useTLS bool) error {
	// Validate options
	if err := ValidateConfig(opts); err != nil {
		return fmt.Errorf("options validation failed: %w", err)
	}

	// Use ApplyConfig for the actual operation
	return ApplyConfig(ctx, opts, serverAddr, useTLS)
}

// ValidateConfig validates the configuration or options for common issues.
func ValidateConfig(cfg Config) error {
	// Validate required fields
	if cfg.GetPackageURL() == "" {
		return fmt.Errorf("package URL is required")
	}
	if cfg.GetFilename() == "" {
		return fmt.Errorf("filename is required")
	}
	if cfg.GetMD5() == "" {
		return fmt.Errorf("MD5 checksum is required")
	}

	// Validate URL format
	if err := validateURL(cfg.GetPackageURL()); err != nil {
		return fmt.Errorf("invalid package URL: %w", err)
	}

	// Validate MD5 format
	if err := validateMD5(cfg.GetMD5()); err != nil {
		return fmt.Errorf("invalid MD5 checksum: %w", err)
	}

	// Validate filename (basic check for absolute path)
	if !strings.HasPrefix(cfg.GetFilename(), "/") {
		return fmt.Errorf("filename must be an absolute path")
	}

	return nil
}

// validateURL validates URL format and scheme.
func validateURL(urlStr string) error {
	if urlStr == "" {
		return fmt.Errorf("URL cannot be empty")
	}

	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return fmt.Errorf("invalid URL format '%s': %w", urlStr, err)
	}

	if parsedURL.Scheme == "" {
		return fmt.Errorf("URL must include scheme (http/https), got: '%s'", urlStr)
	}

	if parsedURL.Host == "" {
		return fmt.Errorf("URL must include host, got: '%s'", urlStr)
	}

	// Check for supported schemes
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return fmt.Errorf("unsupported URL scheme '%s', must be 'http' or 'https'", parsedURL.Scheme)
	}

	return nil
}

// validateServerAddress validates server address format (host:port).
func validateServerAddress(addr string) error {
	if addr == "" {
		return fmt.Errorf("address cannot be empty")
	}

	// Simple validation - should contain host:port format
	parts := strings.Split(addr, ":")
	if len(parts) != 2 {
		return fmt.Errorf("address must be in host:port format, got '%s'", addr)
	}

	host, port := parts[0], parts[1]
	if host == "" {
		return fmt.Errorf("host cannot be empty")
	}
	if port == "" {
		return fmt.Errorf("port cannot be empty")
	}

	return nil
}

// validateMD5 validates MD5 checksum format (32 hex characters).
func validateMD5(md5 string) error {
	if md5 == "" {
		return fmt.Errorf("MD5 checksum cannot be empty")
	}

	// MD5 should be exactly 32 hex characters
	if len(md5) != MD5Length {
		return fmt.Errorf("MD5 checksum must be %d characters, got %d", MD5Length, len(md5))
	}

	// Check if all characters are valid hex
	matched, err := regexp.MatchString(MD5Pattern, md5)
	if err != nil {
		return fmt.Errorf("failed to validate MD5 format: %w", err)
	}

	if !matched {
		return fmt.Errorf("MD5 checksum contains invalid characters, must be hexadecimal")
	}

	return nil
}
