// Package main provides the sonic-gnmi CLI tool for interacting with gNMI servers.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/client/gnmi"
)

const (
	defaultTimeout = 30 * time.Second
	defaultTarget  = "localhost:50055"
)

// Config holds the CLI configuration.
type Config struct {
	Target      string
	Timeout     time.Duration
	TLSEnabled  bool
	TLSInsecure bool
	TLSCertFile string
	TLSKeyFile  string
	Command     string
	Args        []string
}

func main() {
	config := parseFlags()

	if len(config.Args) == 0 {
		printUsage()
		os.Exit(1)
	}

	config.Command = config.Args[0]
	config.Args = config.Args[1:]

	// Create gNMI client
	clientConfig := &gnmi.ClientConfig{
		Target:      config.Target,
		Timeout:     config.Timeout,
		TLSEnabled:  config.TLSEnabled,
		TLSInsecure: config.TLSInsecure,
		TLSCertFile: config.TLSCertFile,
		TLSKeyFile:  config.TLSKeyFile,
	}

	client, err := gnmi.NewClient(clientConfig)
	if err != nil {
		log.Fatalf("Failed to create gNMI client: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), config.Timeout)
	defer cancel()

	// Execute command
	switch config.Command {
	case "capabilities", "cap":
		err = runCapabilities(ctx, client)
	case "disk-space", "ds":
		err = runDiskSpace(ctx, client, config.Args)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", config.Command)
		printUsage()
		os.Exit(1)
	}

	if err != nil {
		log.Fatalf("Command failed: %v", err)
	}
}

func parseFlags() *Config {
	config := &Config{}

	flag.StringVar(&config.Target, "target", defaultTarget, "gNMI server address (host:port)")
	flag.DurationVar(&config.Timeout, "timeout", defaultTimeout, "Request timeout")
	flag.BoolVar(&config.TLSEnabled, "tls", false, "Enable TLS")
	flag.BoolVar(&config.TLSInsecure, "tls-insecure", false, "Skip TLS certificate verification")
	flag.StringVar(&config.TLSCertFile, "tls-cert", "", "TLS certificate file")
	flag.StringVar(&config.TLSKeyFile, "tls-key", "", "TLS key file")

	flag.Usage = printUsage
	flag.Parse()

	config.Args = flag.Args()
	return config
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `sonic-gnmi - gNMI client for SONiC network management

Usage: sonic-gnmi [OPTIONS] COMMAND [ARGS...]

Options:
  -target string
        gNMI server address (default "localhost:50055")
  -timeout duration
        Request timeout (default 30s)
  -tls
        Enable TLS
  -tls-insecure
        Skip TLS certificate verification
  -tls-cert string
        TLS certificate file
  -tls-key string
        TLS key file

Commands:
  capabilities, cap
        Get server capabilities

  disk-space, ds PATH
        Get disk space information for filesystem path
        Example: sonic-gnmi disk-space /host

Examples:
  # Get server capabilities
  sonic-gnmi capabilities

  # Get disk space for root filesystem
  sonic-gnmi disk-space /

  # Get disk space with TLS
  sonic-gnmi -tls disk-space /host

  # Connect to remote server
  sonic-gnmi -target 192.168.1.100:50055 disk-space /var/log
`)
}

func runCapabilities(ctx context.Context, client *gnmi.Client) error {
	resp, err := client.Capabilities(ctx)
	if err != nil {
		return fmt.Errorf("capabilities request failed: %w", err)
	}

	fmt.Printf("gNMI Version: %s\n", resp.GNMIVersion)
	fmt.Printf("Supported Encodings:\n")
	for _, encoding := range resp.SupportedEncodings {
		fmt.Printf("  - %s\n", encoding.String())
	}

	fmt.Printf("Supported Models:\n")
	for _, model := range resp.SupportedModels {
		fmt.Printf("  - Name: %s, Organization: %s, Version: %s\n",
			model.Name, model.Organization, model.Version)
	}

	return nil
}

func runDiskSpace(ctx context.Context, client *gnmi.Client, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("disk-space command requires exactly one argument (filesystem path)")
	}

	path := args[0]
	info, err := client.GetDiskSpace(ctx, path)
	if err != nil {
		return fmt.Errorf("failed to get disk space: %w", err)
	}

	// Pretty print the JSON response
	jsonBytes, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to format response: %w", err)
	}

	fmt.Println(string(jsonBytes))
	return nil
}

func init() {
	// Parse environment variables for configuration
	if target := os.Getenv("GNMI_TARGET"); target != "" {
		os.Args = append(os.Args, "-target", target)
	}
	if timeout := os.Getenv("GNMI_TIMEOUT"); timeout != "" {
		if _, err := time.ParseDuration(timeout); err == nil {
			os.Args = append(os.Args, "-timeout", timeout)
		}
	}
	if tls := os.Getenv("GNMI_TLS"); strings.ToLower(tls) == "true" {
		os.Args = append(os.Args, "-tls")
	}
	if insecure := os.Getenv("GNMI_TLS_INSECURE"); strings.ToLower(insecure) == "true" {
		os.Args = append(os.Args, "-tls-insecure")
	}
	if cert := os.Getenv("GNMI_TLS_CERT"); cert != "" {
		os.Args = append(os.Args, "-tls-cert", cert)
	}
	if key := os.Getenv("GNMI_TLS_KEY"); key != "" {
		os.Args = append(os.Args, "-tls-key", key)
	}
}
