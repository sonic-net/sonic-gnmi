// Package steps contains workflow step implementations like download, reboot, etc.
package steps

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/openconfig/gnoi/system"

	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/client/config"
	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/client/gnoi"
	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/workflow"
)

const (
	// RebootStepType is the step type identifier for reboot operations.
	RebootStepType = "reboot"
)

// RebootStep implements system reboot via gNOI System.Reboot.
//
// This step triggers a system reboot on the target device using the gNOI System service.
// It supports optional delay and message parameters for scheduling and logging.
//
// YAML configuration example:
//
//   - name: reboot-system
//     type: reboot
//     params:
//     method: "COLD"    # optional, reboot method: COLD, WARM, HALT, etc. (default: COLD)
//     delay: 10         # optional, delay in seconds before reboot
//     message: "System reboot for upgrade"  # optional, reason message
//     force: false      # optional, force reboot (default: false)
//
// Optional parameters:
//   - method: Reboot method - COLD, WARM, HALT, POWERDOWN, NSF, POWERUP (default: COLD)
//   - delay: Number of seconds to delay before rebooting (default: 0)
//   - message: Human-readable message explaining the reason for reboot
//   - force: Whether to force reboot without graceful shutdown (default: false)
type RebootStep struct {
	name    string
	Method  string // reboot method as string (converted to RebootMethod enum)
	Delay   uint32 // delay in seconds (converted to nanoseconds for gNOI)
	Message string
	Force   bool
}

// RebootStepParams represents the expected parameters for reboot step configuration.
// This struct is used internally for YAML parameter parsing and validation.
type RebootStepParams struct {
	Method  string `yaml:"method,omitempty"`
	Delay   uint32 `yaml:"delay,omitempty"`
	Message string `yaml:"message,omitempty"`
	Force   bool   `yaml:"force,omitempty"`
}

// NewRebootStep creates a new reboot step from raw YAML parameters.
// This function serves as the factory function for the reboot step type.
//
// All parameters are optional with sensible defaults.
func NewRebootStep(name string, params map[string]interface{}) (workflow.Step, error) {
	step := &RebootStep{name: name}

	// Extract optional parameters with defaults
	if method, exists := params["method"]; exists {
		var ok bool
		if step.Method, ok = method.(string); !ok {
			return nil, fmt.Errorf("method parameter must be a string")
		}
	}

	if delay, exists := params["delay"]; exists {
		switch d := delay.(type) {
		case int:
			if d < 0 {
				return nil, fmt.Errorf("delay parameter must be non-negative, got: %d", d)
			}
			step.Delay = uint32(d)
		case float64:
			if d < 0 {
				return nil, fmt.Errorf("delay parameter must be non-negative, got: %f", d)
			}
			step.Delay = uint32(d)
		default:
			return nil, fmt.Errorf("delay parameter must be a number")
		}
	}

	if message, exists := params["message"]; exists {
		var ok bool
		if step.Message, ok = message.(string); !ok {
			return nil, fmt.Errorf("message parameter must be a string")
		}
	}

	if force, exists := params["force"]; exists {
		var ok bool
		if step.Force, ok = force.(bool); !ok {
			return nil, fmt.Errorf("force parameter must be a boolean")
		}
	}

	return step, nil
}

// GetName returns the human-readable name of this step.
func (s *RebootStep) GetName() string {
	return s.name
}

// GetType returns the step type identifier.
func (s *RebootStep) GetType() string {
	return RebootStepType
}

// Validate performs validation of the reboot step parameters.
//
// Validation includes:
//   - Method must be a valid RebootMethod (COLD, WARM, etc.)
//   - Delay must be non-negative (already checked in NewRebootStep)
//   - Message length is reasonable (optional)
//
// This method should be called before Execute to ensure the step configuration is valid.
func (s *RebootStep) Validate() error {
	// Validate method if specified
	if s.Method != "" {
		if err := s.validateMethod(); err != nil {
			return fmt.Errorf("invalid reboot method: %w", err)
		}
	}

	// Optional: validate message length if needed
	if len(s.Message) > 1024 {
		return fmt.Errorf("message too long, maximum 1024 characters allowed")
	}

	return nil
}

// Execute performs the system reboot operation.
//
// The client parameter must be a struct containing the necessary configuration
// for creating gNOI connections (server address, TLS settings, etc.).
//
// Execution steps:
//  1. Extract connection configuration from client parameter
//  2. Create gNOI System client
//  3. Execute Reboot RPC with reboot parameters
//  4. Handle and wrap any errors with context
//
// The method expects the client to have the following structure:
//
//	type Client struct {
//		ServerAddr string
//		UseTLS     bool
//	}
func (s *RebootStep) Execute(ctx context.Context, client interface{}) error {
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

	// Prepare Reboot parameters (convert seconds to nanoseconds)
	delayNanoseconds := uint64(s.Delay) * uint64(time.Second)
	params := &gnoi.RebootParams{
		Method:  s.convertMethod(),
		Delay:   delayNanoseconds,
		Message: s.Message,
		Force:   s.Force,
	}

	// Execute the reboot
	method := s.Method
	if method == "" {
		method = "COLD"
	}

	if s.Delay > 0 {
		fmt.Printf("  Scheduling %s reboot in %d seconds\n", method, s.Delay)
	} else {
		fmt.Printf("  Rebooting system immediately (%s)\n", method)
	}

	if s.Message != "" {
		fmt.Printf("  Reason: %s\n", s.Message)
	}

	fmt.Printf("  Force: %v\n", s.Force)

	if err := gnoiClient.Reboot(ctx, params); err != nil {
		return fmt.Errorf("system reboot failed: %w", err)
	}

	// If there's a delay, inform the user about when the reboot will occur
	if s.Delay > 0 {
		rebootTime := time.Now().Add(time.Duration(s.Delay) * time.Second)
		fmt.Printf("  System will reboot at: %s\n", rebootTime.Format("15:04:05"))
	}

	return nil
}

// extractClientConfig extracts gNOI client configuration from the generic client interface.
// This method expects the client to be a struct with ServerAddr and UseTLS fields.
func (s *RebootStep) extractClientConfig(client interface{}) (*config.Config, error) {
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

// validateMethod validates the reboot method string.
func (s *RebootStep) validateMethod() error {
	validMethods := []string{"COLD", "WARM", "HALT", "POWERDOWN", "NSF", "POWERUP"}

	method := strings.ToUpper(s.Method)
	for _, valid := range validMethods {
		if method == valid {
			return nil
		}
	}

	return fmt.Errorf("unsupported method '%s', valid methods: %v", s.Method, validMethods)
}

// convertMethod converts string method to RebootMethod enum.
func (s *RebootStep) convertMethod() system.RebootMethod {
	switch strings.ToUpper(s.Method) {
	case "COLD":
		return system.RebootMethod_COLD
	case "WARM":
		return system.RebootMethod_WARM
	case "HALT":
		return system.RebootMethod_HALT
	case "POWERDOWN":
		return system.RebootMethod_POWERDOWN
	case "NSF":
		return system.RebootMethod_NSF
	case "POWERUP":
		return system.RebootMethod_POWERUP
	default:
		return system.RebootMethod_COLD // default fallback
	}
}
