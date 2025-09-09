package loopback

import (
	"crypto/md5"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v2"

	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/workflow"
	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/workflow/steps"
)

// WorkflowDefinition represents a structured workflow for YAML generation.
type WorkflowDefinition struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Steps []StepDefinition `yaml:"steps"`
	} `yaml:"spec"`
}

// StepDefinition represents a structured step for YAML generation.
type StepDefinition struct {
	Name   string                 `yaml:"name"`
	Type   string                 `yaml:"type"`
	Params map[string]interface{} `yaml:"params"`
}

// generateWorkflowYAML creates YAML content from structured data.
func generateWorkflowYAML(name string, steps []StepDefinition) (string, error) {
	workflow := WorkflowDefinition{
		APIVersion: "sonic.net/v1",
		Kind:       "UpgradeWorkflow",
	}
	workflow.Metadata.Name = name
	workflow.Spec.Steps = steps

	yamlBytes, err := yaml.Marshal(&workflow)
	if err != nil {
		return "", err
	}
	return string(yamlBytes), nil
}

// TestCheckDiskSpaceLoopback_Success tests successful disk space validation
// when sufficient space is available.
func TestCheckDiskSpaceLoopback_Success(t *testing.T) {
	// Setup test infrastructure
	tempDir := t.TempDir()
	testServer := SetupInsecureTestServer(t, tempDir, []string{"gnmi"})
	defer testServer.Stop()

	// Create workflow with realistic disk space requirements
	// We'll check the root path "/" which should have plenty of space
	workflowSteps := []StepDefinition{
		{
			Name: "check-root-space",
			Type: "check-disk-space",
			Params: map[string]interface{}{
				"path":             "/",
				"min_available_mb": 100, // Require only 100MB - should pass
			},
		},
	}

	workflowContent, err := generateWorkflowYAML("disk-space-success-test", workflowSteps)
	require.NoError(t, err, "Failed to generate workflow YAML")

	workflowFile := filepath.Join(tempDir, "disk-check-success.yaml")
	err = os.WriteFile(workflowFile, []byte(workflowContent), 0644)
	require.NoError(t, err, "Failed to create workflow file")

	// Load and execute workflow
	wf, err := workflow.LoadWorkflowFromFile(workflowFile)
	require.NoError(t, err, "Failed to load workflow")

	// Create step registry and register check-disk-space step
	registry := workflow.NewRegistry()
	registry.Register(steps.CheckDiskSpaceStepType, steps.NewCheckDiskSpaceStep)

	// Create workflow execution engine
	engine := workflow.NewEngine(registry)

	// Prepare client configuration for steps
	clientConfig := map[string]interface{}{
		"server_addr": testServer.Addr,
		"use_tls":     false,
	}

	// Execute the workflow
	ctx, cancel := WithTestTimeout(30 * time.Second)
	defer cancel()

	err = engine.Execute(ctx, wf, clientConfig)
	assert.NoError(t, err, "Workflow execution should succeed when sufficient disk space is available")
}

// TestCheckDiskSpaceLoopback_InsufficientSpace tests failure when insufficient
// disk space is available.
func TestCheckDiskSpaceLoopback_InsufficientSpace(t *testing.T) {
	// Setup test infrastructure
	tempDir := t.TempDir()
	testServer := SetupInsecureTestServer(t, tempDir, []string{"gnmi"})
	defer testServer.Stop()

	// Create workflow with unrealistic disk space requirements
	// We'll require 1TB of space which should definitely fail
	workflowSteps := []StepDefinition{
		{
			Name: "check-unrealistic-space",
			Type: "check-disk-space",
			Params: map[string]interface{}{
				"path":             "/",
				"min_available_mb": 1000000, // Require 1TB - should fail
			},
		},
	}

	workflowContent, err := generateWorkflowYAML("disk-space-failure-test", workflowSteps)
	require.NoError(t, err, "Failed to generate workflow YAML")

	workflowFile := filepath.Join(tempDir, "disk-check-failure.yaml")
	err = os.WriteFile(workflowFile, []byte(workflowContent), 0644)
	require.NoError(t, err, "Failed to create workflow file")

	// Load and execute workflow
	wf, err := workflow.LoadWorkflowFromFile(workflowFile)
	require.NoError(t, err, "Failed to load workflow")

	// Create step registry and register check-disk-space step
	registry := workflow.NewRegistry()
	registry.Register(steps.CheckDiskSpaceStepType, steps.NewCheckDiskSpaceStep)

	// Create workflow execution engine
	engine := workflow.NewEngine(registry)

	// Prepare client configuration for steps
	clientConfig := map[string]interface{}{
		"server_addr": testServer.Addr,
		"use_tls":     false,
	}

	// Execute the workflow
	ctx, cancel := WithTestTimeout(30 * time.Second)
	defer cancel()

	err = engine.Execute(ctx, wf, clientConfig)
	assert.Error(t, err, "Workflow execution should fail when insufficient disk space is available")
	assert.Contains(t, err.Error(), "insufficient disk space", "Error should mention insufficient disk space")
}

// TestCheckDiskSpaceLoopback_InvalidPath tests error handling when querying
// an invalid filesystem path.
func TestCheckDiskSpaceLoopback_InvalidPath(t *testing.T) {
	// Setup test infrastructure
	tempDir := t.TempDir()
	testServer := SetupInsecureTestServer(t, tempDir, []string{"gnmi"})
	defer testServer.Stop()

	// Create workflow with an invalid path
	workflowSteps := []StepDefinition{
		{
			Name: "check-invalid-path",
			Type: "check-disk-space",
			Params: map[string]interface{}{
				"path":             "/nonexistent/invalid/path",
				"min_available_mb": 100,
			},
		},
	}

	workflowContent, err := generateWorkflowYAML("disk-space-invalid-path-test", workflowSteps)
	require.NoError(t, err, "Failed to generate workflow YAML")

	workflowFile := filepath.Join(tempDir, "disk-check-invalid.yaml")
	err = os.WriteFile(workflowFile, []byte(workflowContent), 0644)
	require.NoError(t, err, "Failed to create workflow file")

	// Load and execute workflow
	wf, err := workflow.LoadWorkflowFromFile(workflowFile)
	require.NoError(t, err, "Failed to load workflow")

	// Create step registry and register check-disk-space step
	registry := workflow.NewRegistry()
	registry.Register(steps.CheckDiskSpaceStepType, steps.NewCheckDiskSpaceStep)

	// Create workflow execution engine
	engine := workflow.NewEngine(registry)

	// Prepare client configuration for steps
	clientConfig := map[string]interface{}{
		"server_addr": testServer.Addr,
		"use_tls":     false,
	}

	// Execute the workflow
	ctx, cancel := WithTestTimeout(30 * time.Second)
	defer cancel()

	err = engine.Execute(ctx, wf, clientConfig)
	assert.Error(t, err, "Workflow execution should fail for invalid path")
	assert.Contains(t, err.Error(), "failed to query disk space", "Error should mention query failure")
}

// TestCheckDiskSpaceWorkflowIntegration tests a complete workflow that includes
// both disk space checks and package downloads.
func TestCheckDiskSpaceWorkflowIntegration(t *testing.T) {
	// Create test package content
	testContent := []byte("test package for disk space integration")
	testMD5 := fmt.Sprintf("%x", md5.Sum(testContent))

	// Setup test infrastructure
	tempDir := t.TempDir()
	packagePath := "/tmp/integration-test-package.bin"
	httpServer := SetupHTTPTestServer(testContent)
	defer httpServer.Close()

	testServer := SetupInsecureTestServer(t, tempDir, []string{"gnoi.system", "gnmi"})
	defer testServer.Stop()

	// Create workflow with both disk check and download
	workflowSteps := []StepDefinition{
		{
			Name: "pre-download-disk-check",
			Type: "check-disk-space",
			Params: map[string]interface{}{
				"path":             "/",
				"min_available_mb": 50, // Small requirement for test
			},
		},
		{
			Name: "download-package",
			Type: "download",
			Params: map[string]interface{}{
				"url":      httpServer.URL,
				"filename": packagePath,
				"md5":      testMD5,
				"version":  "integration-test",
				"activate": false,
			},
		},
	}

	workflowContent, err := generateWorkflowYAML("integration-disk-check-download", workflowSteps)
	require.NoError(t, err, "Failed to generate workflow YAML")

	workflowFile := filepath.Join(tempDir, "integration-workflow.yaml")
	err = os.WriteFile(workflowFile, []byte(workflowContent), 0644)
	require.NoError(t, err, "Failed to create workflow file")

	// Load and execute workflow
	wf, err := workflow.LoadWorkflowFromFile(workflowFile)
	require.NoError(t, err, "Failed to load workflow")

	// Create step registry and register both step types
	registry := workflow.NewRegistry()
	registry.Register(steps.CheckDiskSpaceStepType, steps.NewCheckDiskSpaceStep)
	registry.Register(steps.DownloadStepType, steps.NewDownloadStep)

	// Create workflow execution engine
	engine := workflow.NewEngine(registry)

	// Prepare client configuration for steps
	clientConfig := map[string]interface{}{
		"server_addr": testServer.Addr,
		"use_tls":     false,
	}

	// Execute the workflow
	ctx, cancel := WithTestTimeout(60 * time.Second)
	defer cancel()

	err = engine.Execute(ctx, wf, clientConfig)
	require.NoError(t, err, "Integrated workflow execution failed")

	// Verify package was downloaded after disk check passed
	expectedPath := filepath.Join(tempDir, packagePath)
	assert.FileExists(t, expectedPath, "Package should be downloaded after disk check passes")

	// Verify downloaded content
	downloadedContent, err := os.ReadFile(expectedPath)
	require.NoError(t, err, "Failed to read downloaded package")
	assert.Equal(t, testContent, downloadedContent, "Downloaded content should match original")
}

// TestCheckDiskSpaceWorkflowIntegration_FailsBeforeDownload tests that insufficient
// disk space prevents the download step from executing.
func TestCheckDiskSpaceWorkflowIntegration_FailsBeforeDownload(t *testing.T) {
	// Create test package content
	testContent := []byte("test package for disk space failure integration")
	testMD5 := fmt.Sprintf("%x", md5.Sum(testContent))

	// Setup test infrastructure
	tempDir := t.TempDir()
	packagePath := "/tmp/should-not-download.bin"
	httpServer := SetupHTTPTestServer(testContent)
	defer httpServer.Close()

	testServer := SetupInsecureTestServer(t, tempDir, []string{"gnoi.system", "gnmi"})
	defer testServer.Stop()

	// Create workflow with unrealistic disk requirement
	workflowSteps := []StepDefinition{
		{
			Name: "impossible-disk-check",
			Type: "check-disk-space",
			Params: map[string]interface{}{
				"path":             "/",
				"min_available_mb": 2000000, // Require 2TB - should fail
			},
		},
		{
			Name: "should-not-download",
			Type: "download",
			Params: map[string]interface{}{
				"url":      httpServer.URL,
				"filename": packagePath,
				"md5":      testMD5,
				"version":  "should-not-happen",
				"activate": false,
			},
		},
	}

	workflowContent, err := generateWorkflowYAML("disk-check-prevents-download", workflowSteps)
	require.NoError(t, err, "Failed to generate workflow YAML")

	workflowFile := filepath.Join(tempDir, "fail-before-download.yaml")
	err = os.WriteFile(workflowFile, []byte(workflowContent), 0644)
	require.NoError(t, err, "Failed to create workflow file")

	// Load and execute workflow
	wf, err := workflow.LoadWorkflowFromFile(workflowFile)
	require.NoError(t, err, "Failed to load workflow")

	// Create step registry and register both step types
	registry := workflow.NewRegistry()
	registry.Register(steps.CheckDiskSpaceStepType, steps.NewCheckDiskSpaceStep)
	registry.Register(steps.DownloadStepType, steps.NewDownloadStep)

	// Create workflow execution engine
	engine := workflow.NewEngine(registry)

	// Prepare client configuration for steps
	clientConfig := map[string]interface{}{
		"server_addr": testServer.Addr,
		"use_tls":     false,
	}

	// Execute the workflow
	ctx, cancel := WithTestTimeout(30 * time.Second)
	defer cancel()

	err = engine.Execute(ctx, wf, clientConfig)
	assert.Error(t, err, "Workflow should fail due to insufficient disk space")
	assert.Contains(t, err.Error(), "insufficient disk space", "Error should mention insufficient disk space")

	// Verify package was NOT downloaded
	expectedPath := filepath.Join(tempDir, packagePath)
	assert.NoFileExists(t, expectedPath, "Package should NOT be downloaded when disk check fails")
}

// TestCheckDiskSpaceMultiPath tests checking multiple filesystem paths in sequence.
func TestCheckDiskSpaceMultiPath(t *testing.T) {
	// Setup test infrastructure
	tempDir := t.TempDir()
	testServer := SetupInsecureTestServer(t, tempDir, []string{"gnmi"})
	defer testServer.Stop()

	// Create workflow checking multiple paths
	workflowSteps := []StepDefinition{
		{
			Name: "check-root-space",
			Type: "check-disk-space",
			Params: map[string]interface{}{
				"path":             "/",
				"min_available_mb": 100,
			},
		},
		{
			Name: "check-tmp-space",
			Type: "check-disk-space",
			Params: map[string]interface{}{
				"path":             "/",
				"min_available_mb": 50,
			},
		},
		{
			Name: "check-var-space",
			Type: "check-disk-space",
			Params: map[string]interface{}{
				"path":             "/",
				"min_available_mb": 75,
			},
		},
	}

	workflowContent, err := generateWorkflowYAML("multi-path-disk-check", workflowSteps)
	require.NoError(t, err, "Failed to generate workflow YAML")

	workflowFile := filepath.Join(tempDir, "multi-path-check.yaml")
	err = os.WriteFile(workflowFile, []byte(workflowContent), 0644)
	require.NoError(t, err, "Failed to create workflow file")

	// Load and execute workflow
	wf, err := workflow.LoadWorkflowFromFile(workflowFile)
	require.NoError(t, err, "Failed to load workflow")

	// Create step registry and register check-disk-space step
	registry := workflow.NewRegistry()
	registry.Register(steps.CheckDiskSpaceStepType, steps.NewCheckDiskSpaceStep)

	// Create workflow execution engine
	engine := workflow.NewEngine(registry)

	// Prepare client configuration for steps
	clientConfig := map[string]interface{}{
		"server_addr": testServer.Addr,
		"use_tls":     false,
	}

	// Execute the workflow
	ctx, cancel := WithTestTimeout(45 * time.Second)
	defer cancel()

	err = engine.Execute(ctx, wf, clientConfig)
	assert.NoError(t, err, "Multi-path disk check should succeed with reasonable requirements")
}
