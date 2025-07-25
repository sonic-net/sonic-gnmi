package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/client/config"
	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/client/gnoi"
)

var rootCmd = &cobra.Command{
	Use:   "sonic-gnoi",
	Short: "gNOI client for SONiC devices",
	Long: `sonic-gnoi is a command line client for gNOI (gRPC Network Operations Interface) services.
It provides access to various network device operations including system management,
file operations, and more.`,
}

var systemCmd = &cobra.Command{
	Use:   "system",
	Short: "System service operations",
	Long:  "Access gNOI System service operations like package installation, reboot, etc.",
}

var setPackageCmd = &cobra.Command{
	Use:   "set-package",
	Short: "Install package from remote HTTP URL",
	Long: `Install a software package on the target device by downloading it from a remote HTTP URL.
The server will download the package, verify its MD5 checksum, and install it to the specified location.`,
	Example: `  # Install package with MD5 verification
  sonic-gnoi system set-package \
    --server device.example.com:50051 \
    --url http://updates.example.com/package-1.0.tar.gz \
    --file /opt/packages/package-1.0.tar.gz \
    --md5 d41d8cd98f00b204e9800998ecf8427e \
    --version 1.0 \
    --activate

  # Install with TLS
  sonic-gnoi system set-package \
    --server device:50051 \
    --tls \
    --url https://secure.com/package.tar.gz \
    --file /opt/package.tar.gz \
    --md5 098f6bcd4621d373cade4e832627b4f6`,
	RunE: runSetPackage,
}

// Global flags.
var (
	server  string
	tls     bool
	timeout time.Duration
)

// SetPackage specific flags.
var (
	url      string
	file     string
	md5      string
	version  string
	activate bool
)

func init() {
	// Global flags
	rootCmd.PersistentFlags().StringVar(&server, "server", "", "Server address (host:port)")
	rootCmd.PersistentFlags().BoolVar(&tls, "tls", false, "Enable TLS")
	rootCmd.PersistentFlags().DurationVar(&timeout, "timeout", 5*time.Minute, "Request timeout")

	// SetPackage flags
	setPackageCmd.Flags().StringVar(&url, "url", "", "HTTP URL to download package from")
	setPackageCmd.Flags().StringVar(&file, "file", "", "Destination file path on device")
	setPackageCmd.Flags().StringVar(&md5, "md5", "", "Expected MD5 checksum (hex string)")
	setPackageCmd.Flags().StringVar(&version, "version", "", "Package version (optional)")
	setPackageCmd.Flags().BoolVar(&activate, "activate", false, "Activate package after installation")

	// Mark required flags
	setPackageCmd.MarkFlagRequired("server")
	setPackageCmd.MarkFlagRequired("url")
	setPackageCmd.MarkFlagRequired("file")
	setPackageCmd.MarkFlagRequired("md5")

	// Build command tree
	systemCmd.AddCommand(setPackageCmd)
	rootCmd.AddCommand(systemCmd)
}

func runSetPackage(cmd *cobra.Command, args []string) error {
	// Create client configuration
	cfg := &config.Config{
		Address: server,
		TLS:     tls,
		Timeout: timeout,
	}

	// Create client
	client, err := gnoi.NewSystemClient(cfg)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	defer client.Close()

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Prepare SetPackage parameters
	params := &gnoi.SetPackageParams{
		URL:      url,
		Filename: file,
		MD5:      md5,
		Version:  version,
		Activate: activate,
	}

	// Execute SetPackage
	fmt.Printf("Installing package from %s to %s...\n", url, file)
	if err := client.SetPackage(ctx, params); err != nil {
		return fmt.Errorf("SetPackage failed: %w", err)
	}

	fmt.Println("Package installed successfully!")
	return nil
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
