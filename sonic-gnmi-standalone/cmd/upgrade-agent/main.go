// Package main implements the upgrade-agent CLI tool for managing SONiC package upgrades.
//
// The upgrade-agent executes multi-step upgrade workflows from YAML configuration files.
// Each workflow can contain multiple steps like package downloads, configuration changes, etc.
//
// Example usage:
//
//	upgrade-agent apply workflow.yaml --server device:50055
//
// The tool currently supports download steps via gNOI System.SetPackage RPC with
// features like MD5 validation, multi-step workflows, and both secure (TLS) and
// insecure connections.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/workflow"
	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/workflow/steps"
)

var rootCmd = &cobra.Command{
	Use:   "upgrade-agent",
	Short: "SONiC package upgrade agent",
	Long: `upgrade-agent is a command line tool for managing SONiC package upgrades.
It executes upgrade workflows defined in YAML configuration files.`,
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
  #         activate: false
  #     - name: activate-os
  #       type: activate
  #       params:
  #         version: "SONiC-OS-master.930514-9673e12d4"
  #         no_reboot: false
  #     - name: reboot-system
  #       type: reboot
  #       params:
  #         delay: 10
  #         message: "Rebooting for upgrade"
  #         force: false`,
	RunE:         runApply,
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

func init() {
	// Global flags (shared by all commands)
	rootCmd.PersistentFlags().DurationVar(&timeout, "timeout", DefaultTimeout, "Request timeout")
	rootCmd.PersistentFlags().StringVar(&server, "server", "", "Server address (host:port)")
	rootCmd.PersistentFlags().BoolVar(&tls, "tls", false, "Enable TLS")
	rootCmd.MarkPersistentFlagRequired("server")

	// Add commands to root
	rootCmd.AddCommand(applyCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// runApply loads a workflow YAML file and executes all its steps.
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
	registry.Register(steps.RebootStepType, steps.NewRebootStep)
	registry.Register(steps.ActivateStepType, steps.NewActivateStep)

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
