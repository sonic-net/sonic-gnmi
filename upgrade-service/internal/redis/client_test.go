package redis

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()
	assert.Equal(t, "127.0.0.1", config.Host)
	assert.Equal(t, 6379, config.Port)
	assert.Equal(t, 4, config.DB) // CONFIG_DB
	assert.Equal(t, 5*time.Second, config.Timeout)
}

func TestNewClient(t *testing.T) {
	// Start miniredis server
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	// Parse miniredis address
	host := mr.Host()
	port, err := strconv.Atoi(mr.Port())
	require.NoError(t, err)

	// Create client with miniredis config
	config := &Config{
		Host:    host,
		Port:    port,
		DB:      4,
		Timeout: 5 * time.Second,
	}

	client, err := NewClient(config)
	require.NoError(t, err)
	require.NotNil(t, client)
	defer client.Close()

	// Test ping
	ctx := context.Background()
	err = client.Ping(ctx)
	assert.NoError(t, err)
}

func TestNewClientConnectionFailure(t *testing.T) {
	// Try to connect to a non-existent Redis server
	config := &Config{
		Host:    "127.0.0.1",
		Port:    59999, // Invalid port
		DB:      4,
		Timeout: 1 * time.Second,
	}

	client, err := NewClient(config)
	assert.Error(t, err)
	assert.Nil(t, client)
	assert.Contains(t, err.Error(), "failed to connect to Redis")
}

func TestHGet(t *testing.T) {
	// Start miniredis server
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	// Set up test data in miniredis
	mr.Select(4) // CONFIG_DB
	mr.HSet("DEVICE_METADATA|localhost", "type", "ToRRouter")
	mr.HSet("DEVICE_METADATA|localhost", "platform", "x86_64-mlnx_msn2700-r0")

	// Create client
	port, err := strconv.Atoi(mr.Port())
	require.NoError(t, err)
	config := &Config{
		Host:    mr.Host(),
		Port:    port,
		DB:      4,
		Timeout: 5 * time.Second,
	}

	client, err := NewClient(config)
	require.NoError(t, err)
	defer client.Close()

	ctx := context.Background()

	// Test successful HGet
	t.Run("successful HGet", func(t *testing.T) {
		value, err := client.HGet(ctx, "DEVICE_METADATA|localhost", "type")
		assert.NoError(t, err)
		assert.Equal(t, "ToRRouter", value)

		value, err = client.HGet(ctx, "DEVICE_METADATA|localhost", "platform")
		assert.NoError(t, err)
		assert.Equal(t, "x86_64-mlnx_msn2700-r0", value)
	})

	// Test HGet with non-existent field
	t.Run("non-existent field", func(t *testing.T) {
		value, err := client.HGet(ctx, "DEVICE_METADATA|localhost", "non_existent_field")
		assert.Error(t, err)
		assert.Empty(t, value)
		assert.Contains(t, err.Error(), "field 'non_existent_field' not found")
	})

	// Test HGet with non-existent key
	t.Run("non-existent key", func(t *testing.T) {
		value, err := client.HGet(ctx, "NON_EXISTENT_KEY", "field")
		assert.Error(t, err)
		assert.Empty(t, value)
		assert.Contains(t, err.Error(), "field 'field' not found")
	})
}

func TestClose(t *testing.T) {
	// Start miniredis server
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	// Create client
	port, err := strconv.Atoi(mr.Port())
	require.NoError(t, err)
	config := &Config{
		Host:    mr.Host(),
		Port:    port,
		DB:      4,
		Timeout: 5 * time.Second,
	}

	client, err := NewClient(config)
	require.NoError(t, err)

	// Close should not error
	err = client.Close()
	assert.NoError(t, err)

	// Close on nil client should not panic
	var nilClient *Client
	err = nilClient.Close()
	assert.NoError(t, err)
}

func TestNewClientWithNilConfig(t *testing.T) {
	// When miniredis is not available, this will fail
	// but it tests that nil config uses defaults
	client, err := NewClient(nil)
	if err == nil {
		client.Close()
	}
	// We expect an error since default config points to localhost:6379
	assert.Error(t, err)
}
