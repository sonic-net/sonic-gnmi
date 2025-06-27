package firmware

// Package-level configuration for firmware operations.

var (
	// DefaultSearchDirectories defines the directories to search for firmware images.
	DefaultSearchDirectories = []string{"/host", "/tmp"}
	// SupportedImageExtensions defines the file extensions for SONiC images.
	SupportedImageExtensions = []string{"*.bin", "*.swi"}
)
