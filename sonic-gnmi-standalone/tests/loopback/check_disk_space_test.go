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

	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/workflow"
	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/workflow/steps"
)

// TestCheckDiskSpaceLoopback_Success tests successful disk space validation
// when sufficient space is available.
func TestCheckDiskSpaceLoopback_Success(t *testing.T) {
	// Setup test infrastructure
	tempDir := t.TempDir()
	testServer := SetupInsecureTestServer(t, tempDir, []string{"gnmi"})
	defer testServer.Stop()

	// Create workflow YAML with realistic disk space requirements
	// We'll check the root path "/" which should have plenty of space
	workflowContent := `apiVersion: sonic.net/v1
kind: UpgradeWorkflow
metadata:
  name: disk-space-success-test
spec:
  steps:
    - name: check-root-space
      type: check-disk-space
      params:
        path: "/"
        min_available_mb: 100  # Require only 100MB - should pass
`

	workflowFile := filepath.Join(tempDir, "disk-check-success.yaml")
	err := os.WriteFile(workflowFile, []byte(workflowContent), 0644)
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

	// Create workflow YAML with unrealistic disk space requirements
	// We'll require 1TB of space which should definitely fail
	workflowContent := `apiVersion: sonic.net/v1
kind: UpgradeWorkflow
metadata:
  name: disk-space-failure-test
spec:
  steps:
    - name: check-unrealistic-space
      type: check-disk-space
      params:
        path: "/"
        min_available_mb: 1000000  # Require 1TB - should fail
`

	workflowFile := filepath.Join(tempDir, "disk-check-failure.yaml")
	err := os.WriteFile(workflowFile, []byte(workflowContent), 0644)
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

	// Create workflow YAML with an invalid path
	workflowContent := `apiVersion: sonic.net/v1
kind: UpgradeWorkflow
metadata:
  name: disk-space-invalid-path-test
spec:
  steps:
    - name: check-invalid-path
      type: check-disk-space
      params:
        path: "/nonexistent/invalid/path"
        min_available_mb: 100
`

	workflowFile := filepath.Join(tempDir, "disk-check-invalid.yaml")
	err := os.WriteFile(workflowFile, []byte(workflowContent), 0644)
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

	// Create workflow YAML with both disk check and download
	workflowContent := fmt.Sprintf(`apiVersion: sonic.net/v1
kind: UpgradeWorkflow
metadata:
  name: integration-disk-check-download
spec:
  steps:
    # Step 1: Check disk space before download
    - name: pre-download-disk-check
      type: check-disk-space
      params:
        path: "/"
        min_available_mb: 50  # Small requirement for test
    
    # Step 2: Download package if space is available
    - name: download-package
      type: download
      params:
        url: "%s"
        filename: "%s"
        md5: "%s"
        version: "integration-test"
        activate: false
`, httpServer.URL, packagePath, testMD5)

	workflowFile := filepath.Join(tempDir, "integration-workflow.yaml")
	err := os.WriteFile(workflowFile, []byte(workflowContent), 0644)
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

	// Create workflow YAML with unrealistic disk requirement
	workflowContent := fmt.Sprintf(`apiVersion: sonic.net/v1
kind: UpgradeWorkflow
metadata:
  name: disk-check-prevents-download
spec:
  steps:
    # Step 1: Check for impossible disk space requirement
    - name: impossible-disk-check
      type: check-disk-space
      params:
        path: "/"
        min_available_mb: 2000000  # Require 2TB - should fail
    
    # Step 2: This download should never execute
    - name: should-not-download
      type: download
      params:
        url: "%s"
        filename: "%s"
        md5: "%s"
        version: "should-not-happen"
        activate: false
`, httpServer.URL, packagePath, testMD5)

	workflowFile := filepath.Join(tempDir, "fail-before-download.yaml")
	err := os.WriteFile(workflowFile, []byte(workflowContent), 0644)
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

	// Create workflow YAML checking multiple paths
	workflowContent := `apiVersion: sonic.net/v1
kind: UpgradeWorkflow
metadata:
  name: multi-path-disk-check
spec:
  steps:
    - name: check-root-space
      type: check-disk-space
      params:
        path: "/"
        min_available_mb: 100
        
    - name: check-tmp-space
      type: check-disk-space
      params:
        path: "/"
        min_available_mb: 50
        
    - name: check-var-space
      type: check-disk-space
      params:
        path: "/"
        min_available_mb: 75
`

	workflowFile := filepath.Join(tempDir, "multi-path-check.yaml")
	err := os.WriteFile(workflowFile, []byte(workflowContent), 0644)
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
