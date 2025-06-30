package firmware

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/golang/glog"
	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/bootloader"
	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/installer"
)

// ConsolidationResult contains the result of image consolidation operation.
type ConsolidationResult struct {
	// CurrentImage is the image that was set as default
	CurrentImage string

	// RemovedImages is the list of images that were removed
	RemovedImages []string

	// SpaceFreedBytes is the estimated space freed (if calculable)
	SpaceFreedBytes int64

	// Warnings contains any non-fatal warnings encountered
	Warnings []string

	// Executed indicates whether the operation was actually performed (false for dry run)
	Executed bool

	// Method indicates which consolidation method was used
	Method string
}

// ConsolidationService provides image consolidation functionality.
type ConsolidationService struct {
	config         *ConsolidationConfig
	sonicInstaller *installer.SonicInstaller
}

// NewConsolidationService creates a new consolidation service with default config.
func NewConsolidationService() *ConsolidationService {
	config := DefaultConsolidationConfig()
	return &ConsolidationService{
		config:         config,
		sonicInstaller: installer.NewSonicInstallerWithPath(config.SonicInstallerPath),
	}
}

// NewConsolidationServiceWithConfig creates a new consolidation service with custom config.
func NewConsolidationServiceWithConfig(config *ConsolidationConfig) *ConsolidationService {
	return &ConsolidationService{
		config:         config,
		sonicInstaller: installer.NewSonicInstallerWithPath(config.SonicInstallerPath),
	}
}

// ConsolidateImages consolidates SONiC images by setting current as default and removing unused images.
func (cs *ConsolidationService) ConsolidateImages(dryRun bool) (*ConsolidationResult, error) {
	glog.V(1).Infof("Starting image consolidation (dry_run=%t, method=%s)",
		dryRun, cs.config.GetConsolidationMethod())

	result := &ConsolidationResult{
		RemovedImages: make([]string, 0),
		Warnings:      make([]string, 0),
		Executed:      !dryRun,
		Method:        cs.config.GetConsolidationMethod(),
	}

	switch cs.config.Method {
	case ConsolidationMethodCLI:
		return cs.consolidateWithCLI(dryRun, result)
	case ConsolidationMethodBootloader:
		return cs.consolidateWithBootloader(dryRun, result)
	default:
		return nil, fmt.Errorf("unsupported consolidation method: %d", cs.config.Method)
	}
}

// consolidateWithCLI performs consolidation using sonic-installer CLI.
func (cs *ConsolidationService) consolidateWithCLI(
	dryRun bool, result *ConsolidationResult,
) (*ConsolidationResult, error) {
	// Step 1: Get current state
	listResult, err := cs.sonicInstaller.List()
	if err != nil {
		return nil, fmt.Errorf("failed to list images: %w", err)
	}

	glog.V(2).Infof("Found %d images before consolidation", len(listResult.Images))

	// Find current image from the list
	var currentImage string
	for _, img := range listResult.Images {
		if img.Current {
			currentImage = img.Name
			break
		}
	}

	// Fallback: if no current image found in parsed result, use sonic-installer's current field
	if currentImage == "" {
		currentImage = listResult.Current
	}

	// Final fallback: if still no current image, use the first available image
	if currentImage == "" && len(listResult.Images) > 0 {
		currentImage = listResult.Images[0].Name
		result.Warnings = append(result.Warnings,
			"Could not determine current image, using first available image")
	}

	if currentImage == "" {
		return nil, fmt.Errorf("no images found to consolidate")
	}

	result.CurrentImage = currentImage
	glog.V(2).Infof("Current image identified as: %s", currentImage)

	// Step 2: Set current image as default (if not dry run)
	if !dryRun {
		_, err := cs.sonicInstaller.SetDefault(currentImage)
		if err != nil {
			return nil, fmt.Errorf("failed to set default image: %w", err)
		}
		glog.V(2).Infof("Set default image to: %s", currentImage)
	}

	// Step 3: Estimate what cleanup will remove (before cleanup)
	removedImages, spaceFreed, err := cs.estimateCleanupResults(listResult, currentImage)
	if err != nil {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("Failed to estimate cleanup results: %v", err))
	}

	result.RemovedImages = removedImages
	result.SpaceFreedBytes = spaceFreed

	// Step 4: Perform cleanup (if not dry run)
	if !dryRun {
		cleanupResult, err := cs.sonicInstaller.Cleanup()
		if err != nil {
			return nil, fmt.Errorf("failed to cleanup images: %w", err)
		}

		// Update with actual cleanup results
		result.RemovedImages = cleanupResult.RemovedImages
		glog.V(2).Infof("Cleanup completed, removed %d images", len(cleanupResult.RemovedImages))
	}

	glog.V(1).Infof("Image consolidation completed: current=%s, removed=%d, executed=%t",
		result.CurrentImage, len(result.RemovedImages), result.Executed)

	return result, nil
}

// consolidateWithBootloader performs consolidation using bootloader package (future implementation).
func (cs *ConsolidationService) consolidateWithBootloader(
	dryRun bool, result *ConsolidationResult,
) (*ConsolidationResult, error) {
	// Get bootloader instance
	bl, err := bootloader.GetBootloader()
	if err != nil {
		return nil, fmt.Errorf("failed to get bootloader: %w", err)
	}

	// Get current image
	currentImage, err := bl.GetCurrentImage()
	if err != nil {
		return nil, fmt.Errorf("failed to get current image: %w", err)
	}

	result.CurrentImage = currentImage
	result.Warnings = append(result.Warnings,
		"Bootloader-based consolidation not yet implemented - would set current image as default and remove others")

	// NOTE: Implement bootloader-based consolidation
	// This would involve:
	// 1. Getting all installed images
	// 2. Setting current as default (bootloader-specific method)
	// 3. Removing non-current, non-next images (bootloader-specific method)

	return result, fmt.Errorf("bootloader-based consolidation not yet implemented")
}

// estimateCleanupResults estimates what images will be removed and space freed.
func (cs *ConsolidationService) estimateCleanupResults(
	listResult *installer.ListResult, currentImage string,
) ([]string, int64, error) {
	removedImages := make([]string, 0, len(listResult.Images))
	var totalSpaceFreed int64

	for _, img := range listResult.Images {
		// Skip current image
		if img.Name == currentImage {
			continue
		}

		// Skip next image if it's different from current
		if img.Next && img.Name != currentImage {
			continue
		}

		// This image would be removed
		removedImages = append(removedImages, img.Name)

		// Try to estimate space (best effort)
		if spaceFreed, err := cs.estimateImageSize(img.Name); err == nil {
			totalSpaceFreed += spaceFreed
		}
	}

	return removedImages, totalSpaceFreed, nil
}

// estimateImageSize attempts to estimate the size of an image directory.
func (cs *ConsolidationService) estimateImageSize(imageName string) (int64, error) {
	// Convert image name to directory name (e.g., SONiC-OS-202311.1 -> image-202311.1)
	dirName := strings.Replace(imageName, "SONiC-OS-", "image-", 1)
	imagePath := filepath.Join("/host", dirName)

	// Check if directory exists
	if _, err := os.Stat(imagePath); os.IsNotExist(err) {
		return 0, fmt.Errorf("image directory not found: %s", imagePath)
	}

	// Calculate directory size
	var size int64
	err := filepath.Walk(imagePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip files we can't access
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})

	return size, err
}
