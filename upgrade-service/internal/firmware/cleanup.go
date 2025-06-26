package firmware

import (
	"os"
	"path/filepath"

	"github.com/golang/glog"

	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/config"
)

type CleanupResult struct {
	FilesDeleted    int32
	DeletedFiles    []string
	Errors          []string
	SpaceFreedBytes int64
}

type CleanupConfig struct {
	Directories []string
	Extensions  []string
}

func DefaultCleanupConfig() *CleanupConfig {
	return &CleanupConfig{
		Directories: []string{"/host", "/tmp"},
		Extensions:  []string{"*.bin", "*.swi", "*.rpm"},
	}
}

func CleanupOldFirmware() *CleanupResult {
	return CleanupOldFirmwareWithConfig(DefaultCleanupConfig())
}

func CleanupOldFirmwareWithConfig(cfg *CleanupConfig) *CleanupResult {
	result := &CleanupResult{
		DeletedFiles: make([]string, 0),
		Errors:       make([]string, 0),
	}

	for _, dir := range cfg.Directories {
		dirPath := config.GetHostPath(dir)
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
