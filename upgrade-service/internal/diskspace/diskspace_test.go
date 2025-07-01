package diskspace

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetDiskTotalSpaceMB(t *testing.T) {
	// Test with root filesystem
	total, err := GetDiskTotalSpaceMB("/")
	require.NoError(t, err)
	assert.Greater(t, total, int64(0))

	t.Logf("Total space for /: %d MB", total)
}

func TestGetDiskFreeSpaceMB(t *testing.T) {
	// Test with root filesystem
	free, err := GetDiskFreeSpaceMB("/")
	require.NoError(t, err)
	assert.Greater(t, free, int64(0))

	t.Logf("Free space for /: %d MB", free)
}

func TestGetDiskSpaceMBInvalidPath(t *testing.T) {
	// Test with non-existent path
	_, err := GetDiskTotalSpaceMB("/non/existent/path")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get filesystem stats")

	_, err = GetDiskFreeSpaceMB("/non/existent/path")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get filesystem stats")
}

func TestDiskSpaceConsistency(t *testing.T) {
	// Total should always be >= free
	total, err := GetDiskTotalSpaceMB("/")
	require.NoError(t, err)

	free, err := GetDiskFreeSpaceMB("/")
	require.NoError(t, err)

	assert.GreaterOrEqual(t, total, free)
	t.Logf("Disk usage for /: %d MB used of %d MB total", total-free, total)
}
