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

// TestUpgradeAgentWorkflowLoopback tests the complete upgrade-agent workflow system
// against a real sonic-gnmi-standalone server.
func TestUpgradeAgentWorkflowLoopback(t *testing.T) {
	// Create test package content
	testContent := []byte("test package content for upgrade-agent workflow integration test")
	testMD5 := fmt.Sprintf("%x", md5.Sum(testContent))

	// Setup test infrastructure
	tempDir := t.TempDir()
	packagePath := "/tmp/workflow-test-package.bin"
	httpServer := SetupHTTPTestServer(testContent)
	defer httpServer.Close()

	testServer := SetupInsecureTestServer(t, tempDir, []string{"gnoi.system"})
	defer testServer.Stop()

	// Create workflow YAML file
	workflowContent := fmt.Sprintf(`apiVersion: sonic.net/v1
kind: UpgradeWorkflow
metadata:
  name: integration-test-workflow
spec:
  steps:
    - name: download-test-package
      type: download
      params:
        url: "%s"
        filename: "%s"
        md5: "%s"
        version: "1.2.3"
        activate: false
`, httpServer.URL, packagePath, testMD5)

	workflowFile := filepath.Join(tempDir, "test-workflow.yaml")
	err := os.WriteFile(workflowFile, []byte(workflowContent), 0644)
	require.NoError(t, err, "Failed to create workflow file")

	// Load workflow using the workflow engine
	wf, err := workflow.LoadWorkflowFromFile(workflowFile)
	require.NoError(t, err, "Failed to load workflow")

	// Create step registry and register download step
	registry := workflow.NewRegistry()
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
	require.NoError(t, err, "Workflow execution failed")

	// Verify package was downloaded correctly
	expectedPath := filepath.Join(tempDir, packagePath)
	installedContent, err := os.ReadFile(expectedPath)
	require.NoError(t, err, "Failed to read installed package")

	assert.Equal(t, testContent, installedContent, "Package content mismatch")

	// Verify MD5 checksum
	actualMD5 := fmt.Sprintf("%x", md5.Sum(installedContent))
	assert.Equal(t, testMD5, actualMD5, "MD5 checksum mismatch")
}

// TestUpgradeAgentWorkflowLoopback_MultiStep tests multi-step workflow execution.
func TestUpgradeAgentWorkflowLoopback_MultiStep(t *testing.T) {
	// Create test content for two packages
	package1Content := []byte("first package content")
	package2Content := []byte("second package content")
	package1MD5 := fmt.Sprintf("%x", md5.Sum(package1Content))
	package2MD5 := fmt.Sprintf("%x", md5.Sum(package2Content))

	// Setup test infrastructure
	tempDir := t.TempDir()
	httpServer1 := SetupHTTPTestServer(package1Content)
	defer httpServer1.Close()
	httpServer2 := SetupHTTPTestServer(package2Content)
	defer httpServer2.Close()

	testServer := SetupInsecureTestServer(t, tempDir, []string{"gnoi.system"})
	defer testServer.Stop()

	// Create multi-step workflow YAML
	workflowContent := fmt.Sprintf(`apiVersion: sonic.net/v1
kind: UpgradeWorkflow
metadata:
  name: multi-step-test-workflow
spec:
  steps:
    - name: download-package-1
      type: download
      params:
        url: "%s"
        filename: "/tmp/package1.bin"
        md5: "%s"
        version: "1.0.0"
        activate: false
    - name: download-package-2
      type: download
      params:
        url: "%s"
        filename: "/tmp/package2.bin"
        md5: "%s"
        version: "2.0.0"
        activate: false
`, httpServer1.URL, package1MD5, httpServer2.URL, package2MD5)

	workflowFile := filepath.Join(tempDir, "multi-step-workflow.yaml")
	err := os.WriteFile(workflowFile, []byte(workflowContent), 0644)
	require.NoError(t, err)

	// Execute workflow
	wf, err := workflow.LoadWorkflowFromFile(workflowFile)
	require.NoError(t, err)

	registry := workflow.NewRegistry()
	registry.Register(steps.DownloadStepType, steps.NewDownloadStep)
	engine := workflow.NewEngine(registry)

	clientConfig := map[string]interface{}{
		"server_addr": testServer.Addr,
		"use_tls":     false,
	}

	ctx, cancel := WithTestTimeout(60 * time.Second)
	defer cancel()

	err = engine.Execute(ctx, wf, clientConfig)
	require.NoError(t, err, "Multi-step workflow execution failed")

	// Verify both packages were downloaded
	package1Path := filepath.Join(tempDir, "/tmp/package1.bin")
	package2Path := filepath.Join(tempDir, "/tmp/package2.bin")

	content1, err := os.ReadFile(package1Path)
	require.NoError(t, err)
	assert.Equal(t, package1Content, content1)

	content2, err := os.ReadFile(package2Path)
	require.NoError(t, err)
	assert.Equal(t, package2Content, content2)
}

// TestUpgradeAgentWorkflowLoopback_InvalidWorkflow tests error handling for invalid workflows.
func TestUpgradeAgentWorkflowLoopback_InvalidWorkflow(t *testing.T) {
	tempDir := t.TempDir()

	tests := []struct {
		name         string
		workflowYAML string
		expectError  string
	}{
		{
			name: "invalid_api_version",
			workflowYAML: `apiVersion: invalid/v1
kind: UpgradeWorkflow
metadata:
  name: test
spec:
  steps: []`,
			expectError: "unsupported apiVersion",
		},
		{
			name: "missing_steps",
			workflowYAML: `apiVersion: sonic.net/v1
kind: UpgradeWorkflow
metadata:
  name: test
spec:
  steps: []`,
			expectError: "at least one step is required",
		},
		{
			name: "unknown_step_type",
			workflowYAML: `apiVersion: sonic.net/v1
kind: UpgradeWorkflow
metadata:
  name: test
spec:
  steps:
    - name: invalid-step
      type: unknown-type
      params: {}`,
			expectError: "unknown step type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workflowFile := filepath.Join(tempDir, tt.name+".yaml")
			err := os.WriteFile(workflowFile, []byte(tt.workflowYAML), 0644)
			require.NoError(t, err)

			// Try to load and execute workflow
			wf, err := workflow.LoadWorkflowFromFile(workflowFile)
			if err != nil {
				assert.Contains(t, err.Error(), tt.expectError)
				return
			}

			registry := workflow.NewRegistry()
			registry.Register(steps.DownloadStepType, steps.NewDownloadStep)
			engine := workflow.NewEngine(registry)

			clientConfig := map[string]interface{}{
				"server_addr": "127.0.0.1:12345", // dummy address
				"use_tls":     false,
			}

			ctx, cancel := WithTestTimeout(5 * time.Second)
			defer cancel()

			err = engine.Execute(ctx, wf, clientConfig)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectError)
		})
	}
}

// TestUpgradeAgentWorkflowLoopback_DownloadError tests error handling during package download.
func TestUpgradeAgentWorkflowLoopback_DownloadError(t *testing.T) {
	tempDir := t.TempDir()
	testServer := SetupInsecureTestServer(t, tempDir, []string{"gnoi.system"})
	defer testServer.Stop()

	tests := []struct {
		name        string
		url         string
		md5         string
		expectError string
	}{
		{
			name:        "invalid_url",
			url:         "http://invalid-host-that-does-not-exist.example.com/package.bin",
			md5:         "d41d8cd98f00b204e9800998ecf8427e",
			expectError: "failed to download package",
		},
		{
			name:        "invalid_md5_format",
			url:         "http://httpbin.org/robots.txt",
			md5:         "invalid-md5-format",
			expectError: "MD5 checksum must be 32 characters",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workflowContent := fmt.Sprintf(`apiVersion: sonic.net/v1
kind: UpgradeWorkflow
metadata:
  name: error-test-workflow
spec:
  steps:
    - name: download-with-error
      type: download
      params:
        url: "%s"
        filename: "/tmp/error-test.bin"
        md5: "%s"
        version: "1.0.0"
`, tt.url, tt.md5)

			workflowFile := filepath.Join(tempDir, tt.name+".yaml")
			err := os.WriteFile(workflowFile, []byte(workflowContent), 0644)
			require.NoError(t, err)

			wf, err := workflow.LoadWorkflowFromFile(workflowFile)
			require.NoError(t, err)

			registry := workflow.NewRegistry()
			registry.Register(steps.DownloadStepType, steps.NewDownloadStep)
			engine := workflow.NewEngine(registry)

			clientConfig := map[string]interface{}{
				"server_addr": testServer.Addr,
				"use_tls":     false,
			}

			ctx, cancel := WithTestTimeout(10 * time.Second)
			defer cancel()

			err = engine.Execute(ctx, wf, clientConfig)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectError)
		})
	}
}

// TestUpgradeAgentWorkflowLoopback_StepValidation tests step parameter validation.
func TestUpgradeAgentWorkflowLoopback_StepValidation(t *testing.T) {
	tempDir := t.TempDir()

	tests := []struct {
		name        string
		params      string
		expectError string
	}{
		{
			name: "missing_url",
			params: `
        filename: "/tmp/test.bin"
        md5: "d41d8cd98f00b204e9800998ecf8427e"`,
			expectError: "url parameter is required",
		},
		{
			name: "missing_filename",
			params: `
        url: "http://example.com/test.bin"
        md5: "d41d8cd98f00b204e9800998ecf8427e"`,
			expectError: "filename parameter is required",
		},
		{
			name: "missing_md5",
			params: `
        url: "http://example.com/test.bin"
        filename: "/tmp/test.bin"`,
			expectError: "md5 parameter is required",
		},
		{
			name: "relative_filename",
			params: `
        url: "http://example.com/test.bin"
        filename: "relative/path.bin"
        md5: "d41d8cd98f00b204e9800998ecf8427e"`,
			expectError: "filename must be an absolute path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workflowContent := fmt.Sprintf(`apiVersion: sonic.net/v1
kind: UpgradeWorkflow
metadata:
  name: validation-test-workflow
spec:
  steps:
    - name: validation-test-step
      type: download
      params:%s
`, tt.params)

			workflowFile := filepath.Join(tempDir, tt.name+".yaml")
			err := os.WriteFile(workflowFile, []byte(workflowContent), 0644)
			require.NoError(t, err)

			wf, err := workflow.LoadWorkflowFromFile(workflowFile)
			require.NoError(t, err)

			registry := workflow.NewRegistry()
			registry.Register(steps.DownloadStepType, steps.NewDownloadStep)
			engine := workflow.NewEngine(registry)

			clientConfig := map[string]interface{}{
				"server_addr": "127.0.0.1:12345", // dummy address
				"use_tls":     false,
			}

			ctx, cancel := WithTestTimeout(5 * time.Second)
			defer cancel()

			err = engine.Execute(ctx, wf, clientConfig)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectError)
		})
	}
}
