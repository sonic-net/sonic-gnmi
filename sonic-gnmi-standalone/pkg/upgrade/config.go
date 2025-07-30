// Package upgrade provides reusable SONiC package upgrade operations.
// This package can be used by various SONiC management tools to perform
// package installations via gNOI SetPackage RPC.
package upgrade

// Config defines the interface for package upgrade configuration.
// Different tools can implement this interface with their own config formats
// (YAML, JSON, command-line flags, etc.) while using the same upgrade logic.
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

	// GetServerAddress returns the gNOI server address (host:port format).
	GetServerAddress() string

	// GetTLS returns whether to use TLS for the gRPC connection.
	GetTLS() bool
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

	// ServerAddress is the gNOI server address (host:port format)
	ServerAddress string

	// TLS indicates whether to use TLS for the gRPC connection
	TLS bool
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

// GetServerAddress implements Config interface.
func (opts *DownloadOptions) GetServerAddress() string {
	return opts.ServerAddress
}

// GetTLS implements Config interface.
func (opts *DownloadOptions) GetTLS() bool {
	return opts.TLS
}
