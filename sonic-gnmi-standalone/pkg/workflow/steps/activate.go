// Package steps contains workflow step implementations like download, reboot, etc.
package steps

import (
	"context"
	"fmt"

	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/client/config"
	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/client/gnoi"
	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/workflow"
)

const (
	// ActivateStepType is the step type identifier for OS activation operations.
	ActivateStepType = "activate"
)

// ActivateStep implements OS version activation via gNOI OS.Activate.
//
// This step activates a specific OS version on the target device using the gNOI OS service.
// The version parameter is required and must match an installed OS version on the device.
//
// YAML configuration example:
//
//   - name: activate-os
//     type: activate
//     params:
//     version: "SONiC-OS-master.930514-9673e12d4"  # required, OS version to activate
//     no_reboot: false                              # optional, activate without reboot (default: false)
//     standby_supervisor: false                      # optional, activate on standby (default: false)
//
// Required parameters:
//   - version: The exact OS version string to activate
//
// Optional parameters:
//   - no_reboot: Whether to activate without rebooting (default: false)
//   - standby_supervisor: Whether to activate on standby supervisor (default: false)
type ActivateStep struct {
	name              string
	Version           string // OS version to activate (required)
	NoReboot          bool   // Whether to activate without reboot
	StandbySupervisor bool   // Whether to activate on standby supervisor
}

// ActivateStepParams represents the expected parameters for activate step configuration.
// This struct is used internally for YAML parameter parsing and validation.
type ActivateStepParams struct {
	Version           string `yaml:"version"`
	NoReboot          bool   `yaml:"no_reboot,omitempty"`
	StandbySupervisor bool   `yaml:"standby_supervisor,omitempty"`
}

// NewActivateStep creates a new activate step from raw YAML parameters.
// This function serves as the factory function for the activate step type.
//
// The version parameter is required and must be provided.
func NewActivateStep(name string, params map[string]interface{}) (workflow.Step, error) {
	step := &ActivateStep{name: name}

	// Extract required version parameter
	version, exists := params["version"]
	if !exists {
		return nil, fmt.Errorf("version parameter is required")
	}

	var ok bool
	if step.Version, ok = version.(string); !ok {
		return nil, fmt.Errorf("version parameter must be a string")
	}

	if step.Version == "" {
		return nil, fmt.Errorf("version parameter cannot be empty")
	}

	// Extract optional no_reboot parameter
	if noReboot, exists := params["no_reboot"]; exists {
		if step.NoReboot, ok = noReboot.(bool); !ok {
			return nil, fmt.Errorf("no_reboot parameter must be a boolean")
		}
	}

	// Extract optional standby_supervisor parameter
	if standbySupervisor, exists := params["standby_supervisor"]; exists {
		if step.StandbySupervisor, ok = standbySupervisor.(bool); !ok {
			return nil, fmt.Errorf("standby_supervisor parameter must be a boolean")
		}
	}

	return step, nil
}

// GetName returns the human-readable name of this step.
func (s *ActivateStep) GetName() string {
	return s.name
}

// GetType returns the step type identifier.
func (s *ActivateStep) GetType() string {
	return ActivateStepType
}

// Validate performs validation of the activate step parameters.
//
// Validation includes:
//   - Version must not be empty (already checked in NewActivateStep)
//   - Version format validation could be added here if needed
//
// This method should be called before Execute to ensure the step configuration is valid.
func (s *ActivateStep) Validate() error {
	// Version is already validated in NewActivateStep to be non-empty
	// Additional validation could be added here if needed, such as:
	// - Version format checking (e.g., must start with "SONiC-OS-")
	// - Length limits

	// For now, no additional validation is needed
	return nil
}

// Execute performs the OS activation operation.
//
// The client parameter must be a struct containing the necessary configuration
// for creating gNOI connections (server address, TLS settings, etc.).
//
// Execution steps:
//  1. Extract connection configuration from client parameter
//  2. Create gNOI OS client
//  3. Optionally verify current version (for logging)
//  4. Execute Activate RPC with activation parameters
//  5. Handle success or error responses appropriately
//
// The method expects the client to have the following structure:
//
//	type Client struct {
//	    ServerAddr string
//	    UseTLS     bool
//	}
func (s *ActivateStep) Execute(ctx context.Context, client interface{}) error {
	// Extract client configuration
	clientConfig, err := s.extractClientConfig(client)
	if err != nil {
		return fmt.Errorf("invalid client configuration: %w", err)
	}

	// Create gNOI OS client
	osClient, err := gnoi.NewOSClient(clientConfig)
	if err != nil {
		return fmt.Errorf("failed to create gNOI OS client: %w", err)
	}
	defer osClient.Close()

	// Log activation details
	fmt.Printf("  Activating OS version: %s\n", s.Version)
	if s.NoReboot {
		fmt.Printf("  No reboot: true (activation without reboot)\n")
	}
	if s.StandbySupervisor {
		fmt.Printf("  Target: standby supervisor\n")
	}

	// Optionally verify current version first (for informational purposes)
	currentVersion, err := osClient.Verify(ctx)
	if err == nil && currentVersion != "" {
		fmt.Printf("  Current version: %s\n", currentVersion)
	}

	// Prepare activation parameters
	params := &gnoi.ActivateParams{
		Version:           s.Version,
		NoReboot:          s.NoReboot,
		StandbySupervisor: s.StandbySupervisor,
	}

	// Execute the activation
	if err := osClient.Activate(ctx, params); err != nil {
		return fmt.Errorf("OS activation failed: %w", err)
	}

	fmt.Printf("  OS version %s activated successfully\n", s.Version)
	if !s.NoReboot {
		fmt.Printf("  Note: System will use this version after next reboot\n")
	}

	return nil
}

// extractClientConfig extracts gNOI client configuration from the generic client interface.
// This method expects the client to be a struct with ServerAddr and UseTLS fields.
func (s *ActivateStep) extractClientConfig(client interface{}) (*config.Config, error) {
	// Use type assertion to extract configuration
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
