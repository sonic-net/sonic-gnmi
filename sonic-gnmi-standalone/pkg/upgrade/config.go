// Package upgrade provides client-side operations for SONiC package upgrades via gNOI.
//
// This package abstracts the complexity of gNOI System.SetPackage operations and provides
// validation, error handling, and a clean API for upgrade tools. It is designed to be
// reusable across different SONiC management utilities.
//
// Key features:
//   - Configuration-agnostic through the Config interface
//   - Built-in validation for URLs, MD5 checksums, and server addresses
//   - Support for both secure (TLS) and insecure connections
//   - Comprehensive error messages for troubleshooting
package upgrade

// Config defines the interface for package upgrade configuration.
// Different tools can implement this interface with their own config formats
// (YAML, JSON, command-line flags, etc.) while using the same upgrade logic.
//
// Example implementation:
//
//	type MyConfig struct {
//	    PackageURL string `json:"package_url"`
//	    Filename   string `json:"filename"`
//	    MD5        string `json:"md5"`
//	    Version    string `json:"version"`
//	    Activate   bool   `json:"activate"`
//	}
//
//	func (c *MyConfig) GetPackageURL() string { return c.PackageURL }
//	func (c *MyConfig) GetFilename() string   { return c.Filename }
//	func (c *MyConfig) GetMD5() string        { return c.MD5 }
//	func (c *MyConfig) GetVersion() string    { return c.Version }
//	func (c *MyConfig) GetActivate() bool     { return c.Activate }
type Config interface {
	// GetPackageURL returns the HTTP URL to download the package from.
	GetPackageURL() string

	// GetFilename returns the destination file path on the device.
	GetFilename() string

	// GetMD5 returns the expected MD5 checksum (hex string).
	GetMD5() string

	// GetVersion returns the package version (optional, can be empty).
	GetVersion() string

	// GetActivate returns whether to activate the package after installation.
	GetActivate() bool
}

// DownloadOptions contains options for direct package download operations.
// This is used when not loading from a config file but specifying parameters directly.
type DownloadOptions struct {
	// URL is the HTTP URL to download the package from
	URL string

	// Filename is the destination file path on the device
	Filename string

	// MD5 is the expected MD5 checksum (hex string)
	MD5 string

	// Version is the package version (optional)
	Version string

	// Activate indicates whether to activate the package after installation
	Activate bool
}

// GetPackageURL implements Config interface.
func (opts *DownloadOptions) GetPackageURL() string {
	return opts.URL
}

// GetFilename implements Config interface.
func (opts *DownloadOptions) GetFilename() string {
	return opts.Filename
}

// GetMD5 implements Config interface.
func (opts *DownloadOptions) GetMD5() string {
	return opts.MD5
}

// GetVersion implements Config interface.
func (opts *DownloadOptions) GetVersion() string {
	return opts.Version
}

// GetActivate implements Config interface.
func (opts *DownloadOptions) GetActivate() bool {
	return opts.Activate
}
