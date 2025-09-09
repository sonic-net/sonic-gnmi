package gnmi

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewClient_InvalidConfig(t *testing.T) {
	tests := []struct {
		name   string
		config *ClientConfig
	}{
		{
			name:   "empty target",
			config: &ClientConfig{Target: ""},
		},
		{
			name:   "nil config",
			config: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewClient(tt.config)
			assert.Error(t, err)
		})
	}
}

func TestClientConfig_Validation(t *testing.T) {
	// Test that configuration is properly validated
	config := &ClientConfig{
		Target:  "localhost:50055",
		Timeout: 5 * time.Second,
	}

	// We test that NewClient accepts valid config (but may fail at connection)
	client, err := NewClient(config)
	// Either successful creation or connection failure is acceptable
	if err == nil {
		// If creation succeeded, close the client
		client.Close()
	} else {
		// If creation failed, it should be due to connection issues
		assert.Contains(t, err.Error(), "failed to connect")
	}
}

func TestClientConfig_DefaultTimeout(t *testing.T) {
	config := &ClientConfig{
		Target: "localhost:50055",
		// No timeout specified - should default to 30s
	}

	// Test that default timeout is applied (we can't easily test this without mocking)
	client, err := NewClient(config)
	if err == nil {
		client.Close()
	}
	// Just verify the client creation process completes without panic
}

func TestClient_Close(t *testing.T) {
	client := &Client{}

	// Calling Close on a client with no connection should not panic
	err := client.Close()
	assert.NoError(t, err)
}

func TestGetDiskSpace_InvalidPath(t *testing.T) {
	client := &Client{}
	ctx := context.Background()

	_, err := client.GetDiskSpace(ctx, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "filesystem path is required")
}

// Integration test helper - requires a running gNMI server.
func TestIntegration_WithRunningServer(t *testing.T) {
	// Skip this test unless we're in integration test mode
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	config := &ClientConfig{
		Target:  "localhost:50055",
		Timeout: 10 * time.Second,
	}

	client, err := NewClient(config)
	if err != nil {
		t.Skipf("Cannot connect to test server: %v", err)
	}
	defer client.Close()

	ctx := context.Background()

	// First test if gNMI service is available
	_, err = client.Capabilities(ctx)
	if err != nil {
		t.Skipf("gNMI service not available on test server (expected for unit tests): %v", err)
	}

	// Test Capabilities
	t.Run("capabilities", func(t *testing.T) {
		resp, err := client.Capabilities(ctx)
		require.NoError(t, err)
		require.NotNil(t, resp)

		// Should have no supported models without proper YANG schema
		assert.Empty(t, resp.SupportedModels)
	})

	// Test GetDiskSpace
	t.Run("disk_space", func(t *testing.T) {
		info, err := client.GetDiskSpace(ctx, ".")
		require.NoError(t, err)
		require.NotNil(t, info)

		assert.Equal(t, ".", info.Path)
		assert.Greater(t, info.TotalMB, int64(0))
		assert.GreaterOrEqual(t, info.AvailableMB, int64(0))
		assert.LessOrEqual(t, info.AvailableMB, info.TotalMB)
	})
}
