package diskspace

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/golang/glog"
)

// GetDiskTotalSpaceMB returns the total disk space in MB for the given path.
func GetDiskTotalSpaceMB(path string) (int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, fmt.Errorf("failed to get filesystem stats for %s: %w", path, err)
	}

	// Calculate total space in MB
	blockSize := int64(stat.Bsize)
	totalBytes := int64(stat.Blocks) * blockSize
	totalMB := totalBytes / (1024 * 1024)

	glog.V(2).Infof("Total space for %s: %d MB", path, totalMB)
	return totalMB, nil
}

// GetDiskFreeSpaceMB returns the available disk space in MB for the given path.
func GetDiskFreeSpaceMB(path string) (int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, fmt.Errorf("failed to get filesystem stats for %s: %w", path, err)
	}

	// Calculate available space in MB (using Bavail which is available to non-root users)
	blockSize := int64(stat.Bsize)
	availBytes := int64(stat.Bavail) * blockSize
	availMB := availBytes / (1024 * 1024)

	glog.V(2).Infof("Available space for %s: %d MB", path, availMB)
	return availMB, nil
}

// CleanupResult represents the result of a cleanup operation.
type CleanupResult struct {
	Path         string
	FilesRemoved int
	SpaceFreedMB int64
	Errors       []error
}

// CleanupOptions configures cleanup behavior.
type CleanupOptions struct {
	DryRun bool // If true, only report what would be cleaned without actually deleting
}

// CleanupCoreFiles removes core dump files from /var/core/*.
func CleanupCoreFiles(opts CleanupOptions) (*CleanupResult, error) {
	return cleanupDirectory("/var/core", opts)
}

// CleanupDumpFiles removes dump files from /var/dump/*.
func CleanupDumpFiles(opts CleanupOptions) (*CleanupResult, error) {
	return cleanupDirectory("/var/dump", opts)
}

// cleanupDirectory removes all files and subdirectories from the specified directory.
func cleanupDirectory(dirPath string, opts CleanupOptions) (*CleanupResult, error) {
	result := &CleanupResult{
		Path:   dirPath,
		Errors: make([]error, 0),
	}

	// Check if directory exists
	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		glog.V(2).Infof("Directory %s does not exist, skipping cleanup", dirPath)
		return result, nil
	}

	// Get all items in the directory
	pattern := filepath.Join(dirPath, "*")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return result, fmt.Errorf("failed to list files in %s: %w", dirPath, err)
	}

	glog.V(2).Infof("Found %d items to clean in %s (dry-run: %v)", len(matches), dirPath, opts.DryRun)

	// Calculate space and remove files
	for _, match := range matches {
		size, err := calculateDirectorySize(match)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("failed to calculate size of %s: %w", match, err))
			continue
		}

		result.SpaceFreedMB += size
		result.FilesRemoved++

		if !opts.DryRun {
			if err := os.RemoveAll(match); err != nil {
				result.Errors = append(result.Errors, fmt.Errorf("failed to remove %s: %w", match, err))
				continue
			}
			glog.V(2).Infof("Removed %s (%d MB)", match, size)
		} else {
			glog.V(2).Infof("Would remove %s (%d MB)", match, size)
		}
	}

	if opts.DryRun {
		glog.Infof("Dry run: would free %d MB by removing %d items from %s",
			result.SpaceFreedMB, result.FilesRemoved, dirPath)
	} else {
		glog.Infof("Freed %d MB by removing %d items from %s",
			result.SpaceFreedMB, result.FilesRemoved, dirPath)
	}

	return result, nil
}

// calculateDirectorySize calculates the total size of a file or directory in MB.
func calculateDirectorySize(path string) (int64, error) {
	var totalSize int64

	err := filepath.Walk(path, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		totalSize += info.Size()
		return nil
	})

	if err != nil {
		return 0, err
	}

	// Convert to MB, rounding up for non-zero sizes
	totalMB := totalSize / (1024 * 1024)
	if totalSize > 0 && totalMB == 0 {
		totalMB = 1 // Round up to 1 MB for any non-zero size
	}

	return totalMB, nil
}

// GetCleanupSpaceMB estimates how much space would be freed by cleanup operations.
func GetCleanupSpaceMB() (int64, error) {
	var totalSpace int64

	// Check core files
	coreResult, err := CleanupCoreFiles(CleanupOptions{DryRun: true})
	if err != nil {
		glog.V(1).Infof("Failed to estimate core cleanup space: %v", err)
	} else {
		totalSpace += coreResult.SpaceFreedMB
	}

	// Check dump files
	dumpResult, err := CleanupDumpFiles(CleanupOptions{DryRun: true})
	if err != nil {
		glog.V(1).Infof("Failed to estimate dump cleanup space: %v", err)
	} else {
		totalSpace += dumpResult.SpaceFreedMB
	}

	glog.V(2).Infof("Total estimated cleanup space: %d MB", totalSpace)
	return totalSpace, nil
}
