package cert

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStartMonitoring(t *testing.T) {
	t.Run("monitoring_disabled", func(t *testing.T) {
		// Create temp directory and certificates
		tempDir := t.TempDir()
		certDir := filepath.Join(tempDir, "certs")
		err := os.MkdirAll(certDir, 0o755)
		require.NoError(t, err)

		serverCertFile, serverKeyFile, caCertFile := generateTestCertFiles(t, certDir)

		// Create cert config with monitoring disabled
		config := &CertConfig{
			CertFile:          serverCertFile,
			KeyFile:           serverKeyFile,
			CAFile:            caCertFile,
			RequireClientCert: true,
			EnableMonitoring:  false, // Monitoring disabled
		}

		// Create certificate manager
		certMgr := NewCertificateManager(config)
		require.NotNil(t, certMgr)

		// Load certificates
		err = certMgr.LoadCertificates()
		require.NoError(t, err)

		// Start monitoring should succeed but do nothing
		err = certMgr.StartMonitoring()
		assert.NoError(t, err)

		// Stop monitoring should be safe even when not started
		certMgr.StopMonitoring()
	})

	t.Run("monitoring_enabled", func(t *testing.T) {
		// Create temp directory and certificates
		tempDir := t.TempDir()
		certDir := filepath.Join(tempDir, "certs")
		err := os.MkdirAll(certDir, 0o755)
		require.NoError(t, err)

		serverCertFile, serverKeyFile, caCertFile := generateTestCertFiles(t, certDir)

		// Create cert config with monitoring enabled
		config := &CertConfig{
			CertFile:          serverCertFile,
			KeyFile:           serverKeyFile,
			CAFile:            caCertFile,
			RequireClientCert: true,
			EnableMonitoring:  true, // Monitoring enabled
		}

		// Create certificate manager
		certMgr := NewCertificateManager(config)
		require.NotNil(t, certMgr)

		// Load certificates
		err = certMgr.LoadCertificates()
		require.NoError(t, err)

		// Start monitoring should succeed
		err = certMgr.StartMonitoring()
		assert.NoError(t, err)

		// Give monitoring time to start
		time.Sleep(100 * time.Millisecond)

		// Stop monitoring
		certMgr.StopMonitoring()
	})
}

func TestStopMonitoring(t *testing.T) {
	t.Run("stop_without_start", func(t *testing.T) {
		config := NewDefaultConfig()
		config.EnableMonitoring = false

		certMgr := NewCertificateManager(config)
		require.NotNil(t, certMgr)

		// Should be safe to stop monitoring even if never started
		certMgr.StopMonitoring()
	})

	t.Run("stop_after_start", func(t *testing.T) {
		// Create temp directory and certificates
		tempDir := t.TempDir()
		certDir := filepath.Join(tempDir, "certs")
		err := os.MkdirAll(certDir, 0o755)
		require.NoError(t, err)

		serverCertFile, serverKeyFile, caCertFile := generateTestCertFiles(t, certDir)

		config := &CertConfig{
			CertFile:          serverCertFile,
			KeyFile:           serverKeyFile,
			CAFile:            caCertFile,
			RequireClientCert: true,
			EnableMonitoring:  true,
		}

		certMgr := NewCertificateManager(config)
		require.NotNil(t, certMgr)

		err = certMgr.LoadCertificates()
		require.NoError(t, err)

		err = certMgr.StartMonitoring()
		require.NoError(t, err)

		// Stop monitoring
		certMgr.StopMonitoring()

		// Should be safe to stop multiple times
		certMgr.StopMonitoring()
	})
}

func TestReload(t *testing.T) {
	t.Run("successful_reload", func(t *testing.T) {
		// Create temp directory and certificates
		tempDir := t.TempDir()
		certDir := filepath.Join(tempDir, "certs")
		err := os.MkdirAll(certDir, 0o755)
		require.NoError(t, err)

		serverCertFile, serverKeyFile, caCertFile := generateTestCertFiles(t, certDir)

		config := &CertConfig{
			CertFile:          serverCertFile,
			KeyFile:           serverKeyFile,
			CAFile:            caCertFile,
			RequireClientCert: true,
		}

		certMgr := NewCertificateManager(config)
		require.NotNil(t, certMgr)

		// Initial load
		err = certMgr.LoadCertificates()
		require.NoError(t, err)

		// Verify certificates are loaded
		assert.True(t, certMgr.IsHealthy())
		assert.NotNil(t, certMgr.GetServerCertificate())

		// Reload should succeed
		err = certMgr.Reload()
		assert.NoError(t, err)

		// Should still be healthy
		assert.True(t, certMgr.IsHealthy())
	})

	t.Run("reload_with_missing_files", func(t *testing.T) {
		// Create config with non-existent files
		config := &CertConfig{
			CertFile:          "/nonexistent/server.crt",
			KeyFile:           "/nonexistent/server.key",
			CAFile:            "/nonexistent/ca.crt",
			RequireClientCert: true,
		}

		certMgr := NewCertificateManager(config)
		require.NotNil(t, certMgr)

		// Reload should fail
		err := certMgr.Reload()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no such file or directory")
	})
}

func TestComputeAndLogChecksum(t *testing.T) {
	// Create a test file
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test.txt")
	testContent := "test certificate content"

	err := os.WriteFile(testFile, []byte(testContent), 0o644)
	require.NoError(t, err)

	config := NewDefaultConfig()
	certMgr := NewCertificateManager(config).(*CertManager)

	// This tests the computeAndLogChecksum function (currently at 0% coverage)
	// We can't easily test the checksum value since it's only logged, not returned
	// But we can verify the function doesn't panic with valid files
	t.Run("valid_file", func(t *testing.T) {
		// This should not panic
		certMgr.computeAndLogChecksum(testFile)
	})

	t.Run("nonexistent_file", func(t *testing.T) {
		// This should not panic even with nonexistent file
		certMgr.computeAndLogChecksum("/nonexistent/file.txt")
	})
}

func TestIsCertificateFile(t *testing.T) {
	config := NewDefaultConfig()
	certMgr := NewCertificateManager(config).(*CertManager)

	tests := []struct {
		name     string
		filename string
		expected bool
	}{
		{
			name:     "certificate_file_crt",
			filename: "server.crt",
			expected: true,
		},
		{
			name:     "certificate_file_cert",
			filename: "server.cert",
			expected: true,
		},
		{
			name:     "certificate_file_pem",
			filename: "ca.pem",
			expected: true,
		},
		{
			name:     "certificate_file_cer",
			filename: "ca.cer",
			expected: true,
		},
		{
			name:     "key_file",
			filename: "server.key",
			expected: true, // .key files are considered certificate files
		},
		{
			name:     "other_file",
			filename: "config.json",
			expected: false,
		},
		{
			name:     "no_extension",
			filename: "certificate",
			expected: false,
		},
		{
			name:     "uppercase_extension",
			filename: "server.CRT",
			expected: false, // Case sensitive
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := certMgr.isCertificateFile(tt.filename)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// generateTestCertFiles creates test certificate files and returns their paths.
func generateTestCertFiles(t *testing.T, certDir string) (certFile, keyFile, caCertFile string) {
	// Generate test certificates (reuse the helper from existing tests)
	_, _, clientCertFile, clientKeyFile, caCertFile := generateMTLSCertificates(t, certDir)

	// For simplicity, use the client cert as server cert in these tests
	// (the monitoring functions don't care about the certificate content)
	return clientCertFile, clientKeyFile, caCertFile
}
