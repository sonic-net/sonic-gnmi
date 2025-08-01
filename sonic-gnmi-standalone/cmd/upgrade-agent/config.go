// Package main provides configuration loading and validation for upgrade workflows.
//
// This file implements support for the UpgradeWorkflow YAML format, which allows
// defining multi-step upgrade operations in a declarative manner. The format follows
// Kubernetes-style resource definitions for consistency and familiarity.
//
// YAML Configuration Structure:
//
//	apiVersion: sonic.net/v1
//	kind: UpgradeWorkflow
//	metadata:
//	  name: my-upgrade-workflow
//	spec:
//	  steps:
//	    - name: download-package
//	      type: download
//	      params:
//	        url: "http://example.com/package.bin"
//	        filename: "/tmp/package.bin"
//	        md5: "d41d8cd98f00b204e9800998ecf8427e"
//	        version: "1.0.0"      # optional
//	        activate: false       # optional
//
// Server connection details are provided via command-line flags, making
// YAML files reusable across different environments.
package main

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/upgrade"
)

const (
	// SupportedAPIVersion is the only API version currently supported
	SupportedAPIVersion = "sonic.net/v1"

	// WorkflowKind is the expected kind for upgrade workflow configurations
	WorkflowKind = "UpgradeWorkflow"

	// StepTypeDownload indicates a package download and install operation
	StepTypeDownload = "download"
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

// Step represents a single operation in an upgrade workflow.
// Each step has a unique name, type, and type-specific parameters.
//
// Currently supported step types:
//   - download: Downloads and installs a package via HTTP
//
// Example:
//
//	name: download-sonic-image
//	type: download
//	params:
//	  url: http://example.com/sonic.bin
//	  filename: /tmp/sonic.bin
//	  md5: d41d8cd98f00b204e9800998ecf8427e
type Step struct {
	// Name is a human-readable identifier for this step.
	// Used in logging and error messages to identify which step failed.
	Name string `yaml:"name"`

	// Type determines the operation to perform.
	// Must be one of the supported step types (currently only "download").
	Type string `yaml:"type"`

	// Params contains type-specific parameters as key-value pairs.
	// Required parameters depend on the step type.
	Params map[string]interface{} `yaml:"params"`
}

// WorkflowConfig represents a complete upgrade workflow configuration.
// It follows Kubernetes-style resource definitions with apiVersion, kind, and metadata.
//
// The workflow executes steps sequentially, stopping on the first error.
// Server connection details are provided via command-line flags, not in the config.
//
// Example:
//
//	apiVersion: sonic.net/v1
//	kind: UpgradeWorkflow
//	metadata:
//	  name: sonic-os-upgrade
//	spec:
//	  steps:
//	    - name: download-image
//	      type: download
//	      params:
//	        url: http://example.com/sonic.bin
//	        filename: /tmp/sonic.bin
//	        md5: abc123...
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
	if config.APIVersion != SupportedAPIVersion {
		return fmt.Errorf("unsupported apiVersion: %s (expected %s)", config.APIVersion, SupportedAPIVersion)
	}
	if config.Kind != WorkflowKind {
		return fmt.Errorf("invalid kind: %s (expected %s)", config.Kind, WorkflowKind)
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
		if step.Type != StepTypeDownload {
			return fmt.Errorf("step[%d]: unsupported type '%s' (only '%s' is currently supported)", i, step.Type, StepTypeDownload)
		}
	}

	return nil
}

// LoadConfigurationFile loads an UpgradeWorkflow configuration file.
func LoadConfigurationFile(path string) (*WorkflowConfig, error) {
	return LoadWorkflowFromFile(path)
}

// ConvertStepToConfig transforms a workflow step into a Config implementation.
//
// This function bridges between the generic workflow Step format and the
// specific Config interface required by the upgrade package. It extracts
// and validates type-specific parameters from the step's params map.
//
// Currently only supports "download" steps with these required parameters:
//   - url (string): HTTP/HTTPS URL of the package
//   - filename (string): Absolute path where to save the package
//   - md5 (string): Expected MD5 checksum of the package
//
// Optional parameters:
//   - version (string): Package version for tracking
//   - activate (bool): Whether to activate after installation
//
// Returns an error if required parameters are missing or have wrong types.
func ConvertStepToConfig(step Step) (upgrade.Config, error) {
	if step.Type != StepTypeDownload {
		return nil, fmt.Errorf("can only convert %s steps, got %s", StepTypeDownload, step.Type)
	}

	// Create DownloadOptions from step params
	opts := &upgrade.DownloadOptions{}

	// Extract required parameters with detailed error messages
	if url, ok := step.Params["url"].(string); ok {
		opts.URL = url
	} else {
		return nil, fmt.Errorf("step '%s': url parameter is required and must be a string", step.Name)
	}

	if filename, ok := step.Params["filename"].(string); ok {
		opts.Filename = filename
	} else {
		return nil, fmt.Errorf("step '%s': filename parameter is required and must be a string", step.Name)
	}

	if md5, ok := step.Params["md5"].(string); ok {
		opts.MD5 = md5
	} else {
		return nil, fmt.Errorf("step '%s': md5 parameter is required and must be a string (32 hex characters)", step.Name)
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
