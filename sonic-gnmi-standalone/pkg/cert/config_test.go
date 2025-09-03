package cert

import (
	"crypto/tls"
	"testing"
)

func TestNewDefaultConfig(t *testing.T) {
	config := NewDefaultConfig()

	// Check basic defaults - now using production paths
	if config.CertFile != "/etc/sonic/telemetry/gnmiserver.crt" {
		t.Errorf("Expected CertFile to be '/etc/sonic/telemetry/gnmiserver.crt', got %s", config.CertFile)
	}
	if config.KeyFile != "/etc/sonic/telemetry/gnmiserver.key" {
		t.Errorf("Expected KeyFile to be '/etc/sonic/telemetry/gnmiserver.key', got %s", config.KeyFile)
	}
	if config.CAFile != "/etc/sonic/telemetry/gnmiCA.pem" {
		t.Errorf("Expected CAFile to be '/etc/sonic/telemetry/gnmiCA.pem', got %s", config.CAFile)
	}

	// Check security defaults
	if !config.RequireClientCert {
		t.Error("Expected RequireClientCert to be true by default")
	}
	if config.OptionalClientCert {
		t.Error("Expected OptionalClientCert to be false by default")
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

	// Check ConfigDB defaults
	if config.ConfigTableName != "GNMI_CLIENT_CERT" {
		t.Errorf("Expected ConfigTableName to be 'GNMI_CLIENT_CERT', got %s", config.ConfigTableName)
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
				RequireClientCert: true, OptionalClientCert: true,
			},
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
		name               string
		requireClientCert  bool
		optionalClientCert bool
		expectedAuthMode   tls.ClientAuthType
	}{
		{
			name:               "require client cert",
			requireClientCert:  true,
			optionalClientCert: false,
			expectedAuthMode:   tls.RequireAndVerifyClientCert,
		},
		{
			name:               "optional client cert",
			requireClientCert:  false,
			optionalClientCert: true,
			expectedAuthMode:   tls.RequestClientCert,
		},
		{
			name:               "no client cert",
			requireClientCert:  false,
			optionalClientCert: false,
			expectedAuthMode:   tls.NoClientCert,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &CertConfig{
				RequireClientCert:  tt.requireClientCert,
				OptionalClientCert: tt.optionalClientCert,
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
			name:     "SONiC config",
			config:   &CertConfig{UseSONiCConfig: true, RedisAddr: "localhost:6379", RedisDB: 4},
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
