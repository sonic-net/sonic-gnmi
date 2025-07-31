package main

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/upgrade"
)

// validateConfigFile validates that the config file exists and is readable.
func validateConfigFile(path string) error {
	if path == "" {
		return fmt.Errorf("config file path cannot be empty")
	}

	// Check if file exists
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("config file '%s' does not exist", path)
		}
		return fmt.Errorf("cannot access config file '%s': %w", path, err)
	}

	// Check if it's a regular file
	if !info.Mode().IsRegular() {
		return fmt.Errorf("config file '%s' is not a regular file", path)
	}

	// Check if we can read it
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("cannot read config file '%s': %w", path, err)
	}
	file.Close()

	return nil
}

// Step represents a single step in a workflow.
type Step struct {
	Name   string                 `yaml:"name"`
	Type   string                 `yaml:"type"`
	Params map[string]interface{} `yaml:"params"`
}

// WorkflowConfig represents the new workflow-based configuration.
type WorkflowConfig struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Steps []Step `yaml:"steps"`
	} `yaml:"spec"`
}

// LoadWorkflowFromFile loads a workflow configuration file.
func LoadWorkflowFromFile(path string) (*WorkflowConfig, error) {
	// Validate file exists and is readable
	if err := validateConfigFile(path); err != nil {
		return nil, err
	}

	// Read file content
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Parse YAML
	var config WorkflowConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	// Validate workflow structure
	if err := validateWorkflowStructure(&config); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &config, nil
}

// validateWorkflowStructure validates the workflow structure.
func validateWorkflowStructure(config *WorkflowConfig) error {
	// Check required fields
	if config.APIVersion == "" {
		return fmt.Errorf("apiVersion is required")
	}
	if config.APIVersion != "sonic.net/v1" {
		return fmt.Errorf("unsupported apiVersion: %s (expected sonic.net/v1)", config.APIVersion)
	}
	if config.Kind != "UpgradeWorkflow" {
		return fmt.Errorf("invalid kind: %s (expected UpgradeWorkflow)", config.Kind)
	}
	if config.Metadata.Name == "" {
		return fmt.Errorf("metadata.name is required")
	}
	if len(config.Spec.Steps) == 0 {
		return fmt.Errorf("at least one step is required")
	}

	// Validate each step
	for i, step := range config.Spec.Steps {
		if step.Name == "" {
			return fmt.Errorf("step[%d]: name is required", i)
		}
		if step.Type == "" {
			return fmt.Errorf("step[%d]: type is required", i)
		}
		// For now, only support download type
		if step.Type != "download" {
			return fmt.Errorf("step[%d]: unsupported type '%s' (only 'download' is currently supported)", i, step.Type)
		}
	}

	return nil
}

// LoadConfigurationFile loads an UpgradeWorkflow configuration file.
func LoadConfigurationFile(path string) (*WorkflowConfig, error) {
	return LoadWorkflowFromFile(path)
}

// ConvertStepToConfig converts a download step to a Config interface for reuse.
func ConvertStepToConfig(step Step) (upgrade.Config, error) {
	if step.Type != "download" {
		return nil, fmt.Errorf("can only convert download steps")
	}

	// Create DownloadOptions from step params
	opts := &upgrade.DownloadOptions{}

	// Extract parameters
	if url, ok := step.Params["url"].(string); ok {
		opts.URL = url
	} else {
		return nil, fmt.Errorf("step '%s': url parameter is required", step.Name)
	}

	if filename, ok := step.Params["filename"].(string); ok {
		opts.Filename = filename
	} else {
		return nil, fmt.Errorf("step '%s': filename parameter is required", step.Name)
	}

	if md5, ok := step.Params["md5"].(string); ok {
		opts.MD5 = md5
	} else {
		return nil, fmt.Errorf("step '%s': md5 parameter is required", step.Name)
	}

	// Optional parameters
	if version, ok := step.Params["version"].(string); ok {
		opts.Version = version
	}

	if activate, ok := step.Params["activate"].(bool); ok {
		opts.Activate = activate
	}

	return opts, nil
}
