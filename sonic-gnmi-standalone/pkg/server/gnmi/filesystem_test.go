package gnmi

import (
	"testing"

	"github.com/openconfig/gnmi/proto/gnmi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGetSupportedPaths(t *testing.T) {
	// Test the unused function - documents expected paths
	paths := getSupportedPaths()
	expected := []string{"/sonic/system/filesystem[path=*]/disk-space"}
	assert.Equal(t, expected, paths)
	assert.Len(t, paths, 1, "Should return exactly one supported path")
}

func TestHandleFilesystemPath_UnsupportedMetric(t *testing.T) {
	// Test the missing coverage case - non-disk-space filesystem requests
	server := NewServer("/")

	// Create a filesystem path that's not disk-space (e.g., cpu-usage)
	path := &gnmi.Path{
		Elem: []*gnmi.PathElem{
			{Name: "sonic"},
			{Name: "system"},
			{
				Name: "filesystem",
				Key:  map[string]string{"path": "/"},
			},
			{Name: "cpu-usage"}, // Not supported
		},
	}

	update, err := server.handleFilesystemPath(path)
	assert.Error(t, err)
	assert.Nil(t, update)

	// Check it's the right error code and message
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
	assert.Contains(t, st.Message(), "unsupported filesystem metric")
}

func TestHandleFilesystemPath_InvalidExtractionPath(t *testing.T) {
	server := NewServer("/")

	// Test path that looks like filesystem but fails extraction
	path := &gnmi.Path{
		Elem: []*gnmi.PathElem{
			{Name: "sonic"},
			{Name: "system"},
			{
				Name: "filesystem",
				// Missing "path" key - should fail extraction
			},
			{Name: "disk-space"},
		},
	}

	update, err := server.handleFilesystemPath(path)
	assert.Error(t, err)
	assert.Nil(t, update)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "invalid filesystem path")
}

func TestValidateDiskSpacePath_EdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		path        *gnmi.Path
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid 4-element path",
			path: &gnmi.Path{
				Elem: []*gnmi.PathElem{
					{Name: "sonic"},
					{Name: "system"},
					{Name: "filesystem", Key: map[string]string{"path": "/"}},
					{Name: "disk-space"},
				},
			},
			expectError: false,
		},
		{
			name: "too many elements (5)",
			path: &gnmi.Path{
				Elem: []*gnmi.PathElem{
					{Name: "sonic"},
					{Name: "system"},
					{Name: "filesystem", Key: map[string]string{"path": "/"}},
					{Name: "disk-space"},
					{Name: "extra"}, // Too many elements
				},
			},
			expectError: true,
			errorMsg:    "invalid disk space path",
		},
		{
			name: "too few elements (3)",
			path: &gnmi.Path{
				Elem: []*gnmi.PathElem{
					{Name: "sonic"},
					{Name: "system"},
					{Name: "filesystem", Key: map[string]string{"path": "/"}},
					// Missing disk-space element
				},
			},
			expectError: true,
			errorMsg:    "not a disk space path",
		},
		{
			name: "wrong final element",
			path: &gnmi.Path{
				Elem: []*gnmi.PathElem{
					{Name: "sonic"},
					{Name: "system"},
					{Name: "filesystem", Key: map[string]string{"path": "/"}},
					{Name: "not-disk-space"}, // Wrong element
				},
			},
			expectError: true,
			errorMsg:    "not a disk space path",
		},
		{
			name: "completely wrong path",
			path: &gnmi.Path{
				Elem: []*gnmi.PathElem{
					{Name: "wrong"},
					{Name: "path"},
				},
			},
			expectError: true,
			errorMsg:    "not a disk space path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDiskSpacePath(tt.path)
			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestResolveFilesystemPath_EdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		rootFS      string
		fsPath      string
		expected    string
		description string
	}{
		{
			name:        "path already contains rootFS",
			rootFS:      "/mnt/host",
			fsPath:      "/mnt/host/var/log",
			expected:    "/mnt/host/var/log",
			description: "Should use path as-is when already prefixed with rootFS",
		},
		{
			name:        "empty rootFS with absolute path",
			rootFS:      "",
			fsPath:      "/var/log",
			expected:    "/var/log",
			description: "Empty rootFS should use path as-is",
		},
		{
			name:        "relative path with rootFS",
			rootFS:      "/mnt/host",
			fsPath:      "relative/path",
			expected:    "relative/path",
			description: "Relative paths should be used as-is",
		},
		{
			name:        "root rootFS with absolute path",
			rootFS:      "/",
			fsPath:      "/var/log",
			expected:    "/var/log",
			description: "Root rootFS should use path as-is",
		},
		{
			name:        "complex rootFS with nested path",
			rootFS:      "/containers/sonic",
			fsPath:      "/host/var/log",
			expected:    "/containers/sonic/host/var/log",
			description: "Should join complex rootFS with absolute path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := NewServer(tt.rootFS)
			result := server.resolveFilesystemPath(tt.fsPath)
			assert.Equal(t, tt.expected, result, tt.description)
		})
	}
}

func TestHandleDiskSpaceRequest_InvalidPath(t *testing.T) {
	server := NewServer("/")

	// Test with invalid validation path
	invalidPath := &gnmi.Path{
		Elem: []*gnmi.PathElem{
			{Name: "sonic"},
			{Name: "system"},
			{Name: "filesystem", Key: map[string]string{"path": "/"}},
			{Name: "disk-space"},
			{Name: "extra"}, // Too many elements - will fail validation
		},
	}

	update, err := server.handleDiskSpaceRequest(invalidPath, "/")
	assert.Error(t, err)
	assert.Nil(t, update)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "invalid disk space path")
}
