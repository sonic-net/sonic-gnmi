// Package bootloader provides utilities for managing SONiC bootloader configurations,
// including detection of installed images, current image, and next boot image.
//
// This package supports different bootloader types (GRUB, Aboot, U-Boot) and
// mirrors the functionality of the sonic-installer list command.
package bootloader

import (
	"fmt"

	"github.com/golang/glog"
)

// Bootloader represents the interface for different bootloader implementations.
type Bootloader interface {
	// GetInstalledImages returns a list of all installed SONiC images.
	GetInstalledImages() ([]string, error)

	// GetCurrentImage returns the currently running SONiC image.
	GetCurrentImage() (string, error)

	// GetNextImage returns the image that will be used on next boot.
	GetNextImage() (string, error)

	// Detect returns true if this bootloader type is detected on the system.
	Detect() bool
}

// bootloaderTypes contains all supported bootloader implementations.
var bootloaderTypes = []func() Bootloader{
	NewGrubBootloader,
	NewAbootBootloader,
}

// GetBootloader detects and returns the appropriate bootloader implementation.
func GetBootloader() (Bootloader, error) {
	glog.V(1).Info("Detecting bootloader type")

	for _, bootloaderFunc := range bootloaderTypes {
		bootloader := bootloaderFunc()
		if bootloader.Detect() {
			glog.V(1).Infof("Detected bootloader: %T", bootloader)
			return bootloader, nil
		}
	}

	return nil, fmt.Errorf("bootloader could not be detected")
}

// ListInstalledImages returns information about all installed images.
func ListInstalledImages() (*ImageListInfo, error) {
	bootloader, err := GetBootloader()
	if err != nil {
		return nil, fmt.Errorf("failed to detect bootloader: %w", err)
	}

	current, err := bootloader.GetCurrentImage()
	if err != nil {
		glog.Errorf("Failed to get current image: %v", err)
		current = "Unknown"
	}

	next, err := bootloader.GetNextImage()
	if err != nil {
		glog.Errorf("Failed to get next image: %v", err)
		next = "Unknown"
	}

	images, err := bootloader.GetInstalledImages()
	if err != nil {
		return nil, fmt.Errorf("failed to get installed images: %w", err)
	}

	return &ImageListInfo{
		Current:   current,
		Next:      next,
		Available: images,
	}, nil
}

// ImageListInfo contains information about installed images.
type ImageListInfo struct {
	Current   string   `json:"current"`   // Currently running image
	Next      string   `json:"next"`      // Next boot image
	Available []string `json:"available"` // All available images
}
