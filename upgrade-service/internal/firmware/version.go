// Package firmware provides utilities for managing SONiC firmware images,
// including version extraction and cleanup operations.
//
// This package supports extracting version information from both ONIE-based
// (.bin) and Aboot-based (.swi) SONiC image files, mirroring the functionality
// of the sonic_installer binary-version command.
package firmware

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/golang/glog"
	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/config"
	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/paths"
)

const (
	// ImagePrefix is prepended to version strings to create full SONiC image names.
	ImagePrefix = "SONiC-OS-"
)

// ImageVersionInfo contains extracted version information from a SONiC image.
type ImageVersionInfo struct {
	Version     string `json:"version"`     // Raw version string (e.g., "202311.1-1234567")
	FullVersion string `json:"fullVersion"` // Prefixed version (e.g., "SONiC-OS-202311.1-1234567")
	ImageType   string `json:"imageType"`   // Type of image: "onie" or "aboot"
}

// GetBinaryImageVersion extracts version information from a SONiC image file.
func GetBinaryImageVersion(imagePath string) (*ImageVersionInfo, error) {
	glog.V(1).Infof("Extracting version from image: %s", imagePath)

	// Check if file exists
	if _, err := os.Stat(imagePath); os.IsNotExist(err) {
		return nil, fmt.Errorf("image file does not exist: %s", imagePath)
	}

	// Determine image type based on file extension and content
	imageType, err := detectImageType(imagePath)
	if err != nil {
		return nil, fmt.Errorf("failed to detect image type: %w", err)
	}

	var version string
	switch imageType {
	case "onie":
		version, err = extractOnieVersion(imagePath)
	case "aboot":
		version, err = extractAbootVersion(imagePath)
	default:
		return nil, fmt.Errorf("unsupported image type: %s", imageType)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to extract version from %s image: %w", imageType, err)
	}

	if version == "" {
		return nil, fmt.Errorf("no version found in image file")
	}

	fullVersion := ImagePrefix + version
	glog.V(1).Infof("Extracted version: %s (type: %s)", fullVersion, imageType)

	return &ImageVersionInfo{
		Version:     version,
		FullVersion: fullVersion,
		ImageType:   imageType,
	}, nil
}

// detectImageType determines the type of SONiC image based on file extension and content.
func detectImageType(imagePath string) (string, error) {
	ext := strings.ToLower(filepath.Ext(imagePath))

	switch ext {
	case ".swi":
		return "aboot", nil
	case ".bin":
		return "onie", nil
	default:
		// For files without clear extensions, try to detect by content
		return detectImageTypeByContent(imagePath)
	}
}

// detectImageTypeByContent attempts to detect image type by examining file content.
func detectImageTypeByContent(imagePath string) (string, error) {
	file, err := os.Open(imagePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	// Read first 512 bytes to check for ZIP signature (Aboot)
	buffer := make([]byte, 512)
	n, err := file.Read(buffer)
	if err != nil && err != io.EOF {
		return "", err
	}

	// Check for ZIP signature (PK)
	if n >= 4 && buffer[0] == 0x50 && buffer[1] == 0x4B {
		return "aboot", nil
	}

	// Default to ONIE for other file types
	return "onie", nil
}

// extractOnieVersion extracts version from ONIE-based SONiC image files (.bin).
func extractOnieVersion(imagePath string) (string, error) {
	glog.V(2).Infof("Extracting version from ONIE image: %s", imagePath)

	// Use pure Go implementation as primary approach
	version, err := extractOnieVersionPure(imagePath)
	if err != nil {
		return "", fmt.Errorf("failed to extract version from ONIE image: %w", err)
	}

	if version == "" {
		return "", fmt.Errorf("no version found in ONIE image")
	}

	return version, nil
}

// extractOnieVersionPure is a pure Go implementation for extracting ONIE version.
func extractOnieVersionPure(imagePath string) (string, error) {
	file, err := os.Open(imagePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	// Use regex to match image_version="..." pattern
	versionRegex := regexp.MustCompile(`image_version="([^"]+)"`)

	// Read file in chunks to handle binary data and large files efficiently
	const chunkSize = 64 * 1024     // 64KB chunks
	const maxReadSize = 1024 * 1024 // Stop after 1MB (header section)

	buffer := make([]byte, chunkSize)
	var accumulated []byte
	totalRead := 0

	for totalRead < maxReadSize {
		n, err := file.Read(buffer)
		if n == 0 {
			break
		}
		if err != nil && err != io.EOF {
			return "", fmt.Errorf("error reading file: %w", err)
		}

		accumulated = append(accumulated, buffer[:n]...)
		totalRead += n

		// Convert to string and search for version pattern
		content := string(accumulated)
		if matches := versionRegex.FindStringSubmatch(content); len(matches) > 1 {
			return matches[1], nil
		}

		// If we hit EOF, break
		if err == io.EOF {
			break
		}
	}

	return "", fmt.Errorf("version pattern not found in image file")
}

// extractAbootVersion extracts version from Aboot-based SONiC image files (.swi).
func extractAbootVersion(imagePath string) (string, error) {
	glog.V(2).Infof("Extracting version from Aboot image: %s", imagePath)

	// Open ZIP file
	reader, err := zip.OpenReader(imagePath)
	if err != nil {
		return "", fmt.Errorf("failed to open ZIP file: %w", err)
	}
	defer reader.Close()

	// Look for .imagehash file
	for _, file := range reader.File {
		if file.Name == ".imagehash" {
			return readZipFileContent(file)
		}
	}

	return "", fmt.Errorf(".imagehash file not found in image")
}

// readZipFileContent reads the content of a file within a ZIP archive.
func readZipFileContent(file *zip.File) (string, error) {
	reader, err := file.Open()
	if err != nil {
		return "", fmt.Errorf("failed to open file %s in ZIP: %w", file.Name, err)
	}
	defer reader.Close()

	content, err := io.ReadAll(reader)
	if err != nil {
		return "", fmt.Errorf("failed to read file %s: %w", file.Name, err)
	}

	return strings.TrimSpace(string(content)), nil
}

// ImageSearchResult contains information about a found image file.
type ImageSearchResult struct {
	FilePath    string            `json:"filePath"`    // Full path to the image file
	VersionInfo *ImageVersionInfo `json:"versionInfo"` // Extracted version information
	FileSize    int64             `json:"fileSize"`    // File size in bytes
}

// FindImagesByVersion searches for firmware images with a specific version.
// Deprecated: Use FindImagesByVersionInDirectories instead.
func FindImagesByVersion(targetVersion string) ([]*ImageSearchResult, error) {
	glog.V(1).Infof("Searching for images with version: %s", targetVersion)

	var results []*ImageSearchResult

	for _, dir := range DefaultSearchDirectories {
		dirPath := paths.ToHost(dir, config.Global.RootFS)
		glog.V(2).Infof("Searching in directory: %s", dirPath)

		// Check if directory exists
		if _, err := os.Stat(dirPath); os.IsNotExist(err) {
			glog.V(2).Infof("Directory does not exist: %s", dirPath)
			continue
		}

		for _, pattern := range SupportedImageExtensions {
			matches, err := filepath.Glob(filepath.Join(dirPath, pattern))
			if err != nil {
				glog.Errorf("Failed to glob pattern %s in %s: %v", pattern, dirPath, err)
				continue
			}

			for _, filePath := range matches {
				result, err := checkImageForVersion(filePath, targetVersion)
				if err != nil {
					glog.V(2).Infof("Error checking image %s: %v", filePath, err)
					continue
				}
				if result != nil {
					results = append(results, result)
				}
			}
		}
	}

	glog.V(1).Infof("Found %d images matching version %s", len(results), targetVersion)
	return results, nil
}

// FindAllImagesInDirectories searches for all firmware images in the specified directories.
func FindAllImagesInDirectories(directoryPaths []string, extensions []string) ([]*ImageSearchResult, error) {
	glog.V(1).Info("Searching for all firmware images")

	var results []*ImageSearchResult

	for _, dirPath := range directoryPaths {
		glog.V(2).Infof("Searching in directory: %s", dirPath)

		// Check if directory exists
		if _, err := os.Stat(dirPath); os.IsNotExist(err) {
			glog.V(2).Infof("Directory does not exist: %s", dirPath)
			continue
		}

		for _, pattern := range extensions {
			matches, err := filepath.Glob(filepath.Join(dirPath, pattern))
			if err != nil {
				glog.Errorf("Failed to glob pattern %s in %s: %v", pattern, dirPath, err)
				continue
			}

			for _, filePath := range matches {
				result, err := createImageSearchResult(filePath)
				if err != nil {
					glog.V(2).Infof("Error processing image %s: %v", filePath, err)
					continue
				}
				results = append(results, result)
			}
		}
	}

	glog.V(1).Infof("Found %d total firmware images", len(results))
	return results, nil
}

// FindImagesByVersionInDirectories searches for firmware images with a specific version in the specified directories.
func FindImagesByVersionInDirectories(
	targetVersion string, directoryPaths []string, extensions []string,
) ([]*ImageSearchResult, error) {
	glog.V(1).Infof("Searching for images with version: %s", targetVersion)

	var results []*ImageSearchResult

	for _, dirPath := range directoryPaths {
		glog.V(2).Infof("Searching in directory: %s", dirPath)

		// Check if directory exists
		if _, err := os.Stat(dirPath); os.IsNotExist(err) {
			glog.V(2).Infof("Directory does not exist: %s", dirPath)
			continue
		}

		for _, pattern := range extensions {
			matches, err := filepath.Glob(filepath.Join(dirPath, pattern))
			if err != nil {
				glog.Errorf("Failed to glob pattern %s in %s: %v", pattern, dirPath, err)
				continue
			}

			for _, filePath := range matches {
				result, err := checkImageForVersion(filePath, targetVersion)
				if err != nil {
					glog.V(2).Infof("Error checking image %s: %v", filePath, err)
					continue
				}
				if result != nil {
					results = append(results, result)
				}
			}
		}
	}

	glog.V(1).Infof("Found %d images matching version %s", len(results), targetVersion)
	return results, nil
}

// FindAllImages searches for all firmware images in the configured directories.
// Deprecated: Use FindAllImagesInDirectories instead.
func FindAllImages() ([]*ImageSearchResult, error) {
	glog.V(1).Info("Searching for all firmware images")

	var results []*ImageSearchResult

	for _, dir := range DefaultSearchDirectories {
		dirPath := paths.ToHost(dir, config.Global.RootFS)
		glog.V(2).Infof("Searching in directory: %s", dirPath)

		// Check if directory exists
		if _, err := os.Stat(dirPath); os.IsNotExist(err) {
			glog.V(2).Infof("Directory does not exist: %s", dirPath)
			continue
		}

		for _, pattern := range SupportedImageExtensions {
			matches, err := filepath.Glob(filepath.Join(dirPath, pattern))
			if err != nil {
				glog.Errorf("Failed to glob pattern %s in %s: %v", pattern, dirPath, err)
				continue
			}

			for _, filePath := range matches {
				result, err := createImageSearchResult(filePath)
				if err != nil {
					glog.V(2).Infof("Error processing image %s: %v", filePath, err)
					continue
				}
				results = append(results, result)
			}
		}
	}

	glog.V(1).Infof("Found %d total firmware images", len(results))
	return results, nil
}

// checkImageForVersion checks if an image file matches the target version.
func checkImageForVersion(filePath string, targetVersion string) (*ImageSearchResult, error) {
	versionInfo, err := GetBinaryImageVersion(filePath)
	if err != nil {
		return nil, err
	}

	// Check if version matches (both raw version and full version)
	if versionInfo.Version == targetVersion || versionInfo.FullVersion == targetVersion {
		return createImageSearchResultWithVersion(filePath, versionInfo)
	}

	return nil, nil // No match
}

// createImageSearchResult creates an ImageSearchResult for a file.
func createImageSearchResult(filePath string) (*ImageSearchResult, error) {
	versionInfo, err := GetBinaryImageVersion(filePath)
	if err != nil {
		return nil, err
	}

	return createImageSearchResultWithVersion(filePath, versionInfo)
}

// createImageSearchResultWithVersion creates an ImageSearchResult with existing version info.
func createImageSearchResultWithVersion(filePath string, versionInfo *ImageVersionInfo) (*ImageSearchResult, error) {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get file info for %s: %w", filePath, err)
	}

	return &ImageSearchResult{
		FilePath:    filePath,
		VersionInfo: versionInfo,
		FileSize:    fileInfo.Size(),
	}, nil
}
