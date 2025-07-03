// Package firmware provides utilities for managing SONiC firmware files including
// cleanup operations, version detection, and image consolidation.
//
// This package handles various firmware file formats including:
//   - ONIE images (.bin files with embedded version information)
//   - Aboot images (.swi files containing ZIP archives with .imagehash)
//   - RPM packages (.rpm files)
//
// Key capabilities:
//   - Cleanup old firmware files with space reclamation reporting
//   - Extract version information from firmware images
//   - Consolidate installed images using sonic-installer
//   - Search for firmware images across multiple directories
package firmware

import (
	"os"
	"path/filepath"

	"github.com/golang/glog"

	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/config"
	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/paths"
)

// CleanupResult contains the results of a firmware cleanup operation.
type CleanupResult struct {
	FilesDeleted    int32    // Number of files successfully deleted
	DeletedFiles    []string // List of file paths that were deleted
	Errors          []string // List of error messages encountered during cleanup
	SpaceFreedBytes int64    // Total bytes of disk space reclaimed
}

// CleanupConfig specifies directories and file patterns for firmware cleanup operations.
type CleanupConfig struct {
	Directories []string // List of directory paths to search for firmware files
	Extensions  []string // File patterns to match (e.g., "*.bin", "*.swi", "*.rpm")
}

// DefaultCleanupConfig returns a cleanup configuration with standard firmware directories
// and file extensions used by SONiC systems.
func DefaultCleanupConfig() *CleanupConfig {
	return &CleanupConfig{
		Directories: []string{"/host", "/tmp"},
		Extensions:  []string{"*.bin", "*.swi", "*.rpm"},
	}
}

// CleanupOldFirmwareInDirectories cleans up firmware files in the specified directories.
// It searches for files matching the given extensions (glob patterns) and removes them,
// tracking the number of files deleted, total space reclaimed, and any errors encountered.
//
// Parameters:
//   - directoryPaths: List of absolute directory paths to search
//   - extensions: List of file patterns to match (e.g., "*.bin", "*.swi", "*.rpm")
//
// Returns a CleanupResult with detailed information about the cleanup operation.
func CleanupOldFirmwareInDirectories(directoryPaths []string, extensions []string) *CleanupResult {
	result := &CleanupResult{
		DeletedFiles: make([]string, 0),
		Errors:       make([]string, 0),
	}

	for _, dirPath := range directoryPaths {
		glog.V(1).Infof("Cleaning up firmware files in %s", dirPath)

		for _, pattern := range extensions {
			matches, err := filepath.Glob(filepath.Join(dirPath, pattern))
			if err != nil {
				glog.Errorf("Failed to glob pattern %s in %s: %v", pattern, dirPath, err)
				result.Errors = append(result.Errors, err.Error())
				continue
			}

			for _, file := range matches {
				if err := deleteFile(file, result); err != nil {
					glog.Errorf("Failed to delete %s: %v", file, err)
					result.Errors = append(result.Errors, err.Error())
				}
			}
		}
	}

	glog.V(1).Infof("Cleanup completed: %d files deleted, %d errors, %d bytes freed",
		result.FilesDeleted, len(result.Errors), result.SpaceFreedBytes)
	return result
}

// CleanupOldFirmware is deprecated. Use CleanupOldFirmwareInDirectories instead.
// This function uses the default cleanup configuration and global rootFS path resolution.
func CleanupOldFirmware() *CleanupResult {
	return CleanupOldFirmwareWithConfig(DefaultCleanupConfig())
}

// CleanupOldFirmwareWithConfig performs firmware cleanup using the specified configuration.
// Unlike CleanupOldFirmwareInDirectories, this function uses the global config to resolve
// container-relative paths (e.g., "/host" becomes "/mnt/host" in container deployments).
//
// This function is useful when working in containerized environments where the filesystem
// is mounted at a different location than the container's root.
func CleanupOldFirmwareWithConfig(cfg *CleanupConfig) *CleanupResult {
	result := &CleanupResult{
		DeletedFiles: make([]string, 0),
		Errors:       make([]string, 0),
	}

	for _, dir := range cfg.Directories {
		dirPath := paths.ToHost(dir, config.Global.RootFS)
		glog.V(1).Infof("Cleaning up firmware files in %s", dirPath)

		for _, pattern := range cfg.Extensions {
			matches, err := filepath.Glob(filepath.Join(dirPath, pattern))
			if err != nil {
				glog.Errorf("Failed to glob pattern %s in %s: %v", pattern, dirPath, err)
				result.Errors = append(result.Errors, err.Error())
				continue
			}

			for _, file := range matches {
				if err := deleteFile(file, result); err != nil {
					glog.Errorf("Failed to delete %s: %v", file, err)
					result.Errors = append(result.Errors, err.Error())
				}
			}
		}
	}

	glog.V(1).Infof("Cleanup completed: %d files deleted, %d errors, %d bytes freed",
		result.FilesDeleted, len(result.Errors), result.SpaceFreedBytes)
	return result
}

// deleteFile removes a single file and updates the cleanup result with size and path information.
// It safely retrieves the file size before deletion to accurately track space reclamation.
func deleteFile(filePath string, result *CleanupResult) error {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return err
	}

	size := fileInfo.Size()
	if err := os.Remove(filePath); err != nil {
		return err
	}

	result.FilesDeleted++
	result.DeletedFiles = append(result.DeletedFiles, filePath)
	result.SpaceFreedBytes += size
	glog.V(2).Infof("Deleted firmware file: %s (%d bytes)", filePath, size)
	return nil
}
