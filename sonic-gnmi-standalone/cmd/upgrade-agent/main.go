// Package main implements the upgrade-agent CLI tool for managing SONiC package upgrades.
//
// The upgrade-agent supports two operation modes:
//   - Configuration-based: Apply upgrades from YAML workflow files
//   - Direct: Execute upgrades using command-line flags
//
// Example usage:
//
//	upgrade-agent apply workflow.yaml --server device:50055
//	upgrade-agent download --url http://example.com/package.bin --file /tmp/package.bin --md5 abc123...
//
// The tool uses gNOI System.SetPackage RPC for package installation and supports
// features like MD5 validation, multi-step workflows, and both secure (TLS) and
// insecure connections.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/upgrade"
)

var rootCmd = &cobra.Command{
	Use:   "upgrade-agent",
	Short: "SONiC package upgrade agent",
	Long: `upgrade-agent is a command line tool for managing SONiC package upgrades.
It supports both YAML configuration files and direct command-line operations.`,
}

var applyCmd = &cobra.Command{
	Use:   "apply [config.yaml]",
	Short: "Apply package upgrade from YAML configuration",
	Long: `Apply a package upgrade using a YAML configuration file.
The configuration file must be an UpgradeWorkflow with one or more steps.
Server connection is specified via command-line flags.`,
	Args: cobra.ExactArgs(1),
	Example: `  # Apply workflow configuration
  upgrade-agent apply workflow.yaml --server device.example.com:50055
  
  # With TLS enabled
  upgrade-agent apply workflow.yaml --server device:50055 --tls
  
  # Example UpgradeWorkflow:
  # apiVersion: sonic.net/v1
  # kind: UpgradeWorkflow
  # metadata:
  #   name: sonic-upgrade
  # spec:
  #   steps:
  #     - name: download-image
  #       type: download
  #       params:
  #         url: "http://example.com/sonic.bin"
  #         filename: "/tmp/sonic.bin"
  #         md5: "d41d8cd98f00b204e9800998ecf8427e"
  #         version: "1.0.0"
  #         activate: false`,
	RunE:         runApply,
	SilenceUsage: true, // Don't print usage on errors
}

var downloadCmd = &cobra.Command{
	Use:   "download",
	Short: "Download and install package directly",
	Long: `Download and install a package directly using command-line flags.
This bypasses the need for a configuration file.`,
	Example: `  # Download and install package
  upgrade-agent download \
    --server device.example.com:50055 \
    --url http://example.com/package.bin \
    --file /opt/packages/package.bin \
    --md5 d41d8cd98f00b204e9800998ecf8427e \
    --version 1.0.0 \
    --activate

  # Download with TLS
  upgrade-agent download \
    --server device:50055 \
    --tls \
    --url https://secure.com/package.bin \
    --file /opt/package.bin \
    --md5 098f6bcd4621d373cade4e832627b4f6`,
	RunE:         runDownload,
	SilenceUsage: true, // Don't print usage on errors
}

const (
	// DefaultTimeout is the default timeout for upgrade operations.
	// 5 minutes allows time for large package downloads over slow connections.
	DefaultTimeout = 5 * time.Minute

	// DefaultTLSEnabled is the default TLS setting for gRPC connections
	DefaultTLSEnabled = false
)
// Global flags.
var (
	timeout time.Duration
	server  string
	tls     bool
)

// Download command flags.
var (
	url      string
	file     string
	md5      string
	version  string
	activate bool
)

func init() {
	// Global flags (shared by all commands)
	rootCmd.PersistentFlags().DurationVar(&timeout, "timeout", DefaultTimeout, "Request timeout")
	rootCmd.PersistentFlags().StringVar(&server, "server", "", "Server address (host:port)")
	rootCmd.PersistentFlags().BoolVar(&tls, "tls", false, "Enable TLS")
	rootCmd.MarkPersistentFlagRequired("server")

	// Download command flags
	downloadCmd.Flags().StringVar(&url, "url", "", "HTTP URL to download package from")
	downloadCmd.Flags().StringVar(&file, "file", "", "Destination file path on device")
	downloadCmd.Flags().StringVar(&md5, "md5", "", "Expected MD5 checksum (hex string)")
	downloadCmd.Flags().StringVar(&version, "version", "", "Package version (optional)")
	downloadCmd.Flags().BoolVar(&activate, "activate", false, "Activate package after installation")

	// Mark required download flags
	downloadCmd.MarkFlagRequired("url")
	downloadCmd.MarkFlagRequired("file")
	downloadCmd.MarkFlagRequired("md5")

	// Add commands to root
	rootCmd.AddCommand(applyCmd)
	rootCmd.AddCommand(downloadCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// runApply executes a workflow configuration file.
//
// Workflow execution follows these steps:
//  1. Load and validate the YAML configuration file
//  2. Create a context with the specified timeout
//  3. Execute each workflow step sequentially
//  4. Stop on the first error (no rollback currently)
//
// Currently only supports "download" step types. Future versions
// may add support for validation, rollback, and conditional steps.
func runApply(cmd *cobra.Command, args []string) error {
	configFile := args[0]

	// Load workflow configuration from YAML file.
	// This validates the file exists, is readable, and contains
	// a valid UpgradeWorkflow resource.
	config, err := LoadConfigurationFile(configFile)
	if err != nil {
		return fmt.Errorf("failed to load configuration from '%s': %w", configFile, err)
	}

	// Create context with timeout for all operations
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Execute workflow
	fmt.Printf("Executing upgrade workflow from %s\n", configFile)
	fmt.Printf("  Workflow: %s\n", config.Metadata.Name)
	fmt.Printf("  Steps: %d\n", len(config.Spec.Steps))
	fmt.Printf("  Server: %s (TLS: %v)\n", server, tls)
	fmt.Println()

	// Execute each step sequentially
	for i, step := range config.Spec.Steps {
		fmt.Printf("Step %d/%d: %s\n", i+1, len(config.Spec.Steps), step.Name)
		fmt.Printf("  Type: %s\n", step.Type)

		// TODO: Add support for additional step types:
		//   - install: Install downloaded package (separate from download)
		//   - push-config: Push configuration to the device
		// For now, only handle download steps
		if step.Type == "download" {
			// Convert step to Config interface
			stepConfig, err := ConvertStepToConfig(step)
			if err != nil {
				return fmt.Errorf("step '%s' failed: %w", step.Name, err)
			}

			// Execute using existing logic
			if err := upgrade.ApplyConfig(ctx, stepConfig, server, tls); err != nil {
				return fmt.Errorf("step '%s' failed: %w", step.Name, err)
			}
			fmt.Printf("  ✓ Step completed successfully\n\n")
		} else {
			return fmt.Errorf("unsupported step type: %s", step.Type)
		}
	}

	fmt.Println("Upgrade completed successfully!")
	return nil
}

// runDownload executes a direct package download using command-line flags.
//
// This provides a simpler alternative to YAML configuration for single-package
// downloads. All parameters are validated before initiating the download.
//
// The function will:
//  1. Create a DownloadOptions struct from command flags
//  2. Validate all required parameters (URL, file path, MD5)
//  3. Connect to the gNOI server and execute SetPackage RPC
//  4. Report success or failure with detailed error messages
func runDownload(cmd *cobra.Command, args []string) error {
	// Create download options from flags
	opts := &upgrade.DownloadOptions{
		URL:      url,
		Filename: file,
		MD5:      md5,
		Version:  version,
		Activate: activate,
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Display operation details for user confirmation
	fmt.Printf("Downloading and installing package\n")
	fmt.Printf("  Package: %s -> %s\n", opts.URL, opts.Filename)
	fmt.Printf("  Server: %s (TLS: %v)\n", server, tls)
	if opts.Version != "" {
		fmt.Printf("  Version: %s\n", opts.Version)
	}
	fmt.Printf("  Activate: %v\n", opts.Activate)

	// Execute the download
	if err := upgrade.DownloadPackage(ctx, opts, server, tls); err != nil {
		return fmt.Errorf("package download failed: %w", err)
	}

	fmt.Println("Package download and installation completed successfully!")
	return nil
}
