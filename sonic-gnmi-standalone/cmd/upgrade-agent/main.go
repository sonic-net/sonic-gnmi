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
The configuration file specifies the package URL, destination, checksum, and server details.`,
	Args: cobra.ExactArgs(1),
	Example: `  # Apply package upgrade from config file
  upgrade-agent apply package-config.yaml
  
  # Example config.yaml:
  # apiVersion: sonic.net/v1
  # kind: PackageConfig
  # metadata:
  #   name: my-package-upgrade
  # spec:
  #   package:
  #     url: "http://example.com/package.bin"
  #     filename: "/opt/packages/package.bin"
  #     md5: "d41d8cd98f00b204e9800998ecf8427e"
  #     version: "1.0.0"
  #     activate: false
  #   server:
  #     address: "device.example.com:50055"
  #     tls: false`,
	RunE: runApply,
	SilenceUsage: true,  // Don't print usage on errors
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
	RunE: runDownload,
	SilenceUsage: true,  // Don't print usage on errors
}

// Global flags.
var (
	timeout time.Duration
)

// Download command flags.
var (
	server   string
	tls      bool
	url      string
	file     string
	md5      string
	version  string
	activate bool
)

func init() {
	// Global flags
	rootCmd.PersistentFlags().DurationVar(&timeout, "timeout", 5*time.Minute, "Request timeout")

	// Download command flags
	downloadCmd.Flags().StringVar(&server, "server", "", "Server address (host:port)")
	downloadCmd.Flags().BoolVar(&tls, "tls", false, "Enable TLS")
	downloadCmd.Flags().StringVar(&url, "url", "", "HTTP URL to download package from")
	downloadCmd.Flags().StringVar(&file, "file", "", "Destination file path on device")
	downloadCmd.Flags().StringVar(&md5, "md5", "", "Expected MD5 checksum (hex string)")
	downloadCmd.Flags().StringVar(&version, "version", "", "Package version (optional)")
	downloadCmd.Flags().BoolVar(&activate, "activate", false, "Activate package after installation")

	// Mark required download flags
	downloadCmd.MarkFlagRequired("server")
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

func runApply(cmd *cobra.Command, args []string) error {
	configFile := args[0]

	// Load YAML configuration
	config, err := LoadConfigFromFile(configFile)
	if err != nil {
		return fmt.Errorf("failed to load configuration from '%s': %w", configFile, err)
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Display what we're doing
	fmt.Printf("Applying package upgrade from %s\n", configFile)
	fmt.Printf("  Package: %s -> %s\n", config.GetPackageURL(), config.GetFilename())
	fmt.Printf("  Server: %s (TLS: %v)\n", config.GetServerAddress(), config.GetTLS())
	if config.GetVersion() != "" {
		fmt.Printf("  Version: %s\n", config.GetVersion())
	}
	fmt.Printf("  Activate: %v\n", config.GetActivate())

	// Execute the upgrade
	if err := upgrade.ApplyConfig(ctx, config); err != nil {
		return fmt.Errorf("package upgrade failed: %w", err)
	}

	fmt.Println("Package upgrade completed successfully!")
	return nil
}

func runDownload(cmd *cobra.Command, args []string) error {
	// Create download options from flags
	opts := &upgrade.DownloadOptions{
		URL:           url,
		Filename:      file,
		MD5:           md5,
		Version:       version,
		Activate:      activate,
		ServerAddress: server,
		TLS:           tls,
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Display what we're doing
	fmt.Printf("Downloading and installing package\n")
	fmt.Printf("  Package: %s -> %s\n", opts.URL, opts.Filename)
	fmt.Printf("  Server: %s (TLS: %v)\n", opts.ServerAddress, opts.TLS)
	if opts.Version != "" {
		fmt.Printf("  Version: %s\n", opts.Version)
	}
	fmt.Printf("  Activate: %v\n", opts.Activate)

	// Execute the download
	if err := upgrade.DownloadPackage(ctx, opts); err != nil {
		return fmt.Errorf("package download failed: %w", err)
	}

	fmt.Println("Package download and installation completed successfully!")
	return nil
}
