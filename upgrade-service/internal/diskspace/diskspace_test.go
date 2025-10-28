package diskspace

import (
	"os"
	"path/filepath"
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

func TestCleanupCoreFiles(t *testing.T) {
	// Create temporary test directory structure
	tmpDir := t.TempDir()
	testCoreDir := filepath.Join(tmpDir, "var", "core")
	err := os.MkdirAll(testCoreDir, 0755)
	require.NoError(t, err)

	// Create test files
	testFile1 := filepath.Join(testCoreDir, "core.1234")
	testFile2 := filepath.Join(testCoreDir, "core.5678")
	testSubDir := filepath.Join(testCoreDir, "subdir")

	err = os.WriteFile(testFile1, []byte("test core file 1"), 0644)
	require.NoError(t, err)

	err = os.WriteFile(testFile2, []byte("test core file 2"), 0644)
	require.NoError(t, err)

	err = os.MkdirAll(testSubDir, 0755)
	require.NoError(t, err)

	testFile3 := filepath.Join(testSubDir, "nested.core")
	err = os.WriteFile(testFile3, []byte("nested core file"), 0644)
	require.NoError(t, err)

	// Test dry run
	result, err := cleanupDirectory(testCoreDir, CleanupOptions{DryRun: true})
	require.NoError(t, err)
	assert.Equal(t, testCoreDir, result.Path)
	assert.Equal(t, 3, result.FilesRemoved) // 2 files + 1 subdir
	assert.Greater(t, result.SpaceFreedMB, int64(0))
	assert.Empty(t, result.Errors)

	// Files should still exist after dry run
	assert.FileExists(t, testFile1)
	assert.FileExists(t, testFile2)
	assert.FileExists(t, testFile3)

	// Test actual cleanup
	result, err = cleanupDirectory(testCoreDir, CleanupOptions{DryRun: false})
	require.NoError(t, err)
	assert.Equal(t, 3, result.FilesRemoved)
	assert.Greater(t, result.SpaceFreedMB, int64(0))
	assert.Empty(t, result.Errors)

	// Files should be removed after cleanup
	assert.NoFileExists(t, testFile1)
	assert.NoFileExists(t, testFile2)
	assert.NoFileExists(t, testFile3)
	assert.NoDirExists(t, testSubDir)
}

func TestCleanupDumpFiles(t *testing.T) {
	// Create temporary test directory structure
	tmpDir := t.TempDir()
	testDumpDir := filepath.Join(tmpDir, "var", "dump")
	err := os.MkdirAll(testDumpDir, 0755)
	require.NoError(t, err)

	// Create test files
	testFile1 := filepath.Join(testDumpDir, "dump1.log")
	testFile2 := filepath.Join(testDumpDir, "dump2.log")

	err = os.WriteFile(testFile1, []byte("test dump file 1"), 0644)
	require.NoError(t, err)

	err = os.WriteFile(testFile2, []byte("test dump file 2"), 0644)
	require.NoError(t, err)

	// Test dry run
	result, err := cleanupDirectory(testDumpDir, CleanupOptions{DryRun: true})
	require.NoError(t, err)
	assert.Equal(t, testDumpDir, result.Path)
	assert.Equal(t, 2, result.FilesRemoved)
	assert.Greater(t, result.SpaceFreedMB, int64(0))
	assert.Empty(t, result.Errors)

	// Files should still exist after dry run
	assert.FileExists(t, testFile1)
	assert.FileExists(t, testFile2)

	// Test actual cleanup
	result, err = cleanupDirectory(testDumpDir, CleanupOptions{DryRun: false})
	require.NoError(t, err)
	assert.Equal(t, 2, result.FilesRemoved)
	assert.Empty(t, result.Errors)

	// Files should be removed after cleanup
	assert.NoFileExists(t, testFile1)
	assert.NoFileExists(t, testFile2)
}

func TestCleanupNonExistentDirectory(t *testing.T) {
	// Test cleanup on non-existent directory
	result, err := cleanupDirectory("/non/existent/path", CleanupOptions{DryRun: true})
	require.NoError(t, err)
	assert.Equal(t, "/non/existent/path", result.Path)
	assert.Equal(t, 0, result.FilesRemoved)
	assert.Equal(t, int64(0), result.SpaceFreedMB)
	assert.Empty(t, result.Errors)
}

func TestCleanupEmptyDirectory(t *testing.T) {
	// Create empty test directory
	tmpDir := t.TempDir()
	testDir := filepath.Join(tmpDir, "empty")
	err := os.MkdirAll(testDir, 0755)
	require.NoError(t, err)

	// Test cleanup on empty directory
	result, err := cleanupDirectory(testDir, CleanupOptions{DryRun: false})
	require.NoError(t, err)
	assert.Equal(t, testDir, result.Path)
	assert.Equal(t, 0, result.FilesRemoved)
	assert.Equal(t, int64(0), result.SpaceFreedMB)
	assert.Empty(t, result.Errors)
}

func TestCalculateDirectorySize(t *testing.T) {
	// Create test directory with files
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	testContent := make([]byte, 1024*1024) // 1 MB file

	err := os.WriteFile(testFile, testContent, 0644)
	require.NoError(t, err)

	// Test size calculation
	size, err := calculateDirectorySize(testFile)
	require.NoError(t, err)
	assert.Equal(t, int64(1), size) // Should be 1 MB

	// Test directory size calculation
	size, err = calculateDirectorySize(tmpDir)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, size, int64(1)) // Should be at least 1 MB
}

func TestGetCleanupSpaceMB(t *testing.T) {
	// This test checks the function runs without error
	// The actual space will vary depending on system state
	space, err := GetCleanupSpaceMB()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, space, int64(0))

	t.Logf("Estimated cleanup space: %d MB", space)
}
