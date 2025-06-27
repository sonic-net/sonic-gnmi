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
