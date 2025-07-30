package upgrade

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// MockConfig implements the Config interface for testing.
type MockConfig struct {
	PackageURL    string
	Filename      string
	MD5           string
	Version       string
	Activate      bool
	ServerAddress string
	TLS           bool
}

func (m *MockConfig) GetPackageURL() string    { return m.PackageURL }
func (m *MockConfig) GetFilename() string      { return m.Filename }
func (m *MockConfig) GetMD5() string           { return m.MD5 }
func (m *MockConfig) GetVersion() string       { return m.Version }
func (m *MockConfig) GetActivate() bool        { return m.Activate }
func (m *MockConfig) GetServerAddress() string { return m.ServerAddress }
func (m *MockConfig) GetTLS() bool             { return m.TLS }

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name          string
		config        *MockConfig
		expectedError string
	}{
		{
			name: "valid_config",
			config: &MockConfig{
				PackageURL:    "http://example.com/package.bin",
				Filename:      "/opt/packages/package.bin",
				MD5:           "d41d8cd98f00b204e9800998ecf8427e",
				Version:       "1.0.0",
				Activate:      false,
				ServerAddress: "localhost:50055",
				TLS:           false,
			},
			expectedError: "",
		},
		{
			name: "missing_url",
			config: &MockConfig{
				PackageURL:    "",
				Filename:      "/opt/packages/package.bin",
				MD5:           "d41d8cd98f00b204e9800998ecf8427e",
				ServerAddress: "localhost:50055",
			},
			expectedError: "package URL is required",
		},
		{
			name: "missing_filename",
			config: &MockConfig{
				PackageURL:    "http://example.com/package.bin",
				Filename:      "",
				MD5:           "d41d8cd98f00b204e9800998ecf8427e",
				ServerAddress: "localhost:50055",
			},
			expectedError: "filename is required",
		},
		{
			name: "missing_md5",
			config: &MockConfig{
				PackageURL:    "http://example.com/package.bin",
				Filename:      "/opt/packages/package.bin",
				MD5:           "",
				ServerAddress: "localhost:50055",
			},
			expectedError: "MD5 checksum is required",
		},
		{
			name: "missing_server",
			config: &MockConfig{
				PackageURL:    "http://example.com/package.bin",
				Filename:      "/opt/packages/package.bin",
				MD5:           "d41d8cd98f00b204e9800998ecf8427e",
				ServerAddress: "",
			},
			expectedError: "server address is required",
		},
		{
			name: "invalid_url",
			config: &MockConfig{
				PackageURL:    "not-a-url",
				Filename:      "/opt/packages/package.bin",
				MD5:           "d41d8cd98f00b204e9800998ecf8427e",
				ServerAddress: "localhost:50055",
			},
			expectedError: "invalid package URL",
		},
		{
			name: "invalid_server_address",
			config: &MockConfig{
				PackageURL:    "http://example.com/package.bin",
				Filename:      "/opt/packages/package.bin",
				MD5:           "d41d8cd98f00b204e9800998ecf8427e",
				ServerAddress: "invalid-address",
			},
			expectedError: "invalid server address",
		},
		{
			name: "invalid_md5_length",
			config: &MockConfig{
				PackageURL:    "http://example.com/package.bin",
				Filename:      "/opt/packages/package.bin",
				MD5:           "too-short",
				ServerAddress: "localhost:50055",
			},
			expectedError: "invalid MD5 checksum",
		},
		{
			name: "invalid_md5_characters",
			config: &MockConfig{
				PackageURL:    "http://example.com/package.bin",
				Filename:      "/opt/packages/package.bin",
				MD5:           "gggggggggggggggggggggggggggggggg", // Invalid hex characters
				ServerAddress: "localhost:50055",
			},
			expectedError: "invalid MD5 checksum",
		},
		{
			name: "relative_filename",
			config: &MockConfig{
				PackageURL:    "http://example.com/package.bin",
				Filename:      "relative/path/package.bin",
				MD5:           "d41d8cd98f00b204e9800998ecf8427e",
				ServerAddress: "localhost:50055",
			},
			expectedError: "filename must be an absolute path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateConfig(tt.config)
			if tt.expectedError == "" {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			}
		})
	}
}

func TestDownloadOptions_ConfigInterface(t *testing.T) {
	opts := &DownloadOptions{
		URL:           "http://example.com/package.bin",
		Filename:      "/opt/packages/package.bin",
		MD5:           "d41d8cd98f00b204e9800998ecf8427e",
		Version:       "1.0.0",
		Activate:      true,
		ServerAddress: "localhost:50055",
		TLS:           true,
	}

	// Test that DownloadOptions implements Config interface
	var cfg Config = opts

	assert.Equal(t, "http://example.com/package.bin", cfg.GetPackageURL())
	assert.Equal(t, "/opt/packages/package.bin", cfg.GetFilename())
	assert.Equal(t, "d41d8cd98f00b204e9800998ecf8427e", cfg.GetMD5())
	assert.Equal(t, "1.0.0", cfg.GetVersion())
	assert.Equal(t, true, cfg.GetActivate())
	assert.Equal(t, "localhost:50055", cfg.GetServerAddress())
	assert.Equal(t, true, cfg.GetTLS())
}

func TestValidateURL(t *testing.T) {
	tests := []struct {
		name          string
		url           string
		expectedError string
	}{
		{"valid_http", "http://example.com/file.bin", ""},
		{"valid_https", "https://example.com/file.bin", ""},
		{"empty_url", "", "URL cannot be empty"},
		{"no_scheme", "example.com/file.bin", "URL must include scheme"},
		{"no_host", "http:///file.bin", "URL must include host"},
		{"invalid_scheme", "ftp://example.com/file.bin", "unsupported URL scheme 'ftp'"},
		{"malformed_url", "http://[invalid", "invalid URL format"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateURL(tt.url)
			if tt.expectedError == "" {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			}
		})
	}
}

func TestValidateServerAddress(t *testing.T) {
	tests := []struct {
		name          string
		addr          string
		expectedError string
	}{
		{"valid_address", "localhost:50055", ""},
		{"valid_ip", "192.168.1.1:8080", ""},
		{"empty_address", "", "address cannot be empty"},
		{"no_port", "localhost", "address must be in host:port format"},
		{"no_host", ":50055", "host cannot be empty"},
		{"no_port_value", "localhost:", "port cannot be empty"},
		{"too_many_colons", "host:port:extra", "address must be in host:port format"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateServerAddress(tt.addr)
			if tt.expectedError == "" {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			}
		})
	}
}

func TestValidateMD5(t *testing.T) {
	tests := []struct {
		name          string
		md5           string
		expectedError string
	}{
		{"valid_md5", "d41d8cd98f00b204e9800998ecf8427e", ""},
		{"valid_md5_uppercase", "D41D8CD98F00B204E9800998ECF8427E", ""},
		{"valid_md5_mixed", "D41d8cd98F00b204E9800998ecf8427e", ""},
		{"empty_md5", "", "MD5 checksum cannot be empty"},
		{"too_short", "d41d8cd98f00b204e9800998ecf842", "MD5 checksum must be 32 characters"},
		{"too_long", "d41d8cd98f00b204e9800998ecf8427e0", "MD5 checksum must be 32 characters"},
		{"invalid_chars", "g41d8cd98f00b204e9800998ecf8427e", "MD5 checksum contains invalid characters"},
		{"special_chars", "d41d8cd9-f00b204e9800998ecf8427e", "MD5 checksum contains invalid characters"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMD5(tt.md5)
			if tt.expectedError == "" {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			}
		})
	}
}
