package bootloader

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/golang/glog"
	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/config"
	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/paths"
)

const (
	// GrubConfigPath is the path to the GRUB configuration file.
	GrubConfigPath = "/host/grub/grub.cfg"
	// GrubEnvPath is the path to the GRUB environment file.
	GrubEnvPath = "/host/grub/grubenv"
	// ImagePrefix is the prefix used for SONiC images.
	ImagePrefix = "SONiC-OS-"
)

// GrubBootloader implements the Bootloader interface for GRUB-based systems.
type GrubBootloader struct{}

// NewGrubBootloader creates a new GRUB bootloader instance.
func NewGrubBootloader() Bootloader {
	return &GrubBootloader{}
}

// Detect checks if GRUB bootloader is present on the system.
func (g *GrubBootloader) Detect() bool {
	configPath := paths.ToHost(GrubConfigPath, config.Global.RootFS)
	if _, err := os.Stat(configPath); err == nil {
		glog.V(2).Infof("GRUB detected: found config at %s", configPath)
		return true
	}
	glog.V(2).Infof("GRUB not detected: config not found at %s", configPath)
	return false
}

// GetInstalledImages returns all SONiC images found in the GRUB configuration.
func (g *GrubBootloader) GetInstalledImages() ([]string, error) {
	configPath := paths.ToHost(GrubConfigPath, config.Global.RootFS)
	glog.V(1).Infof("Reading GRUB config from: %s", configPath)

	file, err := os.Open(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open GRUB config: %w", err)
	}
	defer file.Close()

	var images []string
	scanner := bufio.NewScanner(file)
	menuEntryRegex := regexp.MustCompile(`^menuentry\s+['"]([^'"]+)['"]`)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "menuentry") {
			matches := menuEntryRegex.FindStringSubmatch(line)
			if len(matches) > 1 {
				menuTitle := matches[1]
				if strings.Contains(menuTitle, ImagePrefix) {
					images = append(images, menuTitle)
					glog.V(2).Infof("Found image in GRUB config: %s", menuTitle)
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading GRUB config: %w", err)
	}

	glog.V(1).Infof("Found %d images in GRUB config", len(images))
	return images, nil
}

// GetCurrentImage returns the currently running SONiC image.
//
//nolint:dupl // Both GRUB and Aboot use similar cmdline parsing - this is expected
func (g *GrubBootloader) GetCurrentImage() (string, error) {
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
	// This can be in various formats, we'll try to extract the image name
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

	// If we can't find it in cmdline, try to get it from installed images
	// and match with the current kernel/root filesystem
	images, err := g.GetInstalledImages()
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
func (g *GrubBootloader) GetNextImage() (string, error) {
	envPath := paths.ToHost(GrubEnvPath, config.Global.RootFS)
	glog.V(1).Infof("Reading GRUB environment from: %s", envPath)

	// Check if grubenv file exists
	if _, err := os.Stat(envPath); os.IsNotExist(err) {
		glog.V(2).Info("grubenv file not found, checking for default boot entry")
		return g.getDefaultBootEntry()
	}

	content, err := os.ReadFile(envPath)
	if err != nil {
		return "", fmt.Errorf("failed to read grubenv: %w", err)
	}

	envContent := string(content)
	glog.V(2).Infof("Grubenv content: %s", envContent)

	// Look for saved_entry or next_entry in grubenv
	lines := strings.Split(envContent, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "saved_entry=") || strings.HasPrefix(line, "next_entry=") {
			// Extract the entry name
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				entryName := strings.Trim(parts[1], `"'`)
				if strings.Contains(entryName, ImagePrefix) {
					glog.V(1).Infof("Next image from grubenv: %s", entryName)
					return entryName, nil
				}
			}
		}
	}

	// If no saved entry found, return the default boot entry
	return g.getDefaultBootEntry()
}

// getDefaultBootEntry returns the default boot entry from GRUB config.
func (g *GrubBootloader) getDefaultBootEntry() (string, error) {
	configPath := paths.ToHost(GrubConfigPath, config.Global.RootFS)
	file, err := os.Open(configPath)
	if err != nil {
		return "", fmt.Errorf("failed to open GRUB config: %w", err)
	}
	defer file.Close()

	defaultEntry := g.parseDefaultEntry(file)
	entries := g.parseMenuEntries(file)

	return g.selectDefaultImage(defaultEntry, entries)
}

// parseDefaultEntry extracts the default entry number from GRUB config.
func (g *GrubBootloader) parseDefaultEntry(file *os.File) int {
	file.Seek(0, 0)
	scanner := bufio.NewScanner(file)
	defaultEntry := 0

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "set default=") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				defaultStr := strings.Trim(parts[1], `"'`)
				if _, err := fmt.Sscanf(defaultStr, "%d", &defaultEntry); err != nil {
					glog.V(2).Infof("Could not parse default entry: %s", defaultStr)
				}
			}
		}
	}
	return defaultEntry
}

// parseMenuEntries extracts all menu entries from GRUB config.
func (g *GrubBootloader) parseMenuEntries(file *os.File) []string {
	file.Seek(0, 0)
	scanner := bufio.NewScanner(file)
	menuEntryRegex := regexp.MustCompile(`^menuentry\s+['"]([^'"]+)['"]`)
	var entries []string

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "menuentry") {
			matches := menuEntryRegex.FindStringSubmatch(line)
			if len(matches) > 1 {
				entries = append(entries, matches[1])
			}
		}
	}
	return entries
}

// selectDefaultImage selects the default image from entries.
func (g *GrubBootloader) selectDefaultImage(defaultEntry int, entries []string) (string, error) {
	if defaultEntry < len(entries) {
		defaultImage := entries[defaultEntry]
		glog.V(1).Infof("Default boot entry: %s", defaultImage)
		return defaultImage, nil
	}

	// If we can't determine the default, return the first SONiC image
	for _, entry := range entries {
		if strings.Contains(entry, ImagePrefix) {
			glog.V(1).Infof("Using first SONiC image as default: %s", entry)
			return entry, nil
		}
	}

	return "", fmt.Errorf("could not determine default boot entry")
}
