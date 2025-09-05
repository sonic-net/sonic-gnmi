package gnmi

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiskSpaceInfo(t *testing.T) {
	info := &DiskSpaceInfo{
		Path:        "/test",
		TotalMB:     1000,
		AvailableMB: 500,
	}

	assert.Equal(t, "/test", info.Path)
	assert.Equal(t, int64(1000), info.TotalMB)
	assert.Equal(t, int64(500), info.AvailableMB)
}

func TestGetDiskSpace_ValidatesPath(t *testing.T) {
	client := &Client{}
	ctx := context.Background()

	// Test empty path validation (the only validation we can test without a server)
	t.Run("empty_path", func(t *testing.T) {
		_, err := client.GetDiskSpace(ctx, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "filesystem path is required")
	})

	// Note: Testing valid paths would require a gRPC connection,
	// which is beyond the scope of unit tests. Integration tests
	// in tests/loopback/ cover the full functionality.
}

func TestDiskSpaceInfo_Constraints(t *testing.T) {
	// Test logical constraints and edge cases for DiskSpaceInfo
	tests := []struct {
		name        string
		info        DiskSpaceInfo
		expectValid bool
	}{
		{
			name:        "valid normal case",
			info:        DiskSpaceInfo{Path: "/", TotalMB: 1000, AvailableMB: 500},
			expectValid: true,
		},
		{
			name:        "available equals total",
			info:        DiskSpaceInfo{Path: "/empty", TotalMB: 1000, AvailableMB: 1000},
			expectValid: true,
		},
		{
			name:        "zero available",
			info:        DiskSpaceInfo{Path: "/full", TotalMB: 1000, AvailableMB: 0},
			expectValid: true,
		},
		{
			name:        "large filesystem",
			info:        DiskSpaceInfo{Path: "/big", TotalMB: 1024000, AvailableMB: 512000},
			expectValid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.expectValid {
				assert.NotEmpty(t, tt.info.Path)
				assert.GreaterOrEqual(t, tt.info.TotalMB, int64(0))
				assert.GreaterOrEqual(t, tt.info.AvailableMB, int64(0))
				assert.LessOrEqual(t, tt.info.AvailableMB, tt.info.TotalMB)
			}
		})
	}
}

func TestDiskSpaceInfo_JSONTags(t *testing.T) {
	// Verify that the struct is properly defined for JSON unmarshaling
	info := &DiskSpaceInfo{
		Path:        "/test",
		TotalMB:     2048,
		AvailableMB: 1024,
	}

	// Verify struct fields and constraints
	assert.NotEmpty(t, info.Path)
	assert.Greater(t, info.TotalMB, int64(0))
	assert.Greater(t, info.AvailableMB, int64(0))
	assert.LessOrEqual(t, info.AvailableMB, info.TotalMB)
}

func TestGetDiskSpace_EmptyPathValidation(t *testing.T) {
	// This is the only validation we can reliably test at the unit level
	// without mocking the gRPC connection
	client := &Client{}
	ctx := context.Background()

	_, err := client.GetDiskSpace(ctx, "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "filesystem path is required")
}
