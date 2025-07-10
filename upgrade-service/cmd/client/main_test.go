package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/client/config"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Helper function to create test config file.
func createTestConfig(t *testing.T) string {
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "test-config.yaml")

	configContent := `apiVersion: sonic.net/v1
kind: UpgradeConfig
metadata:
  name: test-upgrade
spec:
  firmware:
    desiredVersion: "SONiC-OS-202405.1"
    downloadUrl: "https://example.com/sonic-firmware.bin"
    savePath: "/tmp/test-firmware.bin"
    expectedMd5: "d41d8cd98f00b204e9800998ecf8427e"
  download:
    connectTimeout: 30
    totalTimeout: 300
  server:
    address: "localhost:50051"
    tlsEnabled: false
`

	err := os.WriteFile(configFile, []byte(configContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create test config file: %v", err)
	}

	return configFile
}

// Integration Tests for Validation Functions.
func TestValidationFunctions(t *testing.T) {
	t.Run("validateConfigFile", func(t *testing.T) {
		// Test non-existent file
		err := validateConfigFile("/non/existent/file.yaml")
		if err == nil {
			t.Error("Expected error for non-existent file")
		}

		// Test empty path
		err = validateConfigFile("")
		if err == nil {
			t.Error("Expected error for empty path")
		}

		// Test valid file
		configFile := createTestConfig(t)
		err = validateConfigFile(configFile)
		if err != nil {
			t.Errorf("Expected no error for valid file, got: %v", err)
		}
	})

	t.Run("validateServerAddress", func(t *testing.T) {
		tests := []struct {
			addr    string
			wantErr bool
		}{
			{"localhost:50051", false},
			{"127.0.0.1:8080", false},
			{"192.168.1.1:443", false},
			{"invalid", true},
			{"", true},
			{"localhost:", true},
			{":50051", true},
		}

		for _, tt := range tests {
			err := validateServerAddress(tt.addr)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateServerAddress(%q) error = %v, wantErr %v", tt.addr, err, tt.wantErr)
			}
		}
	})

	t.Run("validateURL", func(t *testing.T) {
		tests := []struct {
			url     string
			wantErr bool
		}{
			{"https://example.com/file.bin", false},
			{"http://localhost:8080/firmware", false},
			{"https://releases.example.com/sonic/firmware.bin", false},
			{"ftp://example.com/file", true}, // unsupported scheme
			{"example.com/file", true},       // no scheme
			{"", true},                       // empty
			{"http://", true},                // no host
		}

		for _, tt := range tests {
			err := validateURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		}
	})

	t.Run("validateSessionID", func(t *testing.T) {
		tests := []struct {
			sessionID string
			wantErr   bool
		}{
			{"valid-session-12345", false},
			{"session123456789", false},
			{"abc-def-123-456", false},
			{"short", true},         // too short
			{"", true},              // empty
			{"invalid@char", true},  // invalid characters
			{"invalid space", true}, // space character
			{"1234567", true},       // too short (7 chars)
			{"12345678", false},     // exactly 8 chars
		}

		for _, tt := range tests {
			err := validateSessionID(tt.sessionID)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateSessionID(%q) error = %v, wantErr %v", tt.sessionID, err, tt.wantErr)
			}
		}
	})

	t.Run("validatePath", func(t *testing.T) {
		tests := []struct {
			path    string
			wantErr bool
		}{
			{"/", false},
			{"/host", false},
			{"/usr/local", false},
			{"relative/path", true}, // relative path
			{"", true},              // empty
			{"/path/../..", true},   // contains invalid components
		}

		for _, tt := range tests {
			err := validatePath(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("validatePath(%q) error = %v, wantErr %v", tt.path, err, tt.wantErr)
			}
		}
	})

	t.Run("validateSavePath", func(t *testing.T) {
		// Create a temporary directory for testing
		tempDir := t.TempDir()

		tests := []struct {
			path    string
			wantErr bool
		}{
			{filepath.Join(tempDir, "test.bin"), false}, // valid path in existing dir
			{"/tmp/test.bin", false},                    // /tmp should exist
			{"", true},                                  // empty path
			{"/nonexistent/dir/file.bin", true},         // non-existent directory
		}

		for _, tt := range tests {
			err := validateSavePath(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateSavePath(%q) error = %v, wantErr %v", tt.path, err, tt.wantErr)
			}
		}
	})
}

func TestConfigValidation(t *testing.T) {
	t.Run("validateConfig with valid config", func(t *testing.T) {
		configFile := createTestConfig(t)
		cfg, err := config.LoadFromFile(configFile)
		if err != nil {
			t.Fatalf("Failed to load test config: %v", err)
		}

		err = validateConfig(cfg)
		if err != nil {
			t.Errorf("Expected no error for valid config, got: %v", err)
		}
	})

	t.Run("validateConfig with nil config", func(t *testing.T) {
		err := validateConfig(nil)
		if err == nil {
			t.Error("Expected error for nil config")
		}
	})

	t.Run("validateConfig with empty server address", func(t *testing.T) {
		cfg := &config.Config{
			Spec: config.Spec{
				Server: config.ServerSpec{
					Address:    "",
					TLSEnabled: boolPtr(false),
				},
			},
		}

		err := validateConfig(cfg)
		if err == nil {
			t.Error("Expected error for empty server address")
		}
	})
}

func TestErrorHandling(t *testing.T) {
	t.Run("handleGRPCError", func(t *testing.T) {
		tests := []struct {
			name        string
			err         error
			operation   string
			expectedMsg string
		}{
			{
				name:        "unavailable error",
				err:         status.Error(codes.Unavailable, "connection refused"),
				operation:   "test operation",
				expectedMsg: "server is unavailable",
			},
			{
				name:        "timeout error",
				err:         status.Error(codes.DeadlineExceeded, "timeout"),
				operation:   "test operation",
				expectedMsg: "operation timed out",
			},
			{
				name:        "not found error",
				err:         status.Error(codes.NotFound, "resource not found"),
				operation:   "test operation",
				expectedMsg: "resource not found",
			},
			{
				name:        "permission denied error",
				err:         status.Error(codes.PermissionDenied, "access denied"),
				operation:   "test operation",
				expectedMsg: "permission denied",
			},
			{
				name:        "invalid argument error",
				err:         status.Error(codes.InvalidArgument, "bad request"),
				operation:   "test operation",
				expectedMsg: "invalid arguments",
			},
			{
				name:        "unauthenticated error",
				err:         status.Error(codes.Unauthenticated, "auth failed"),
				operation:   "test operation",
				expectedMsg: "authentication failed",
			},
			{
				name:        "internal error",
				err:         status.Error(codes.Internal, "server error"),
				operation:   "test operation",
				expectedMsg: "internal server error",
			},
			{
				name:        "canceled error",
				err:         status.Error(codes.Canceled, "operation canceled"),
				operation:   "test operation",
				expectedMsg: "operation was canceled",
			},
			{
				name:        "non-gRPC error",
				err:         fmt.Errorf("regular error"),
				operation:   "test operation",
				expectedMsg: "failed to test operation",
			},
			{
				name:        "nil error",
				err:         nil,
				operation:   "test operation",
				expectedMsg: "",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				result := handleGRPCError(tt.err, tt.operation)
				if tt.err == nil {
					if result != nil {
						t.Errorf("Expected nil error for nil input, got: %v", result)
					}
					return
				}

				if result == nil {
					t.Error("Expected non-nil error for non-nil input")
					return
				}

				if !strings.Contains(result.Error(), tt.expectedMsg) {
					t.Errorf("Expected error message to contain %q, got %q", tt.expectedMsg, result.Error())
				}
			})
		}
	})
}

func TestHelperFunctions(t *testing.T) {
	t.Run("formatBytes", func(t *testing.T) {
		tests := []struct {
			bytes    int64
			expected string
		}{
			{0, "0 B"},
			{512, "512 B"},
			{1024, "1.0 KB"},
			{1536, "1.5 KB"},
			{1024 * 1024, "1.0 MB"},
			{1024 * 1024 * 1024, "1.0 GB"},
			{1024 * 1024 * 1024 * 1024, "1.0 TB"},
		}

		for _, tt := range tests {
			result := formatBytes(tt.bytes)
			if result != tt.expected {
				t.Errorf("formatBytes(%d) = %q, expected %q", tt.bytes, result, tt.expected)
			}
		}
	})

	t.Run("formatMB", func(t *testing.T) {
		tests := []struct {
			mb       uint64
			expected string
		}{
			{0, "0 MB"},
			{512, "512 MB"},
			{1024, "1.0 GB"},
			{1536, "1.5 GB"},
			{1024 * 1024, "1.0 TB"},
			{1024 * 1024 * 1024, "1.0 PB"},
		}

		for _, tt := range tests {
			result := formatMB(tt.mb)
			if result != tt.expected {
				t.Errorf("formatMB(%d) = %q, expected %q", tt.mb, result, tt.expected)
			}
		}
	})

	t.Run("boolPtr", func(t *testing.T) {
		truePtr := boolPtr(true)
		if truePtr == nil || *truePtr != true {
			t.Error("boolPtr(true) should return pointer to true")
		}

		falsePtr := boolPtr(false)
		if falsePtr == nil || *falsePtr != false {
			t.Error("boolPtr(false) should return pointer to false")
		}
	})
}

func TestProgressBar(t *testing.T) {
	t.Run("progressBar", func(t *testing.T) {
		tests := []struct {
			percent  int
			width    int
			expected string
		}{
			{0, 10, "----------"},
			{50, 10, "=====-----"},
			{100, 10, "=========="},
			{25, 4, "=---"},
			{75, 8, "======--"},
		}

		for _, tt := range tests {
			result := progressBar(tt.percent, tt.width)
			if result != tt.expected {
				t.Errorf("progressBar(%d, %d) = %q, expected %q", tt.percent, tt.width, result, tt.expected)
			}
		}
	})
}

func TestDryRunMode(t *testing.T) {
	// Test the dry run functionality directly
	configFile := createTestConfig(t)
	cfg, err := config.LoadFromFile(configFile)
	if err != nil {
		t.Fatalf("Failed to load test config: %v", err)
	}

	// Validate that the config was loaded correctly
	if cfg.Spec.Firmware.DesiredVersion != "SONiC-OS-202405.1" {
		t.Errorf("Expected version SONiC-OS-202405.1, got %s", cfg.Spec.Firmware.DesiredVersion)
	}

	if cfg.Spec.Firmware.DownloadURL != "https://example.com/sonic-firmware.bin" {
		t.Errorf("Expected download URL https://example.com/sonic-firmware.bin, got %s", cfg.Spec.Firmware.DownloadURL)
	}

	if cfg.Spec.Server.Address != "localhost:50051" {
		t.Errorf("Expected server localhost:50051, got %s", cfg.Spec.Server.Address)
	}
}

// Benchmark tests for performance validation.
func BenchmarkValidation(b *testing.B) {
	b.Run("validateURL", func(b *testing.B) {
		url := "https://example.com/firmware.bin"
		for i := 0; i < b.N; i++ {
			_ = validateURL(url)
		}
	})

	b.Run("validateSessionID", func(b *testing.B) {
		sessionID := "test-session-12345678"
		for i := 0; i < b.N; i++ {
			_ = validateSessionID(sessionID)
		}
	})

	b.Run("validateServerAddress", func(b *testing.B) {
		addr := "localhost:50051"
		for i := 0; i < b.N; i++ {
			_ = validateServerAddress(addr)
		}
	})

	b.Run("formatBytes", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = formatBytes(1024 * 1024 * 100) // 100MB
		}
	})

	b.Run("progressBar", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = progressBar(50, 50)
		}
	})
}
