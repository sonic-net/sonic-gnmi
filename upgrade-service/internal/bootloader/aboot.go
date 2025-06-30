package bootloader

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/golang/glog"
	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/config"
	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/paths"
)

const (
	// AbootHostPath is the base path where Aboot stores images.
	AbootHostPath = "/host"
	// ImageDirPrefix is the prefix used for image directories in Aboot.
	ImageDirPrefix = "image-"
	// AbootConfigPath is the path to Aboot's boot configuration.
	AbootConfigPath = "/host/boot-config"
)

// AbootBootloader implements the Bootloader interface for Aboot-based systems.
type AbootBootloader struct{}

// NewAbootBootloader creates a new Aboot bootloader instance.
func NewAbootBootloader() Bootloader {
	return &AbootBootloader{}
}

// Detect checks if Aboot bootloader is present on the system.
func (a *AbootBootloader) Detect() bool {
	hostPath := paths.ToHost(AbootHostPath, config.Global.RootFS)

	// Check for Aboot-specific files or directories
	abootIndicators := []string{
		filepath.Join(hostPath, "boot-config"),
		filepath.Join(hostPath, ".aboot"),
	}

	for _, indicator := range abootIndicators {
		if _, err := os.Stat(indicator); err == nil {
			glog.V(2).Infof("Aboot detected: found %s", indicator)
			return true
		}
	}

	// Also check for image directories which are typical in Aboot systems
	entries, err := os.ReadDir(hostPath)
	if err != nil {
		glog.V(2).Infof("Could not read %s: %v", hostPath, err)
		return false
	}

	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), ImageDirPrefix) {
			glog.V(2).Infof("Aboot detected: found image directory %s", entry.Name())
			return true
		}
	}

	glog.V(2).Info("Aboot not detected")
	return false
}

// GetInstalledImages returns all SONiC images found in Aboot image directories.
func (a *AbootBootloader) GetInstalledImages() ([]string, error) {
	hostPath := paths.ToHost(AbootHostPath, config.Global.RootFS)
	glog.V(1).Infof("Scanning for Aboot images in: %s", hostPath)

	entries, err := os.ReadDir(hostPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read host directory: %w", err)
	}

	var images []string
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), ImageDirPrefix) {
			// Convert image directory name to image name
			// e.g., "image-SONiC-OS-202311.1-12345" -> "SONiC-OS-202311.1-12345"
			imageName := strings.TrimPrefix(entry.Name(), ImageDirPrefix)
			images = append(images, imageName)
			glog.V(2).Infof("Found Aboot image: %s (dir: %s)", imageName, entry.Name())
		}
	}

	glog.V(1).Infof("Found %d images in Aboot", len(images))
	return images, nil
}

// GetCurrentImage returns the currently running SONiC image.
//
//nolint:dupl // Both GRUB and Aboot use similar cmdline parsing - this is expected
func (a *AbootBootloader) GetCurrentImage() (string, error) {
	glog.V(1).Info("Getting current image from /proc/cmdline")

	// Read /proc/cmdline to get the current boot parameters
	cmdlinePath := paths.ToHost("/proc/cmdline", config.Global.RootFS)
	content, err := os.ReadFile(cmdlinePath)
	if err != nil {
		return "", fmt.Errorf("failed to read /proc/cmdline: %w", err)
	}

	cmdline := string(content)
	glog.V(2).Infof("Cmdline content: %s", cmdline)

	// Look for SONiC image reference in command line
	if idx := strings.Index(cmdline, ImagePrefix); idx != -1 {
		// Find the end of the image name
		start := idx
		end := start
		for end < len(cmdline) && cmdline[end] != ' ' && cmdline[end] != '\t' && cmdline[end] != '\n' {
			end++
		}
		currentImage := cmdline[start:end]
		glog.V(1).Infof("Current image from cmdline: %s", currentImage)
		return currentImage, nil
	}

	// Fallback: try to get from installed images
	images, err := a.GetInstalledImages()
	if err != nil {
		return "", fmt.Errorf("failed to get installed images: %w", err)
	}

	if len(images) > 0 {
		// As a fallback, return the first image (this is not ideal but better than failing)
		glog.V(1).Infof("Could not determine current image from cmdline, using first available: %s", images[0])
		return images[0], nil
	}

	return "", fmt.Errorf("could not determine current image")
}

// GetNextImage returns the image that will be used on next boot.
func (a *AbootBootloader) GetNextImage() (string, error) {
	configPath := paths.ToHost(AbootConfigPath, config.Global.RootFS)
	glog.V(1).Infof("Reading Aboot config from: %s", configPath)

	// Check if boot-config file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		glog.V(2).Info("boot-config file not found, using current image as next")
		return a.GetCurrentImage()
	}

	content, err := os.ReadFile(configPath)
	if err != nil {
		return "", fmt.Errorf("failed to read boot-config: %w", err)
	}

	configContent := string(content)
	glog.V(2).Infof("Boot-config content: %s", configContent)

	// Parse the boot configuration
	// Aboot config typically contains the image path or name
	lines := strings.Split(configContent, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.Contains(line, ImagePrefix) {
			// Extract image name from the line
			if imageName := a.extractImageNameFromConfig(line); imageName != "" {
				glog.V(1).Infof("Next image from boot-config: %s", imageName)
				return imageName, nil
			}
		}
	}

	// If no specific next image found, return current image
	return a.GetCurrentImage()
}

// extractImageNameFromConfig extracts image name from a boot config line.
func (a *AbootBootloader) extractImageNameFromConfig(line string) string {
	// Look for image name patterns in the config line
	if idx := strings.Index(line, ImagePrefix); idx != -1 {
		// Find the end of the image name - look for version pattern
		start := idx
		end := start
		for end < len(line) && line[end] != ' ' && line[end] != '\t' &&
			line[end] != '\n' && line[end] != '"' && line[end] != '\'' && line[end] != '/' {
			end++
		}
		return line[start:end]
	}
	return ""
}
