package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfig_SetDefaults(t *testing.T) {
	tests := []struct {
		name     string
		config   Config
		expected Config
	}{
		{
			name: "empty optional fields",
			config: Config{
				Spec: Spec{
					Firmware: FirmwareSpec{
						DesiredVersion: "test",
						DownloadURL:    "http://test.com/test.bin",
					},
					Server: ServerSpec{
						Address: "localhost:50051",
					},
				},
			},
			expected: Config{
				Spec: Spec{
					Firmware: FirmwareSpec{
						DesiredVersion: "test",
						DownloadURL:    "http://test.com/test.bin",
						SavePath:       "/host",
					},
					Download: DownloadSpec{
						ConnectTimeout: 5, // Updated to match new default
						TotalTimeout:   300,
					},
					Server: ServerSpec{
						Address:    "localhost:50051",
						TLSEnabled: boolPtr(true),
					},
				},
			},
		},
		{
			name: "custom values preserved",
			config: Config{
				Spec: Spec{
					Firmware: FirmwareSpec{
						DesiredVersion: "test",
						DownloadURL:    "http://test.com/test.bin",
						SavePath:       "/custom/path",
					},
					Download: DownloadSpec{
						ConnectTimeout: 60,
						TotalTimeout:   600,
					},
					Server: ServerSpec{
						Address:    "localhost:50051",
						TLSEnabled: boolPtr(false),
					},
				},
			},
			expected: Config{
				Spec: Spec{
					Firmware: FirmwareSpec{
						DesiredVersion: "test",
						DownloadURL:    "http://test.com/test.bin",
						SavePath:       "/custom/path",
					},
					Download: DownloadSpec{
						ConnectTimeout: 60,
						TotalTimeout:   600,
					},
					Server: ServerSpec{
						Address:    "localhost:50051",
						TLSEnabled: boolPtr(false),
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.config.SetDefaults()
			assert.Equal(t, tt.expected, tt.config)
		})
	}
}

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr string
	}{
		{
			name:    "empty config",
			config:  Config{},
			wantErr: "apiVersion is required",
		},
		{
			name: "wrong apiVersion",
			config: Config{
				APIVersion: "v2",
			},
			wantErr: "unsupported apiVersion: v2",
		},
		{
			name: "wrong kind",
			config: Config{
				APIVersion: "sonic.net/v1",
				Kind:       "WrongKind",
			},
			wantErr: "invalid kind: WrongKind",
		},
		{
			name: "missing metadata.name",
			config: Config{
				APIVersion: "sonic.net/v1",
				Kind:       "UpgradeConfig",
			},
			wantErr: "metadata.name is required",
		},
		{
			name: "missing desiredVersion",
			config: Config{
				APIVersion: "sonic.net/v1",
				Kind:       "UpgradeConfig",
				Metadata:   Metadata{Name: "test"},
			},
			wantErr: "spec.firmware.desiredVersion is required",
		},
		{
			name: "missing downloadUrl",
			config: Config{
				APIVersion: "sonic.net/v1",
				Kind:       "UpgradeConfig",
				Metadata:   Metadata{Name: "test"},
				Spec: Spec{
					Firmware: FirmwareSpec{
						DesiredVersion: "test-version",
					},
				},
			},
			wantErr: "spec.firmware.downloadUrl is required",
		},
		{
			name: "missing server address",
			config: Config{
				APIVersion: "sonic.net/v1",
				Kind:       "UpgradeConfig",
				Metadata:   Metadata{Name: "test"},
				Spec: Spec{
					Firmware: FirmwareSpec{
						DesiredVersion: "test-version",
						DownloadURL:    "http://test.com/test.bin",
					},
				},
			},
			wantErr: "spec.server.address is required",
		},
		{
			name: "negative connect timeout",
			config: Config{
				APIVersion: "sonic.net/v1",
				Kind:       "UpgradeConfig",
				Metadata:   Metadata{Name: "test"},
				Spec: Spec{
					Firmware: FirmwareSpec{
						DesiredVersion: "test-version",
						DownloadURL:    "http://test.com/test.bin",
					},
					Download: DownloadSpec{
						ConnectTimeout: -1,
					},
					Server: ServerSpec{
						Address: "localhost:50051",
					},
				},
			},
			wantErr: "spec.download.connectTimeout must be positive",
		},
		{
			name: "connect timeout greater than total timeout",
			config: Config{
				APIVersion: "sonic.net/v1",
				Kind:       "UpgradeConfig",
				Metadata:   Metadata{Name: "test"},
				Spec: Spec{
					Firmware: FirmwareSpec{
						DesiredVersion: "test-version",
						DownloadURL:    "http://test.com/test.bin",
					},
					Download: DownloadSpec{
						ConnectTimeout: 100,
						TotalTimeout:   50,
					},
					Server: ServerSpec{
						Address: "localhost:50051",
					},
				},
			},
			wantErr: "connectTimeout cannot be greater than totalTimeout",
		},
		{
			name: "valid config",
			config: Config{
				APIVersion: "sonic.net/v1",
				Kind:       "UpgradeConfig",
				Metadata:   Metadata{Name: "test-upgrade"},
				Spec: Spec{
					Firmware: FirmwareSpec{
						DesiredVersion: "SONiC-OS-202311.1",
						DownloadURL:    "http://server.com/sonic.bin",
						SavePath:       "/host/images",
						ExpectedMD5:    "d41d8cd98f00b204e9800998ecf8427e",
					},
					Download: DownloadSpec{
						ConnectTimeout: 30,
						TotalTimeout:   300,
					},
					Server: ServerSpec{
						Address:     "localhost:50051",
						TLSEnabled:  boolPtr(true),
						TLSCertFile: "/etc/ssl/certs/server.crt",
						TLSKeyFile:  "/etc/ssl/private/server.key",
					},
				},
			},
			wantErr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestLoadFromFile(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
		check   func(t *testing.T, cfg *Config)
	}{
		{
			name: "valid minimal config",
			yaml: `apiVersion: sonic.net/v1
kind: UpgradeConfig
metadata:
  name: sonic-upgrade
spec:
  firmware:
    desiredVersion: "SONiC-OS-202311.1"
    downloadUrl: "http://server.com/sonic.bin"
  server:
    address: "localhost:50051"
`,
			check: func(t *testing.T, cfg *Config) {
				assert.Equal(t, "sonic.net/v1", cfg.APIVersion)
				assert.Equal(t, "UpgradeConfig", cfg.Kind)
				assert.Equal(t, "sonic-upgrade", cfg.Metadata.Name)
				assert.Equal(t, "SONiC-OS-202311.1", cfg.Spec.Firmware.DesiredVersion)
				assert.Equal(t, "http://server.com/sonic.bin", cfg.Spec.Firmware.DownloadURL)
				assert.Equal(t, "/host", cfg.Spec.Firmware.SavePath) // default
				assert.Equal(t, 5, cfg.Spec.Download.ConnectTimeout) // default updated
				assert.Equal(t, 300, cfg.Spec.Download.TotalTimeout) // default
				assert.Equal(t, "localhost:50051", cfg.Spec.Server.Address)
				assert.True(t, *cfg.Spec.Server.TLSEnabled) // default
			},
		},
		{
			name: "valid full config",
			yaml: `apiVersion: sonic.net/v1
kind: UpgradeConfig
metadata:
  name: sonic-upgrade
spec:
  firmware:
    desiredVersion: "SONiC-OS-202311.1"
    downloadUrl: "http://server.com/sonic.bin"
    savePath: "/host/images"
    expectedMd5: "d41d8cd98f00b204e9800998ecf8427e"
  download:
    connectTimeout: 60
    totalTimeout: 600
  server:
    address: "localhost:50051"
    tlsEnabled: false
    tlsCertFile: "/etc/ssl/certs/server.crt"
    tlsKeyFile: "/etc/ssl/private/server.key"
`,
			check: func(t *testing.T, cfg *Config) {
				assert.Equal(t, "/host/images", cfg.Spec.Firmware.SavePath)
				assert.Equal(t, "d41d8cd98f00b204e9800998ecf8427e", cfg.Spec.Firmware.ExpectedMD5)
				assert.Equal(t, 60, cfg.Spec.Download.ConnectTimeout)
				assert.Equal(t, 600, cfg.Spec.Download.TotalTimeout)
				assert.False(t, *cfg.Spec.Server.TLSEnabled)
				assert.Equal(t, "/etc/ssl/certs/server.crt", cfg.Spec.Server.TLSCertFile)
				assert.Equal(t, "/etc/ssl/private/server.key", cfg.Spec.Server.TLSKeyFile)
			},
		},
		{
			name:    "invalid YAML",
			yaml:    `invalid: yaml: content`,
			wantErr: "failed to parse YAML",
		},
		{
			name: "missing required field",
			yaml: `apiVersion: sonic.net/v1
kind: UpgradeConfig
metadata:
  name: sonic-upgrade
spec:
  firmware:
    desiredVersion: "SONiC-OS-202311.1"
  server:
    address: "localhost:50051"
`,
			wantErr: "spec.firmware.downloadUrl is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp file
			tmpDir := t.TempDir()
			configFile := filepath.Join(tmpDir, "config.yaml")
			err := os.WriteFile(configFile, []byte(tt.yaml), 0644)
			require.NoError(t, err)

			// Load config
			cfg, err := LoadFromFile(configFile)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				require.NoError(t, err)
				require.NotNil(t, cfg)
				if tt.check != nil {
					tt.check(t, cfg)
				}
			}
		})
	}
}

func TestLoadFromFile_FileErrors(t *testing.T) {
	t.Run("file not found", func(t *testing.T) {
		_, err := LoadFromFile("/non/existent/file.yaml")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to read config file")
	})

	t.Run("permission denied", func(t *testing.T) {
		// Create a file with no read permissions
		tmpDir := t.TempDir()
		configFile := filepath.Join(tmpDir, "config.yaml")
		err := os.WriteFile(configFile, []byte("test"), 0000)
		require.NoError(t, err)

		_, err = LoadFromFile(configFile)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to read config file")
	})
}

func TestConfig_GetTimeouts(t *testing.T) {
	cfg := &Config{
		Spec: Spec{
			Download: DownloadSpec{
				ConnectTimeout: 45,
				TotalTimeout:   900,
			},
		},
	}

	assert.Equal(t, "45s", cfg.GetConnectTimeout().String())
	assert.Equal(t, "15m0s", cfg.GetTotalTimeout().String())
}

func boolPtr(b bool) *bool {
	return &b
}
