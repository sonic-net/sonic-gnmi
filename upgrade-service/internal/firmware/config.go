package firmware

// Package-level configuration for firmware operations.

var (
	// DefaultSearchDirectories defines the directories to search for firmware images.
	DefaultSearchDirectories = []string{"/host", "/tmp"}
	// SupportedImageExtensions defines the file extensions for SONiC images.
	SupportedImageExtensions = []string{"*.bin", "*.swi"}
)

// ImageConsolidationMethod defines how image consolidation is performed.
type ImageConsolidationMethod int

const (
	// ConsolidationMethodCLI uses sonic-installer CLI commands.
	ConsolidationMethodCLI ImageConsolidationMethod = iota
	// ConsolidationMethodBootloader uses direct bootloader package integration.
	ConsolidationMethodBootloader
)

// ConsolidationConfig contains configuration for image consolidation operations.
type ConsolidationConfig struct {
	// Method specifies which approach to use for image consolidation
	Method ImageConsolidationMethod

	// DryRunDefault specifies the default dry-run behavior
	DryRunDefault bool
}

// DefaultConsolidationConfig returns the default configuration for image consolidation.
func DefaultConsolidationConfig() *ConsolidationConfig {
	return &ConsolidationConfig{
		Method:        ConsolidationMethodCLI, // Start with CLI, transition to bootloader later
		DryRunDefault: false,
	}
}

// GetConsolidationMethod returns a human-readable string for the consolidation method.
func (c *ConsolidationConfig) GetConsolidationMethod() string {
	switch c.Method {
	case ConsolidationMethodCLI:
		return "sonic-installer CLI"
	case ConsolidationMethodBootloader:
		return "bootloader package"
	default:
		return "unknown"
	}
}
