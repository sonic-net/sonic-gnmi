package installer

import (
	"bufio"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/golang/glog"
)

const (
	// sonicInstallerBinary is the name of the sonic-installer command.
	sonicInstallerBinary = "sonic-installer"

	// nsenterBinary is the nsenter command for running in host namespace
	nsenterBinary = "nsenter"
)

// SonicInstaller provides a wrapper around the sonic-installer CLI tool.
type SonicInstaller struct {
	// This struct is kept for future extensibility but currently has no fields
}

// ImageInfo represents information about a SONiC image.
type ImageInfo struct {
	Name    string `json:"name"`
	Current bool   `json:"current"`
	Next    bool   `json:"next"`
}

// ListResult contains the result of sonic-installer list command.
type ListResult struct {
	Images  []ImageInfo `json:"images"`
	Current string      `json:"current"`
	Next    string      `json:"next"`
}

// CleanupResult contains the result of sonic-installer cleanup command.
type CleanupResult struct {
	RemovedImages []string `json:"removed_images"`
	Message       string   `json:"message"`
}

// SetDefaultResult contains the result of sonic-installer set-default command.
type SetDefaultResult struct {
	DefaultImage string `json:"default_image"`
	Message      string `json:"message"`
}

// NewSonicInstaller creates a new SonicInstaller instance.
func NewSonicInstaller() *SonicInstaller {
	return &SonicInstaller{}
}

// buildCommand creates an exec.Cmd that runs sonic-installer in the host namespace
func (si *SonicInstaller) buildCommand(args ...string) *exec.Cmd {
	// Build the full command with nsenter prefix
	nsenterArgs := []string{"-t", "1", "-m", "-u", "-i", "-n", "-p", "--", sonicInstallerBinary}
	nsenterArgs = append(nsenterArgs, args...)

	return exec.Command(nsenterBinary, nsenterArgs...)
}

// List executes sonic-installer list and returns parsed results.
func (si *SonicInstaller) List() (*ListResult, error) {
	glog.V(1).Info("Executing sonic-installer list")

	cmd := si.buildCommand("list")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to execute sonic-installer list: %w", err)
	}

	result, err := si.parseListOutput(string(output))
	if err != nil {
		return nil, fmt.Errorf("failed to parse sonic-installer list output: %w", err)
	}

	glog.V(1).Infof("Found %d images, current: %s, next: %s",
		len(result.Images), result.Current, result.Next)
	return result, nil
}

// SetDefault executes sonic-installer set-default for the specified image.
func (si *SonicInstaller) SetDefault(imageName string) (*SetDefaultResult, error) {
	glog.V(1).Infof("Setting default image to: %s", imageName)

	cmd := si.buildCommand("set-default", imageName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to execute sonic-installer set-default: %w, output: %s", err, string(output))
	}

	result := &SetDefaultResult{
		DefaultImage: imageName,
		Message:      strings.TrimSpace(string(output)),
	}

	glog.V(1).Infof("Set default image successfully: %s", imageName)
	return result, nil
}

// Cleanup executes sonic-installer cleanup with -y flag to auto-confirm.
func (si *SonicInstaller) Cleanup() (*CleanupResult, error) {
	glog.V(1).Info("Executing sonic-installer cleanup")

	cmd := si.buildCommand("cleanup", "-y")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to execute sonic-installer cleanup: %w, output: %s", err, string(output))
	}

	result, err := si.parseCleanupOutput(string(output))
	if err != nil {
		return nil, fmt.Errorf("failed to parse sonic-installer cleanup output: %w", err)
	}

	glog.V(1).Infof("Cleanup completed, removed %d images", len(result.RemovedImages))
	return result, nil
}

// parseListOutput parses the output of sonic-installer list command.
func (si *SonicInstaller) parseListOutput(output string) (*ListResult, error) {
	result := &ListResult{
		Images: make([]ImageInfo, 0),
	}

	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip header lines and empty lines
		if line == "" || strings.Contains(line, "Available") || strings.Contains(line, "--------") {
			continue
		}

		// Parse image lines
		// Format: "SONiC-OS-202311.1-build123 (Current)"
		// Format: "SONiC-OS-202311.1-build123 (Next)"
		// Format: "SONiC-OS-202311.1-build123"
		imageInfo := ImageInfo{}

		// Check for Current/Next markers
		if strings.Contains(line, "(Current)") {
			imageInfo.Current = true
			imageInfo.Name = strings.TrimSpace(strings.Replace(line, "(Current)", "", 1))
			result.Current = imageInfo.Name
		} else if strings.Contains(line, "(Next)") {
			imageInfo.Next = true
			imageInfo.Name = strings.TrimSpace(strings.Replace(line, "(Next)", "", 1))
			result.Next = imageInfo.Name
		} else {
			// Regular image line
			imageInfo.Name = line
		}

		// Only add if we have a valid image name
		if imageInfo.Name != "" && strings.HasPrefix(imageInfo.Name, "SONiC-OS-") {
			result.Images = append(result.Images, imageInfo)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading output: %w", err)
	}

	return result, nil
}

// parseCleanupOutput parses the output of sonic-installer cleanup command.
func (si *SonicInstaller) parseCleanupOutput(output string) (*CleanupResult, error) {
	result := &CleanupResult{
		RemovedImages: make([]string, 0),
		Message:       strings.TrimSpace(output),
	}

	// Look for "Removing image" lines
	// Format: "Removing image SONiC-OS-202311.1-build123"
	removeRegex := regexp.MustCompile(`Removing image (.+)`)

	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		matches := removeRegex.FindStringSubmatch(line)
		if len(matches) > 1 {
			imageName := strings.TrimSpace(matches[1])
			result.RemovedImages = append(result.RemovedImages, imageName)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading output: %w", err)
	}

	return result, nil
}
