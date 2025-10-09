package operationalhandler

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/sonic-net/sonic-gnmi/internal/firmware"
)

// FirmwareHandler implements PathHandler for firmware file queries.
// It acts as a gNMI adapter for the internal firmware package.
type FirmwareHandler struct {
	monitor *firmware.Monitor
}

// FirmwareFileInfo represents the firmware file information returned by gNMI queries.
// This maintains backward compatibility with existing gNMI handlers.
type FirmwareFileInfo struct {
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	ModTime     string `json:"mod_time"`
	IsDirectory bool   `json:"is_directory"`
	Permissions string `json:"permissions"`
	Type        string `json:"type"`
	MD5Sum      string `json:"md5sum,omitempty"` // MD5 checksum for files (empty for directories)
}

// NewFirmwareHandler creates a new FirmwareHandler.
func NewFirmwareHandler() *FirmwareHandler {
	return &FirmwareHandler{
		monitor: firmware.New(),
	}
}

// SupportedPaths returns the list of paths this handler supports.
func (h *FirmwareHandler) SupportedPaths() []string {
	return []string{
		"filesystem/files",
	}
}

// HandleGet processes a gNMI Get request for file listing information.
func (h *FirmwareHandler) HandleGet(path *gnmipb.Path) ([]byte, error) {
	// Extract the filesystem path, pattern, and field from the gNMI path
	filesystemPath, pattern, field, err := h.extractFilePathInfo(path)
	if err != nil {
		return nil, fmt.Errorf("failed to extract file path info: %v", err)
	}

	// Use internal firmware library to get file information
	var value interface{}

	switch field {
	case "count":
		files, err := h.monitor.ListFiles(filesystemPath, "")
		if err != nil {
			return nil, fmt.Errorf("failed to get file count for path %s: %v", filesystemPath, err)
		}
		// Filter by pattern if provided
		filteredFiles := h.filterFilesByPattern(files, pattern)
		value = len(filteredFiles)

	case "list":
		files, err := h.monitor.ListFiles(filesystemPath, "")
		if err != nil {
			return nil, fmt.Errorf("failed to list files for path %s: %v", filesystemPath, err)
		}

		// Filter by pattern if provided
		filteredFiles := h.filterFilesByPattern(files, pattern)

		// Convert to gNMI-compatible format
		fileInfos := make([]FirmwareFileInfo, len(filteredFiles))
		for i, file := range filteredFiles {
			fileInfos[i] = FirmwareFileInfo{
				Name:        file.Name,
				Size:        file.Size,
				ModTime:     file.ModTime.Format("2006-01-02T15:04:05Z07:00"),
				IsDirectory: file.IsDirectory,
				Permissions: file.Permissions,
				Type:        file.Type,
				MD5Sum:      file.MD5Sum,
			}
		}

		value = map[string]interface{}{
			"path":       filesystemPath,
			"pattern":    pattern,
			"file_count": len(fileInfos),
			"files":      fileInfos,
		}

	case "types":
		// Get files grouped by type
		files, err := h.monitor.ListFiles(filesystemPath, "")
		if err != nil {
			return nil, fmt.Errorf("failed to list files for path %s: %v", filesystemPath, err)
		}

		// Filter by pattern if provided
		filteredFiles := h.filterFilesByPattern(files, pattern)

		// Group files by type
		typeGroups := make(map[string][]FirmwareFileInfo)
		for _, file := range filteredFiles {
			if !file.IsDirectory {
				fileInfo := FirmwareFileInfo{
					Name:        file.Name,
					Size:        file.Size,
					ModTime:     file.ModTime.Format("2006-01-02T15:04:05Z07:00"),
					IsDirectory: file.IsDirectory,
					Permissions: file.Permissions,
					Type:        file.Type,
					MD5Sum:      file.MD5Sum,
				}
				typeGroups[file.Type] = append(typeGroups[file.Type], fileInfo)
			}
		}

		value = map[string]interface{}{
			"path":    filesystemPath,
			"pattern": pattern,
			"types":   typeGroups,
		}

	default:
		// Check if it's a specific file request
		if field != "" {
			fileInfo, err := h.monitor.GetFileInfo(filesystemPath, field, "")
			if err != nil {
				// Check if it's actually an unsupported field (not a file not found error)
				if strings.Contains(err.Error(), "file not found") {
					// Try to list files to see if directory exists - if it does, then it's an unsupported field
					_, listErr := h.monitor.ListFiles(filesystemPath, "")
					if listErr == nil {
						return nil, fmt.Errorf("unsupported file field: %s", field)
					}
				}
				return nil, fmt.Errorf("failed to get file info for %s in path %s: %v", field, filesystemPath, err)
			}

			fileData := FirmwareFileInfo{
				Name:        fileInfo.Name,
				Size:        fileInfo.Size,
				ModTime:     fileInfo.ModTime.Format("2006-01-02T15:04:05Z07:00"),
				IsDirectory: fileInfo.IsDirectory,
				Permissions: fileInfo.Permissions,
				Type:        fileInfo.Type,
				MD5Sum:      fileInfo.MD5Sum,
			}

			value = map[string]interface{}{
				"path": filesystemPath,
				"file": fileData,
			}
		} else {
			return nil, fmt.Errorf("unsupported file field: %s", field)
		}
	}

	// Marshal to JSON
	jsonData, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal file data: %v", err)
	}

	return jsonData, nil
}

// extractFilePathInfo extracts the filesystem path, pattern, and field from a gNMI path.
// Handles paths like /sonic/system/filesystem[path=/tmp]/files[pattern=*.bin]/list
func (h *FirmwareHandler) extractFilePathInfo(path *gnmipb.Path) (string, string, string, error) {
	if path == nil {
		return "", "", "", fmt.Errorf("path cannot be nil")
	}

	elems := path.GetElem()
	if len(elems) == 0 {
		return "", "", "", fmt.Errorf("path elements cannot be empty")
	}

	var filesystemPath string
	var pattern string = "*"  // default pattern matches all files
	var field string = "list" // default to listing all files

	// Look for filesystem element with path key
	for i, elem := range elems {
		if elem.GetName() == "filesystem" {
			keys := elem.GetKey()
			if pathValue, exists := keys["path"]; exists {
				if pathValue == "" {
					return "", "", "", fmt.Errorf("filesystem path cannot be empty")
				}
				filesystemPath = pathValue
			} else {
				return "", "", "", fmt.Errorf("filesystem path not specified in path")
			}
		}

		// Check for files element with optional pattern key
		if elem.GetName() == "files" {
			// Check for pattern filter in files element
			if keys := elem.GetKey(); len(keys) > 0 {
				if patternValue, exists := keys["pattern"]; exists {
					pattern = patternValue
				}
			}

			// Check if there's a next element specifying the field (list, count, types, etc.)
			if i+1 < len(elems) {
				nextElem := elems[i+1]
				field = nextElem.GetName()
			}
		}
	}

	if filesystemPath == "" {
		return "", "", "", fmt.Errorf("no filesystem path found in gNMI path")
	}

	return filesystemPath, pattern, field, nil
}

// filterFilesByPattern filters files based on a glob pattern.
// Pattern examples: "*.bin", "*.img", "*", "test-*", etc.
func (h *FirmwareHandler) filterFilesByPattern(files []firmware.FileInfo, pattern string) []firmware.FileInfo {
	// If pattern is "*" or empty, return all files
	if pattern == "*" || pattern == "" {
		return files
	}

	var filtered []firmware.FileInfo
	for _, file := range files {
		matched, err := filepath.Match(pattern, filepath.Base(file.Name))
		if err != nil {
			// If pattern is invalid, skip filtering and log warning
			// In production, you might want to return an error instead
			return files
		}
		if matched {
			filtered = append(filtered, file)
		}
	}

	return filtered
}

// GetFirmwareFilesByType retrieves firmware files filtered by type.
func (h *FirmwareHandler) GetFirmwareFilesByType(directory string, fileType string) ([]FirmwareFileInfo, error) {
	files, err := h.monitor.GetFilesByType(directory, "", fileType)
	if err != nil {
		return nil, err
	}

	firmwareFiles := make([]FirmwareFileInfo, len(files))
	for i, file := range files {
		firmwareFiles[i] = FirmwareFileInfo{
			Name:        file.Name,
			Size:        file.Size,
			ModTime:     file.ModTime.Format("2006-01-02T15:04:05Z07:00"),
			IsDirectory: file.IsDirectory,
			Permissions: file.Permissions,
			Type:        file.Type,
			MD5Sum:      file.MD5Sum,
		}
	}

	return firmwareFiles, nil
}

// IsValidDirectory checks if a firmware directory path is valid and accessible.
func (h *FirmwareHandler) IsValidDirectory(directory string) bool {
	_, err := h.monitor.GetFileCount(directory, "")
	return err == nil
}

// FormatFirmwareFileInfo formats firmware file information as a human-readable string.
func (h *FirmwareHandler) FormatFirmwareFileInfo(info *FirmwareFileInfo) string {
	if info == nil {
		return "No firmware file information available"
	}

	// Convert back to internal format for formatting
	modTime, _ := time.Parse("2006-01-02T15:04:05Z07:00", info.ModTime)
	internalInfo := &firmware.FileInfo{
		Name:        info.Name,
		Size:        info.Size,
		ModTime:     modTime,
		IsDirectory: info.IsDirectory,
		Permissions: info.Permissions,
		Type:        info.Type,
		MD5Sum:      info.MD5Sum,
	}

	return firmware.FormatFileInfo(internalInfo)
}
