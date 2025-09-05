package gnmi

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/openconfig/gnmi/proto/gnmi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewServer(t *testing.T) {
	server := NewServer("/mnt/host")
	assert.NotNil(t, server)
	assert.Equal(t, "/mnt/host", server.rootFS)
}

func TestCapabilities(t *testing.T) {
	server := NewServer("/")
	ctx := context.Background()
	req := &gnmi.CapabilityRequest{}

	resp, err := server.Capabilities(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	// Check supported models - should be empty without proper YANG schema
	assert.Empty(t, resp.SupportedModels, "No YANG models should be registered without proper schema definitions")

	// Check supported encodings
	assert.Contains(t, resp.SupportedEncodings, gnmi.Encoding_JSON)
	assert.Contains(t, resp.SupportedEncodings, gnmi.Encoding_JSON_IETF)

	// Check gNMI version
	assert.Equal(t, "0.7.0", resp.GNMIVersion)
}

func TestPathToString(t *testing.T) {
	tests := []struct {
		name     string
		path     *gnmi.Path
		expected string
	}{
		{
			name:     "nil path",
			path:     nil,
			expected: "/",
		},
		{
			name: "simple path",
			path: &gnmi.Path{
				Elem: []*gnmi.PathElem{
					{Name: "sonic"},
					{Name: "system"},
				},
			},
			expected: "/sonic/system",
		},
		{
			name: "path with keys",
			path: &gnmi.Path{
				Elem: []*gnmi.PathElem{
					{Name: "sonic"},
					{Name: "system"},
					{
						Name: "filesystem",
						Key:  map[string]string{"path": "/host"},
					},
					{Name: "disk-space"},
				},
			},
			expected: "/sonic/system/filesystem[path=/host]/disk-space",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := pathToString(tt.path)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsFilesystemPath(t *testing.T) {
	tests := []struct {
		name     string
		path     *gnmi.Path
		expected bool
	}{
		{
			name: "valid filesystem path",
			path: &gnmi.Path{
				Elem: []*gnmi.PathElem{
					{Name: "sonic"},
					{Name: "system"},
					{Name: "filesystem"},
				},
			},
			expected: true,
		},
		{
			name: "non-filesystem path",
			path: &gnmi.Path{
				Elem: []*gnmi.PathElem{
					{Name: "sonic"},
					{Name: "system"},
					{Name: "network"},
				},
			},
			expected: false,
		},
		{
			name: "short path",
			path: &gnmi.Path{
				Elem: []*gnmi.PathElem{
					{Name: "sonic"},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isFilesystemPath(tt.path)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractFilesystemPath(t *testing.T) {
	tests := []struct {
		name        string
		path        *gnmi.Path
		expected    string
		expectError bool
	}{
		{
			name: "valid path with key",
			path: &gnmi.Path{
				Elem: []*gnmi.PathElem{
					{Name: "sonic"},
					{Name: "system"},
					{
						Name: "filesystem",
						Key:  map[string]string{"path": "/host"},
					},
				},
			},
			expected:    "/host",
			expectError: false,
		},
		{
			name: "path without key",
			path: &gnmi.Path{
				Elem: []*gnmi.PathElem{
					{Name: "sonic"},
					{Name: "system"},
					{Name: "filesystem"},
				},
			},
			expected:    "",
			expectError: true,
		},
		{
			name: "non-filesystem path",
			path: &gnmi.Path{
				Elem: []*gnmi.PathElem{
					{Name: "sonic"},
					{Name: "system"},
					{Name: "network"},
				},
			},
			expected:    "",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := extractFilesystemPath(tt.path)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestResolveFilesystemPath(t *testing.T) {
	tests := []struct {
		name     string
		rootFS   string
		fsPath   string
		expected string
	}{
		{
			name:     "bare metal deployment",
			rootFS:   "/",
			fsPath:   "/host",
			expected: "/host",
		},
		{
			name:     "container deployment",
			rootFS:   "/mnt/host",
			fsPath:   "/host",
			expected: "/mnt/host/host",
		},
		{
			name:     "empty rootFS",
			rootFS:   "",
			fsPath:   "/host",
			expected: "/host",
		},
		{
			name:     "relative path",
			rootFS:   "/mnt/host",
			fsPath:   "host",
			expected: "host",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := NewServer(tt.rootFS)
			result := server.resolveFilesystemPath(tt.fsPath)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGet_InvalidRequests(t *testing.T) {
	server := NewServer("/")
	ctx := context.Background()

	// Test empty paths
	req := &gnmi.GetRequest{Path: []*gnmi.Path{}}
	_, err := server.Get(ctx, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no paths specified")

	// Test nil path
	req = &gnmi.GetRequest{Path: []*gnmi.Path{nil}}
	_, err = server.Get(ctx, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nil path")

	// Test unsupported path
	req = &gnmi.GetRequest{
		Path: []*gnmi.Path{
			{
				Elem: []*gnmi.PathElem{
					{Name: "unsupported"},
					{Name: "path"},
				},
			},
		},
	}
	_, err = server.Get(ctx, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "path not found")
}

func TestGet_DiskSpaceSuccess(t *testing.T) {
	server := NewServer("/")
	ctx := context.Background()

	// Test request for current directory disk space
	req := &gnmi.GetRequest{
		Path: []*gnmi.Path{
			{
				Elem: []*gnmi.PathElem{
					{Name: "sonic"},
					{Name: "system"},
					{
						Name: "filesystem",
						Key:  map[string]string{"path": "."},
					},
					{Name: "disk-space"},
				},
			},
		},
	}

	resp, err := server.Get(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Len(t, resp.Notification, 1)
	require.Len(t, resp.Notification[0].Update, 1)

	// Check the response structure
	update := resp.Notification[0].Update[0]
	assert.NotNil(t, update.Path)
	assert.NotNil(t, update.Val)
	assert.NotNil(t, update.Val.GetJsonVal())

	// Parse the JSON response
	var result map[string]interface{}
	err = json.Unmarshal(update.Val.GetJsonVal(), &result)
	require.NoError(t, err)

	// Verify the response contains expected fields
	assert.Contains(t, result, "path")
	assert.Contains(t, result, "total-mb")
	assert.Contains(t, result, "available-mb")
	assert.Equal(t, ".", result["path"])

	// Verify the values are reasonable
	totalMB, ok := result["total-mb"].(float64)
	assert.True(t, ok)
	assert.Greater(t, totalMB, float64(0))

	availableMB, ok := result["available-mb"].(float64)
	assert.True(t, ok)
	assert.GreaterOrEqual(t, availableMB, float64(0))
	assert.LessOrEqual(t, availableMB, totalMB)

	t.Logf("Disk space response: %v", result)
}

func TestSet_Unimplemented(t *testing.T) {
	server := NewServer("/")
	ctx := context.Background()
	req := &gnmi.SetRequest{}

	_, err := server.Set(ctx, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not implemented")
}

func TestSubscribe_Unimplemented(t *testing.T) {
	server := NewServer("/")

	// We can't easily test the streaming interface, but we can test
	// that the method exists and returns the expected error
	err := server.Subscribe(nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not implemented")
}
