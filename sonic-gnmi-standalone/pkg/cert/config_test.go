package cert

import (
	"crypto/tls"
	"testing"
)

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
		expectedAuthMode  tls.ClientAuthType
	}{
		{
			name:              "require client cert",
			requireClientCert: true,
			expectedAuthMode:  tls.RequireAndVerifyClientCert,
		},
		{
			name:              "no client cert",
			requireClientCert: false,
			expectedAuthMode:  tls.NoClientCert,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &CertConfig{
				RequireClientCert: tt.requireClientCert,
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
