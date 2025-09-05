package steps

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCheckDiskSpaceStep_ValidParameters(t *testing.T) {
	tests := []struct {
		name          string
		stepName      string
		params        map[string]interface{}
		expectedPath  string
		expectedMinMB int64
	}{
		{
			name:     "valid parameters with int",
			stepName: "test-step",
			params: map[string]interface{}{
				"path":             "/var/log",
				"min_available_mb": 1024,
			},
			expectedPath:  "/var/log",
			expectedMinMB: 1024,
		},
		{
			name:     "valid parameters with int64",
			stepName: "test-step-int64",
			params: map[string]interface{}{
				"path":             "/",
				"min_available_mb": int64(2048),
			},
			expectedPath:  "/",
			expectedMinMB: 2048,
		},
		{
			name:     "valid parameters with float64",
			stepName: "test-step-float",
			params: map[string]interface{}{
				"path":             "/tmp",
				"min_available_mb": 512.0,
			},
			expectedPath:  "/tmp",
			expectedMinMB: 512,
		},
		{
			name:     "valid parameters with large value",
			stepName: "test-large",
			params: map[string]interface{}{
				"path":             "/host/data",
				"min_available_mb": 1000000,
			},
			expectedPath:  "/host/data",
			expectedMinMB: 1000000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step, err := NewCheckDiskSpaceStep(tt.stepName, tt.params)
			require.NoError(t, err)
			require.NotNil(t, step)

			diskStep, ok := step.(*CheckDiskSpaceStep)
			require.True(t, ok, "Step should be CheckDiskSpaceStep type")

			assert.Equal(t, tt.stepName, diskStep.GetName())
			assert.Equal(t, CheckDiskSpaceStepType, diskStep.GetType())
			assert.Equal(t, tt.expectedPath, diskStep.Path)
			assert.Equal(t, tt.expectedMinMB, diskStep.MinAvailableMB)
		})
	}
}

func TestNewCheckDiskSpaceStep_InvalidParameters(t *testing.T) {
	tests := []struct {
		name           string
		params         map[string]interface{}
		expectedErrMsg string
	}{
		{
			name:           "missing path parameter",
			params:         map[string]interface{}{"min_available_mb": 1024},
			expectedErrMsg: "missing or invalid 'path' parameter",
		},
		{
			name: "invalid path type",
			params: map[string]interface{}{
				"path":             123,
				"min_available_mb": 1024,
			},
			expectedErrMsg: "missing or invalid 'path' parameter",
		},
		{
			name:           "missing min_available_mb parameter",
			params:         map[string]interface{}{"path": "/var/log"},
			expectedErrMsg: "missing or invalid 'min_available_mb' parameter",
		},
		{
			name: "invalid min_available_mb type",
			params: map[string]interface{}{
				"path":             "/var/log",
				"min_available_mb": "not-a-number",
			},
			expectedErrMsg: "missing or invalid 'min_available_mb' parameter",
		},
		{
			name: "empty path",
			params: map[string]interface{}{
				"path":             "",
				"min_available_mb": 1024,
			},
			expectedErrMsg: "validation failed: path cannot be empty",
		},
		{
			name: "zero min_available_mb",
			params: map[string]interface{}{
				"path":             "/var/log",
				"min_available_mb": 0,
			},
			expectedErrMsg: "validation failed: min_available_mb must be positive, got 0",
		},
		{
			name: "negative min_available_mb",
			params: map[string]interface{}{
				"path":             "/var/log",
				"min_available_mb": -500,
			},
			expectedErrMsg: "validation failed: min_available_mb must be positive, got -500",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step, err := NewCheckDiskSpaceStep("test-step", tt.params)
			assert.Error(t, err)
			assert.Nil(t, step)
			assert.Contains(t, err.Error(), tt.expectedErrMsg)
		})
	}
}

func TestCheckDiskSpaceStep_Properties(t *testing.T) {
	params := map[string]interface{}{
		"path":             "/var/log",
		"min_available_mb": 1024,
	}

	step, err := NewCheckDiskSpaceStep("my-disk-check", params)
	require.NoError(t, err)

	assert.Equal(t, "my-disk-check", step.GetName())
	assert.Equal(t, CheckDiskSpaceStepType, step.GetType())
}

func TestCheckDiskSpaceStep_Validate(t *testing.T) {
	tests := []struct {
		name      string
		step      *CheckDiskSpaceStep
		wantError bool
		errorMsg  string
	}{
		{
			name: "valid step",
			step: &CheckDiskSpaceStep{
				Path:           "/var/log",
				MinAvailableMB: 1024,
			},
			wantError: false,
		},
		{
			name: "empty path",
			step: &CheckDiskSpaceStep{
				Path:           "",
				MinAvailableMB: 1024,
			},
			wantError: true,
			errorMsg:  "path cannot be empty",
		},
		{
			name: "zero min available MB",
			step: &CheckDiskSpaceStep{
				Path:           "/var/log",
				MinAvailableMB: 0,
			},
			wantError: true,
			errorMsg:  "min_available_mb must be positive, got 0",
		},
		{
			name: "negative min available MB",
			step: &CheckDiskSpaceStep{
				Path:           "/var/log",
				MinAvailableMB: -100,
			},
			wantError: true,
			errorMsg:  "min_available_mb must be positive, got -100",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.step.Validate()
			if tt.wantError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestCheckDiskSpaceStep_Execute_InvalidClientConfig(t *testing.T) {
	step := &CheckDiskSpaceStep{
		Path:           "/var/log",
		MinAvailableMB: 1024,
	}

	tests := []struct {
		name         string
		clientConfig interface{}
		expectedErr  string
	}{
		{
			name:         "nil config",
			clientConfig: nil,
			expectedErr:  "invalid client configuration type",
		},
		{
			name:         "wrong type",
			clientConfig: "not-a-map",
			expectedErr:  "invalid client configuration type",
		},
		{
			name:         "missing server_addr",
			clientConfig: map[string]interface{}{"use_tls": false},
			expectedErr:  "server_addr not found in client configuration",
		},
		{
			name: "empty server_addr",
			clientConfig: map[string]interface{}{
				"server_addr": "",
				"use_tls":     false,
			},
			expectedErr: "server_addr not found in client configuration",
		},
		{
			name: "invalid server_addr type",
			clientConfig: map[string]interface{}{
				"server_addr": 12345,
				"use_tls":     false,
			},
			expectedErr: "server_addr not found in client configuration",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			err := step.Execute(ctx, tt.clientConfig)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectedErr)
		})
	}
}

func TestCheckDiskSpaceStep_Execute_ConnectionFailure(t *testing.T) {
	step := &CheckDiskSpaceStep{
		Path:           "/var/log",
		MinAvailableMB: 1024,
	}

	clientConfig := map[string]interface{}{
		"server_addr": "nonexistent-server:50055",
		"use_tls":     false,
	}

	ctx := context.Background()
	err := step.Execute(ctx, clientConfig)
	assert.Error(t, err)
	// The error comes from gNMI query failure after connection is established
	assert.Contains(t, err.Error(), "failed to query disk space")
}

func TestCheckDiskSpaceStep_Execute_TLSNotImplemented(t *testing.T) {
	step := &CheckDiskSpaceStep{
		Path:           "/var/log",
		MinAvailableMB: 1024,
	}

	clientConfig := map[string]interface{}{
		"server_addr": "localhost:50055",
		"use_tls":     true,
	}

	ctx := context.Background()
	err := step.Execute(ctx, clientConfig)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "TLS not yet implemented for disk space checks")
}

func TestCheckDiskSpaceStep_TypeConversions(t *testing.T) {
	// Test various numeric type conversions that can come from YAML/JSON parsing
	tests := []struct {
		name          string
		inputValue    interface{}
		expectedValue int64
	}{
		{"int conversion", 1024, 1024},
		{"int64 conversion", int64(2048), 2048},
		{"float64 conversion", 512.0, 512},
		{"float64 with decimal (truncated)", 1023.9, 1023},
		{"large int", 1000000, 1000000},
		{"large int64", int64(2000000), 2000000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := map[string]interface{}{
				"path":             "/test",
				"min_available_mb": tt.inputValue,
			}

			step, err := NewCheckDiskSpaceStep("test", params)
			require.NoError(t, err)

			diskStep := step.(*CheckDiskSpaceStep)
			assert.Equal(t, tt.expectedValue, diskStep.MinAvailableMB)
		})
	}
}

func TestCheckDiskSpaceStep_EdgeCasePaths(t *testing.T) {
	// Test various filesystem path formats
	tests := []struct {
		name string
		path string
	}{
		{"root path", "/"},
		{"absolute path", "/var/log"},
		{"nested path", "/host/var/log/containers"},
		{"relative path", "."},
		{"current directory", "./"},
		{"parent reference", "../"},
		{"path with spaces", "/path with spaces"},
		{"path with special chars", "/path-with_special.chars"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := map[string]interface{}{
				"path":             tt.path,
				"min_available_mb": 100,
			}

			step, err := NewCheckDiskSpaceStep("test", params)
			require.NoError(t, err)

			diskStep := step.(*CheckDiskSpaceStep)
			assert.Equal(t, tt.path, diskStep.Path)
			assert.NoError(t, diskStep.Validate())
		})
	}
}

func TestCheckDiskSpaceStep_ParameterDocumentation(t *testing.T) {
	// This test serves as living documentation for the expected parameters
	t.Run("documented_api_example", func(t *testing.T) {
		// Example usage that would be documented in API docs
		params := map[string]interface{}{
			"path":             "/host/var/log", // Filesystem path to check
			"min_available_mb": int64(2048),     // Minimum required space in MB
		}

		step, err := NewCheckDiskSpaceStep("pre-upgrade-disk-check", params)
		require.NoError(t, err)
		require.NotNil(t, step)

		// Verify the step implements the expected interface
		assert.Equal(t, "pre-upgrade-disk-check", step.GetName())
		assert.Equal(t, "check-disk-space", step.GetType())

		// Verify parameter extraction worked correctly
		diskStep := step.(*CheckDiskSpaceStep)
		assert.Equal(t, "/host/var/log", diskStep.Path)
		assert.Equal(t, int64(2048), diskStep.MinAvailableMB)
	})
}
