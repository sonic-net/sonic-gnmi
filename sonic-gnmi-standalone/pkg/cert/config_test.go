package cert

import (
	"crypto/tls"
	"testing"
)

func TestNewDefaultConfig(t *testing.T) {
	config := NewDefaultConfig()

	// Check basic defaults
	if config.CertFile != "server.crt" {
		t.Errorf("Expected CertFile to be 'server.crt', got %s", config.CertFile)
	}
	if config.KeyFile != "server.key" {
		t.Errorf("Expected KeyFile to be 'server.key', got %s", config.KeyFile)
	}
	if config.CAFile != "ca.crt" {
		t.Errorf("Expected CAFile to be 'ca.crt', got %s", config.CAFile)
	}

	// Check security defaults
	if !config.RequireClientCert {
		t.Error("Expected RequireClientCert to be true by default")
	}
	if config.AllowNoClientCert {
		t.Error("Expected AllowNoClientCert to be false by default")
	}
	if config.MinTLSVersion != tls.VersionTLS12 {
		t.Errorf("Expected MinTLSVersion to be TLS 1.2, got %x", config.MinTLSVersion)
	}

	// Check monitoring defaults
	if !config.EnableMonitoring {
		t.Error("Expected EnableMonitoring to be true by default")
	}
	if !config.ChecksumValidation {
		t.Error("Expected ChecksumValidation to be true by default")
	}

	// Check cipher suites and curves are set
	if len(config.CipherSuites) == 0 {
		t.Error("Expected CipherSuites to be configured")
	}
	if len(config.CurvePreferences) == 0 {
		t.Error("Expected CurvePreferences to be configured")
	}
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name        string
		config      *CertConfig
		expectError bool
	}{
		{
			name:        "valid file-based config",
			config:      &CertConfig{CertFile: "server.crt", KeyFile: "server.key", CAFile: "ca.crt", RequireClientCert: true},
			expectError: false,
		},
		{
			name:        "valid container sharing config",
			config:      &CertConfig{ShareWithContainer: "gnmi", CertMountPath: "/etc/certs"},
			expectError: false,
		},
		{
			name:        "valid SONiC config",
			config:      &CertConfig{UseSONiCConfig: true},
			expectError: false,
		},
		{
			name:        "missing cert file",
			config:      &CertConfig{KeyFile: "server.key"},
			expectError: true,
		},
		{
			name:        "missing key file",
			config:      &CertConfig{CertFile: "server.crt"},
			expectError: true,
		},
		{
			name:        "missing CA file with client cert required",
			config:      &CertConfig{CertFile: "server.crt", KeyFile: "server.key", RequireClientCert: true},
			expectError: true,
		},
		{
			name: "conflicting client cert settings",
			config: &CertConfig{
				CertFile: "server.crt", KeyFile: "server.key",
				RequireClientCert: true, AllowNoClientCert: true,
			},
			expectError: true,
		},
		{
			name:        "missing mount path for container sharing",
			config:      &CertConfig{ShareWithContainer: "gnmi"},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.expectError && err == nil {
				t.Error("Expected validation error, but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected validation error: %v", err)
			}
		})
	}
}

func TestGetClientAuthMode(t *testing.T) {
	tests := []struct {
		name              string
		requireClientCert bool
		allowNoClientCert bool
		expectedAuthMode  tls.ClientAuthType
	}{
		{
			name:              "require client cert",
			requireClientCert: true,
			allowNoClientCert: false,
			expectedAuthMode:  tls.RequireAndVerifyClientCert,
		},
		{
			name:              "optional client cert",
			requireClientCert: false,
			allowNoClientCert: true,
			expectedAuthMode:  tls.RequestClientCert,
		},
		{
			name:              "no client cert",
			requireClientCert: false,
			allowNoClientCert: false,
			expectedAuthMode:  tls.NoClientCert,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &CertConfig{
				RequireClientCert: tt.requireClientCert,
				AllowNoClientCert: tt.allowNoClientCert,
			}

			authMode := config.GetClientAuthMode()
			if authMode != tt.expectedAuthMode {
				t.Errorf("Expected ClientAuthType %v, got %v", tt.expectedAuthMode, authMode)
			}
		})
	}
}

func TestConfigString(t *testing.T) {
	tests := []struct {
		name     string
		config   *CertConfig
		contains string
	}{
		{
			name:     "file-based config",
			config:   &CertConfig{CertFile: "server.crt", KeyFile: "server.key", CAFile: "ca.crt"},
			contains: "server.crt",
		},
		{
			name:     "container sharing config",
			config:   &CertConfig{ShareWithContainer: "gnmi", CertMountPath: "/etc/certs"},
			contains: "Container: gnmi",
		},
		{
			name:     "SONiC config",
			config:   &CertConfig{UseSONiCConfig: true, ConfigTable: "GNMI_CLIENT_CERT"},
			contains: "SONiC: true",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			str := tt.config.String()
			if len(str) == 0 {
				t.Error("Expected non-empty string representation")
			}
			// Note: We could add more specific string content checks here if needed
		})
	}
}
