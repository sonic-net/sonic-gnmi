// Package diskspace provides utilities for retrieving filesystem disk space information.
package diskspace

import (
	"fmt"
	"syscall"
)

// DiskSpaceInfo contains disk space metrics for a filesystem.
type DiskSpaceInfo struct {
	TotalMB     int64 `json:"total-mb"`
	AvailableMB int64 `json:"available-mb"`
}

// GetDiskSpace retrieves disk space information for the given filesystem path.
// It uses syscall.Statfs to efficiently query filesystem statistics.
//
// The path parameter should be any valid path on the filesystem to query.
// Common examples: "/", "/tmp", "/var", "/host"
//
// This function returns:
// - TotalMB: Total space in megabytes (1024*1024 bytes)
// - AvailableMB: Available space in megabytes for non-privileged users
//
// Note: AvailableMB uses Bavail (available to non-superuser) rather than
// Bfree (free blocks) to match the behavior of df command.
func GetDiskSpace(path string) (*DiskSpaceInfo, error) {
	var stat syscall.Statfs_t

	if err := syscall.Statfs(path, &stat); err != nil {
		return nil, fmt.Errorf("failed to get filesystem stats for path %s: %w", path, err)
	}

	// Calculate sizes in bytes first
	// stat.Blocks = total data blocks in filesystem
	// stat.Bavail = free blocks available to non-privileged user
	// stat.Bsize = fundamental filesystem block size
	totalBytes := stat.Blocks * uint64(stat.Bsize)
	availBytes := stat.Bavail * uint64(stat.Bsize)

	// Convert to megabytes
	const megabyte = 1024 * 1024
	totalMB := int64(totalBytes / megabyte)
	availMB := int64(availBytes / megabyte)

	return &DiskSpaceInfo{
		TotalMB:     totalMB,
		AvailableMB: availMB,
	}, nil
}
