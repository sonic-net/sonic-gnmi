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
	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/workflow"
	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/workflow/steps"
)

var rootCmd = &cobra.Command{
	Use:   "upgrade-agent",
	Short: "SONiC package upgrade agent",
	Long: `upgrade-agent is a command line tool for managing SONiC package upgrades.
It supports both YAML configuration files and direct command-line operations.`,
}

var applyCmd = &cobra.Command{
	Use:   "apply [workflow.yaml]",
	Short: "Apply package upgrade from YAML workflow configuration",
	Long: `Apply a package upgrade using a YAML workflow configuration file.
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

	// DefaultTLSEnabled is the default TLS setting for gRPC connections.
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

// runApply executes a workflow configuration file using the new workflow engine.
//
// This function now uses the pluggable workflow system which supports:
//   - Type-safe step implementations with proper validation
//   - Extensible step registry for adding new operation types
//   - Clean separation between CLI concerns and workflow logic
//   - Better error handling and reporting
func runApply(cmd *cobra.Command, args []string) error {
	workflowFile := args[0]

	// Load workflow from YAML file
	wf, err := workflow.LoadWorkflowFromFile(workflowFile)
	if err != nil {
		return fmt.Errorf("failed to load workflow from '%s': %w", workflowFile, err)
	}

	// Create step registry and register available step types
	registry := workflow.NewRegistry()
	registry.Register(steps.DownloadStepType, steps.NewDownloadStep)

	// Create workflow execution engine
	engine := workflow.NewEngine(registry)

	// Create context with timeout for all operations
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Prepare client configuration for steps
	clientConfig := map[string]interface{}{
		"server_addr": server,
		"use_tls":     tls,
	}

	// Display execution details
	fmt.Printf("Executing workflow from %s\n", workflowFile)
	fmt.Printf("  Server: %s (TLS: %v)\n", server, tls)
	fmt.Printf("  Timeout: %v\n", timeout)
	fmt.Println()

	// Execute the workflow
	if err := engine.Execute(ctx, wf, clientConfig); err != nil {
		return fmt.Errorf("workflow execution failed: %w", err)
	}

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
