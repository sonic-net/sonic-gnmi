// Package firmware provides firmware file listing and management utilities.
// This package is vanilla Go compatible and does not require CGO or SONiC dependencies.
//
// Unlike the sonic-image package which handles complete SONIC OS images, this package
// focuses on individual firmware files (drivers, bootloaders, device firmware, etc.).
package firmware

import (
	"crypto/md5"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/golang/glog"
)

// FileInfo represents information about a firmware file.
type FileInfo struct {
	Name        string    `json:"name"`
	Size        int64     `json:"size"`
	ModTime     time.Time `json:"mod_time"`
	IsDirectory bool      `json:"is_directory"`
	Permissions string    `json:"permissions"`
	Type        string    `json:"type"`             // "firmware", "driver", "bootloader", etc.
	MD5Sum      string    `json:"md5sum,omitempty"` // MD5 checksum for files (empty for directories)
}

// Monitor provides firmware file monitoring functionality.
type Monitor struct{}

// New creates a new firmware file monitor.
func New() *Monitor {
	return &Monitor{}
}

// ListFiles lists all firmware files in the specified directory.
// This function walks through the directory and returns information about firmware-related files.
func (m *Monitor) ListFiles(directory string, rootFS string) ([]FileInfo, error) {
	glog.V(2).Infof("Listing firmware files in directory: %s", directory)

	// Resolve the firmware directory path with rootFS
	resolvedPath := resolveFilesystemPath(directory, rootFS)

	// Check if directory exists
	if _, err := os.Stat(resolvedPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("directory does not exist: %s", directory)
	} else if err != nil {
		return nil, fmt.Errorf("failed to access directory %s: %v", directory, err)
	}

	var files []FileInfo

	// Walk through the directory and collect file information
	err := filepath.WalkDir(resolvedPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			glog.Warningf("Error accessing path %s: %v", path, err)
			return nil // Continue processing other files
		}

		// Skip the root directory itself
		if path == resolvedPath {
			return nil
		}

		// Get file info
		info, err := d.Info()
		if err != nil {
			glog.Warningf("Error getting info for %s: %v", path, err)
			return nil // Continue processing other files
		}

		// Get relative path from the firmware directory
		relPath, err := filepath.Rel(resolvedPath, path)
		if err != nil {
			glog.Warningf("Error getting relative path for %s: %v", path, err)
			return nil // Continue processing other files
		}

		// Determine firmware file type
		fileType := determineFirmwareType(relPath, info.IsDir())

		// Create file info entry
		fileInfo := FileInfo{
			Name:        relPath,
			Size:        info.Size(),
			ModTime:     info.ModTime(),
			IsDirectory: info.IsDir(),
			Permissions: info.Mode().String(),
			Type:        fileType,
		}

		// Calculate MD5 checksum for files (not directories)
		if !info.IsDir() {
			if md5sum, err := calculateMD5(path); err != nil {
				glog.Warningf("Error calculating MD5 for %s: %v", path, err)
				// Continue without MD5 - don't fail the entire operation
			} else {
				fileInfo.MD5Sum = md5sum
			}
		}

		files = append(files, fileInfo)
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to walk directory %s: %v", directory, err)
	}

	glog.V(2).Infof("Found %d files in firmware directory %s", len(files), directory)
	return files, nil
}

// GetFileCount returns the count of files in a firmware directory.
func (m *Monitor) GetFileCount(directory string, rootFS string) (int, error) {
	files, err := m.ListFiles(directory, rootFS)
	if err != nil {
		return 0, err
	}
	return len(files), nil
}

// GetFileInfo returns information about a specific firmware file.
func (m *Monitor) GetFileInfo(directory string, filename string, rootFS string) (*FileInfo, error) {
	files, err := m.ListFiles(directory, rootFS)
	if err != nil {
		return nil, err
	}

	// Look for the specific file
	for _, file := range files {
		if file.Name == filename {
			return &file, nil
		}
	}

	return nil, fmt.Errorf("file not found: %s", filename)
}

// GetFilesByType returns firmware files filtered by type.
func (m *Monitor) GetFilesByType(directory string, rootFS string, fileType string) ([]FileInfo, error) {
	files, err := m.ListFiles(directory, rootFS)
	if err != nil {
		return nil, err
	}

	var filteredFiles []FileInfo
	for _, file := range files {
		if file.Type == fileType || fileType == "all" {
			filteredFiles = append(filteredFiles, file)
		}
	}

	return filteredFiles, nil
}

// IsFirmwareFile checks if a file is likely a firmware file based on its extension and name.
// This distinguishes firmware files from SONIC OS images.
// Only supports .bin and .img extensions.
func IsFirmwareFile(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	lowername := strings.ToLower(filename)

	// Common firmware file extensions - only .bin and .img supported
	firmwareExts := []string{
		".bin", ".img",
	}

	// First check if it has a supported firmware extension
	for _, validExt := range firmwareExts {
		if ext == validExt {
			// For .img files, make sure it's not a SONIC OS image
			if ext == ".img" && strings.Contains(lowername, "sonic") {
				return false // This is likely a SONIC OS image, not firmware
			}
			return true
		}
	}

	// If it doesn't have a supported extension, it's not considered a firmware file
	// (even if it has firmware keywords in the name)
	return false
}

// calculateMD5 calculates the MD5 checksum of a file.
func calculateMD5(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := md5.New() // nosemgrep: go.lang.security.audit.crypto.use_of_weak_crypto.use-of-md5
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

// determineFirmwareType determines the type of firmware file based on name and properties.
func determineFirmwareType(filename string, isDir bool) string {
	if isDir {
		return "directory"
	}

	lowername := strings.ToLower(filename)
	ext := strings.ToLower(filepath.Ext(filename))

	// Only process files with supported firmware extensions
	if ext != ".bin" && ext != ".img" {
		return "other"
	}

	// For .img files, exclude SONIC OS images
	if ext == ".img" && strings.Contains(lowername, "sonic") {
		return "other"
	}

	// Determine specific firmware types (order matters for specificity)
	switch {
	case strings.Contains(lowername, "bootloader") || strings.Contains(lowername, "boot"):
		return "bootloader"
	case strings.Contains(lowername, "bios") || strings.Contains(lowername, "uefi"):
		return "bios"
	case strings.Contains(lowername, "microcode") || strings.Contains(lowername, "ucode"):
		return "microcode"
	case strings.Contains(lowername, "asic") || strings.Contains(lowername, "fpga"):
		return "asic"
	case strings.Contains(lowername, "phy") || strings.Contains(lowername, "serdes"):
		return "phy"
	case strings.Contains(lowername, "driver"):
		return "driver"
	default:
		return "firmware" // Generic firmware for supported extensions
	}
}

// resolveFilesystemPath resolves a filesystem path with the server's rootFS.
// This handles the difference between containerized and bare-metal deployments.
func resolveFilesystemPath(fsPath string, rootFS string) string {
	// If no rootFS is configured or it's root, use the path as-is
	if rootFS == "" || rootFS == "/" {
		return fsPath
	}

	// If the path is already absolute and starts with rootFS, use as-is
	if strings.HasPrefix(fsPath, rootFS) {
		return fsPath
	}

	// For containerized deployments, resolve the path within rootFS
	if strings.HasPrefix(fsPath, "/") {
		// Absolute path - join with rootFS
		return filepath.Join(rootFS, fsPath)
	}

	// Relative path - use as-is (though this is unusual for filesystem queries)
	return fsPath
}

// FormatFileInfo formats firmware file information as a human-readable string.
func FormatFileInfo(info *FileInfo) string {
	if info == nil {
		return "No firmware file information available"
	}

	typeStr := ""
	if info.Type != "other" {
		typeStr = fmt.Sprintf(" [%s]", info.Type)
	}

	if info.IsDirectory {
		return fmt.Sprintf("Directory: %s%s, Permissions: %s, Modified: %s",
			info.Name, typeStr, info.Permissions, info.ModTime.Format(time.RFC3339))
	}

	md5Str := ""
	if info.MD5Sum != "" {
		md5Str = fmt.Sprintf(", MD5: %s", info.MD5Sum)
	}

	return fmt.Sprintf("File: %s%s, Size: %d bytes, Permissions: %s, Modified: %s%s",
		info.Name, typeStr, info.Size, info.Permissions, info.ModTime.Format(time.RFC3339), md5Str)
}
