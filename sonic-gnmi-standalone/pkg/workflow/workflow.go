// Package workflow executes multi-step operations from YAML files.
//
// Usage:
//
//	workflow, err := LoadWorkflowFromFile("workflow.yaml")
//	registry := NewRegistry()
//	registry.Register("download", steps.NewDownloadStep)
//	engine := NewEngine(registry)
//	err = engine.Execute(ctx, workflow, client)
package workflow

import (
	"context"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

const (
	// SupportedAPIVersion is the only API version currently supported.
	SupportedAPIVersion = "sonic.net/v1"

	// WorkflowKind is the expected kind for upgrade workflow configurations.
	WorkflowKind = "UpgradeWorkflow"
)

// Step represents a single operation that can be executed within a workflow.
// Each step type implements this interface to provide type-safe parameter
// handling and execution logic.
//
// Steps are responsible for:
//   - Validating their own parameters
//   - Executing their operation using the provided client
//   - Reporting detailed errors with context
//
// Example implementation:
//
//	type DownloadStep struct {
//		Name     string `yaml:"name"`
//		URL      string `yaml:"url"`
//		Filename string `yaml:"filename"`
//		MD5      string `yaml:"md5"`
//	}
//
//	func (s *DownloadStep) Execute(ctx context.Context, client interface{}) error {
//		// Implementation here
//	}
//
//	func (s *DownloadStep) Validate() error {
//		// Validation here
//	}
type Step interface {
	// Execute performs the step's operation using the provided client.
	// The client parameter is typically a gNOI client or similar service client.
	Execute(ctx context.Context, client interface{}) error

	// Validate checks that the step's parameters are valid and complete.
	// This is called before execution to fail fast on invalid configurations.
	Validate() error

	// GetName returns a human-readable name for this step, used in logging.
	GetName() string

	// GetType returns the step type identifier (e.g., "download", "push-config").
	GetType() string
}

// StepFactory creates a step instance from raw YAML parameters.
// Each step type registers a factory function that knows how to parse
// its specific parameter format from the generic params map.
type StepFactory func(name string, params map[string]interface{}) (Step, error)

// StepRegistry manages the mapping between step type names and their factory functions.
// This allows the workflow system to dynamically create step instances based on
// the "type" field in YAML configurations.
type StepRegistry interface {
	// Register associates a step type name with its factory function.
	Register(stepType string, factory StepFactory)

	// CreateStep creates a step instance of the specified type with the given parameters.
	CreateStep(stepType, name string, params map[string]interface{}) (Step, error)

	// GetSupportedTypes returns a list of all registered step types.
	GetSupportedTypes() []string
}

// Workflow represents a complete multi-step operation configuration.
// It follows Kubernetes-style resource definitions with apiVersion, kind, and metadata
// for consistency and future extensibility.
//
// The workflow executes steps sequentially, stopping on the first error.
// Future versions may support parallel execution and conditional steps.
type Workflow struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Steps []RawStep `yaml:"steps"`
	} `yaml:"spec"`
}

// RawStep represents an unparsed step from YAML configuration.
// This is used during the loading phase before steps are converted
// to their specific typed implementations via the step registry.
type RawStep struct {
	// Name is a human-readable identifier for this step.
	Name string `yaml:"name"`

	// Type determines which step implementation to use.
	Type string `yaml:"type"`

	// Params contains type-specific parameters as key-value pairs.
	// These will be parsed by the appropriate step factory.
	Params map[string]interface{} `yaml:"params"`
}

// Engine executes workflows step by step.
type Engine struct {
	registry StepRegistry
}

// NewEngine creates a new workflow execution engine with the provided step registry.
func NewEngine(registry StepRegistry) *Engine {
	return &Engine{registry: registry}
}

// Execute runs a workflow by converting each raw step to its typed implementation
// and executing them sequentially. Execution stops on the first error.
//
// The client parameter is passed to each step's Execute method and typically
// contains gRPC clients or other service connections needed for operations.
func (e *Engine) Execute(ctx context.Context, workflow *Workflow, client interface{}) error {
	fmt.Printf("Executing workflow: %s\n", workflow.Metadata.Name)
	fmt.Printf("Steps: %d\n", len(workflow.Spec.Steps))
	fmt.Println()

	for i, rawStep := range workflow.Spec.Steps {
		fmt.Printf("Step %d/%d: %s\n", i+1, len(workflow.Spec.Steps), rawStep.Name)
		fmt.Printf("  Type: %s\n", rawStep.Type)

		// Convert raw step to typed implementation
		step, err := e.registry.CreateStep(rawStep.Type, rawStep.Name, rawStep.Params)
		if err != nil {
			return fmt.Errorf("failed to create step '%s': %w", rawStep.Name, err)
		}

		// Validate step parameters
		if err := step.Validate(); err != nil {
			return fmt.Errorf("step '%s' validation failed: %w", rawStep.Name, err)
		}

		// Execute the step
		if err := step.Execute(ctx, client); err != nil {
			return fmt.Errorf("step '%s' execution failed: %w", rawStep.Name, err)
		}

		fmt.Printf("  ✓ Step completed successfully\n\n")
	}

	fmt.Println("Workflow completed successfully!")
	return nil
}

// LoadWorkflowFromFile loads and validates a workflow configuration from a YAML file.
//
// The function performs these validations:
//   - File exists and is readable
//   - YAML syntax is valid
//   - Required fields (apiVersion, kind, metadata.name) are present
//   - API version and kind match expected values
//   - At least one step is defined
//   - Each step has required name and type fields
//
// Step-specific parameter validation is deferred to the individual step implementations.
func LoadWorkflowFromFile(path string) (*Workflow, error) {
	// Validate file exists and is readable
	if err := validateWorkflowFile(path); err != nil {
		return nil, err
	}

	// Read file content
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read workflow file: %w", err)
	}

	// Parse YAML
	var workflow Workflow
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	// Validate workflow structure
	if err := validateWorkflowStructure(&workflow); err != nil {
		return nil, fmt.Errorf("invalid workflow: %w", err)
	}

	return &workflow, nil
}

// validateWorkflowFile validates that the workflow file exists and is readable.
func validateWorkflowFile(path string) error {
	if path == "" {
		return fmt.Errorf("workflow file path cannot be empty")
	}

	// Check if file exists
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("workflow file '%s' does not exist", path)
		}
		return fmt.Errorf("cannot access workflow file '%s': %w", path, err)
	}

	// Check if it's a regular file
	if !info.Mode().IsRegular() {
		return fmt.Errorf("workflow file '%s' is not a regular file", path)
	}

	// Check if we can read it
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("cannot read workflow file '%s': %w", path, err)
	}
	file.Close()

	return nil
}

// validateWorkflowStructure validates the basic structure of a workflow configuration.
func validateWorkflowStructure(workflow *Workflow) error {
	// Check required fields
	if workflow.APIVersion == "" {
		return fmt.Errorf("apiVersion is required")
	}
	if workflow.APIVersion != SupportedAPIVersion {
		return fmt.Errorf("unsupported apiVersion: %s (expected %s)", workflow.APIVersion, SupportedAPIVersion)
	}
	if workflow.Kind != WorkflowKind {
		return fmt.Errorf("invalid kind: %s (expected %s)", workflow.Kind, WorkflowKind)
	}
	if workflow.Metadata.Name == "" {
		return fmt.Errorf("metadata.name is required")
	}
	if len(workflow.Spec.Steps) == 0 {
		return fmt.Errorf("at least one step is required")
	}

	// Validate each step
	for i, step := range workflow.Spec.Steps {
		if step.Name == "" {
			return fmt.Errorf("step[%d]: name is required", i)
		}
		if step.Type == "" {
			return fmt.Errorf("step[%d]: type is required", i)
		}
	}

	return nil
}
