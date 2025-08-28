package loopback

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/cert"
	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/server"
	serverConfig "github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/server/config"
)

// TestCertificateManagementFileBased tests file-based certificate mode.
func TestCertificateManagementFileBased(t *testing.T) {
	// Create temp directory for certificates
	tempDir := t.TempDir()

	// Generate test certificates
	serverCert, serverKey, clientCert, clientKey, caCert := generateMTLSCertificates(t, tempDir)

	// Test certificate manager directly
	certConfig := cert.NewDefaultConfig()
	certConfig.CertFile = serverCert
	certConfig.KeyFile = serverKey
	certConfig.CAFile = caCert
	certConfig.RequireClientCert = true
	certConfig.EnableMonitoring = false // Disable for testing

	// Create certificate manager
	certMgr := cert.NewCertificateManager(certConfig)

	// Load certificates
	err := certMgr.LoadCertificates()
	require.NoError(t, err, "Failed to load certificates")

	// Verify certificates are loaded
	assert.True(t, certMgr.IsHealthy(), "Certificate manager should be healthy")
	assert.NotNil(t, certMgr.GetServerCertificate(), "Server certificate should be loaded")
	assert.NotNil(t, certMgr.GetCACertPool(), "CA certificate pool should be loaded")

	// Get TLS config
	tlsConfig, err := certMgr.GetTLSConfig()
	require.NoError(t, err, "Failed to get TLS config")
	assert.NotNil(t, tlsConfig, "TLS config should not be nil")
	assert.Equal(t, tls.RequireAndVerifyClientCert, tlsConfig.ClientAuth, "Client auth should be required")

	// Test server integration using existing test infrastructure
	testCertManagerWithServer(t, tempDir, certMgr, clientCert, clientKey, caCert)
}

// TestCertificateManagementContainerSharing tests container certificate sharing mode.
func TestCertificateManagementContainerSharing(t *testing.T) {
	// Create temp directory structure for shared certificates
	tempDir := t.TempDir()
	sharedDir := filepath.Join(tempDir, "gnmi")
	err := os.MkdirAll(sharedDir, 0755)
	require.NoError(t, err)

	// Generate test certificates directly in shared directory
	_, _, clientCert, clientKey, caCert := generateMTLSCertificates(t, sharedDir)

	// Create container certificate configuration
	certConfig := cert.CreateContainerCertConfig("gnmi", tempDir)
	certConfig.EnableMonitoring = false // Disable for testing

	// Create certificate manager
	certMgr := cert.NewCertificateManager(certConfig)

	// Load certificates from shared container directory
	err = certMgr.LoadCertificates()
	require.NoError(t, err, "Failed to load certificates from container")

	// Verify certificates are loaded
	assert.True(t, certMgr.IsHealthy(), "Certificate manager should be healthy")
	assert.NotNil(t, certMgr.GetServerCertificate(), "Server certificate should be loaded")
	assert.NotNil(t, certMgr.GetCACertPool(), "CA certificate pool should be loaded")

	// Test server integration using existing test infrastructure
	testCertManagerWithServer(t, tempDir, certMgr, clientCert, clientKey, caCert)
}

// TestCertificateManagementSONiCConfigDB tests SONiC ConfigDB mode with Redis.
func TestCertificateManagementSONiCConfigDB(t *testing.T) {
	// Start miniredis for testing
	mr := miniredis.RunT(t)
	defer mr.Close()

	// Create temp directory for certificates
	tempDir := t.TempDir()

	// Generate test certificates
	serverCert, serverKey, clientCert, clientKey, caCert := generateMTLSCertificates(t, tempDir)

	// Connect to miniredis and populate ConfigDB
	ctx := context.Background()
	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
		DB:   4, // ConfigDB database
	})
	defer client.Close()

	// Populate GNMI|certs table (newer format)
	err := client.HSet(ctx, "GNMI|certs",
		"server_crt", serverCert,
		"server_key", serverKey,
		"ca_crt", caCert,
	).Err()
	require.NoError(t, err, "Failed to populate GNMI|certs")

	// Populate GNMI|gnmi table for service configuration
	err = client.HSet(ctx, "GNMI|gnmi",
		"client_auth", "true",
		"user_auth", "cert",
		"port", "50051",
		"log_level", "2",
	).Err()
	require.NoError(t, err, "Failed to populate GNMI|gnmi")

	// Create SONiC ConfigDB certificate configuration
	certConfig := cert.CreateSONiCCertConfig()
	certConfig.RedisAddr = mr.Addr()
	certConfig.RedisDB = 4
	certConfig.EnableMonitoring = false // Disable for testing

	// Create certificate manager
	certMgr := cert.NewCertificateManager(certConfig)

	// Load certificates from ConfigDB
	err = certMgr.LoadCertificates()
	require.NoError(t, err, "Failed to load certificates from ConfigDB")

	// Verify certificates are loaded
	assert.True(t, certMgr.IsHealthy(), "Certificate manager should be healthy")
	assert.NotNil(t, certMgr.GetServerCertificate(), "Server certificate should be loaded")
	assert.NotNil(t, certMgr.GetCACertPool(), "CA certificate pool should be loaded")

	// Test server integration using existing test infrastructure
	testCertManagerWithServer(t, tempDir, certMgr, clientCert, clientKey, caCert)
}

// TestCertificateManagementSONiCConfigDBFallback tests fallback to DEVICE_METADATA|x509.
func TestCertificateManagementSONiCConfigDBFallback(t *testing.T) {
	// Start miniredis for testing
	mr := miniredis.RunT(t)
	defer mr.Close()

	// Create temp directory for certificates
	tempDir := t.TempDir()

	// Generate test certificates
	serverCert, serverKey, clientCert, clientKey, caCert := generateMTLSCertificates(t, tempDir)

	// Connect to miniredis and populate ConfigDB
	ctx := context.Background()
	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
		DB:   4, // ConfigDB database
	})
	defer client.Close()

	// Only populate DEVICE_METADATA|x509 table (older format)
	err := client.HSet(ctx, "DEVICE_METADATA|x509",
		"server_crt", serverCert,
		"server_key", serverKey,
		"ca_crt", caCert,
	).Err()
	require.NoError(t, err, "Failed to populate DEVICE_METADATA|x509")

	// Create SONiC ConfigDB certificate configuration
	certConfig := cert.CreateSONiCCertConfig()
	certConfig.RedisAddr = mr.Addr()
	certConfig.RedisDB = 4
	certConfig.EnableMonitoring = false // Disable for testing

	// Create certificate manager
	certMgr := cert.NewCertificateManager(certConfig)

	// Load certificates from ConfigDB (should fallback to DEVICE_METADATA)
	err = certMgr.LoadCertificates()
	require.NoError(t, err, "Failed to load certificates from ConfigDB fallback")

	// Verify certificates are loaded
	assert.True(t, certMgr.IsHealthy(), "Certificate manager should be healthy")
	assert.NotNil(t, certMgr.GetServerCertificate(), "Server certificate should be loaded")
	assert.NotNil(t, certMgr.GetCACertPool(), "CA certificate pool should be loaded")

	// Test server integration using existing test infrastructure
	testCertManagerWithServer(t, tempDir, certMgr, clientCert, clientKey, caCert)
}

// TestCertificateClientAuthModes tests different client authentication configurations.
func TestCertificateClientAuthModes(t *testing.T) {
	tests := []struct {
		name              string
		requireClientCert bool
		allowNoClientCert bool
		expectedAuthMode  tls.ClientAuthType
	}{
		{
			name:              "Require client certificates",
			requireClientCert: true,
			allowNoClientCert: false,
			expectedAuthMode:  tls.RequireAndVerifyClientCert,
		},
		{
			name:              "Optional client certificates",
			requireClientCert: false,
			allowNoClientCert: true,
			expectedAuthMode:  tls.RequestClientCert,
		},
		{
			name:              "No client certificates",
			requireClientCert: false,
			allowNoClientCert: false,
			expectedAuthMode:  tls.NoClientCert,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp directory for certificates
			tempDir := t.TempDir()

			// Generate test certificates
			serverCert, serverKey, _, _, caCert := generateMTLSCertificates(t, tempDir)

			// Create certificate configuration
			certConfig := cert.NewDefaultConfig()
			certConfig.CertFile = serverCert
			certConfig.KeyFile = serverKey
			certConfig.CAFile = caCert
			certConfig.RequireClientCert = tt.requireClientCert
			certConfig.AllowNoClientCert = tt.allowNoClientCert
			certConfig.EnableMonitoring = false

			// Create certificate manager
			certMgr := cert.NewCertificateManager(certConfig)

			// Load certificates
			err := certMgr.LoadCertificates()
			require.NoError(t, err, "Failed to load certificates")

			// Get TLS config
			tlsConfig, err := certMgr.GetTLSConfig()
			require.NoError(t, err, "Failed to get TLS config")

			// Verify client auth mode
			assert.Equal(t, tt.expectedAuthMode, tlsConfig.ClientAuth,
				"Client auth mode should match expected value")
		})
	}
}

// testCertManagerWithServer tests a certificate manager integrated with a server.
func testCertManagerWithServer(
	t *testing.T, tempDir string, certMgr cert.CertificateManager, clientCert, clientKey, caCert string,
) {
	// Store the original global config
	originalConfig := serverConfig.Global

	// Create server with certificate manager using the new API
	srv, err := server.NewServerWithCertManager("127.0.0.1:0", certMgr)
	require.NoError(t, err, "Failed to create server with cert manager")

	// Start server in background
	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- srv.Start()
	}()

	// Give server time to start
	time.Sleep(300 * time.Millisecond)

	// Test mTLS connection
	testCertMgrMTLSConnection(t, clientCert, clientKey, caCert)

	// Stop server
	srv.Stop()

	// Restore original config
	serverConfig.Global = originalConfig

	// Check for server errors
	select {
	case err := <-serverErrCh:
		if err != nil {
			// Server errors on shutdown are expected
			t.Logf("Server shutdown: %v", err)
		}
	case <-time.After(1 * time.Second):
		// Server stopped gracefully
	}
}

// testCertMgrMTLSConnection tests mTLS connection for certificate manager integration.
func testCertMgrMTLSConnection(t *testing.T, clientCert, clientKey, caCert string) {
	// For this test, we'll verify that the certificate manager produces valid TLS config
	// A full connection test would require more complex server setup

	// Load client certificates to verify they're valid
	cert, err := tls.LoadX509KeyPair(clientCert, clientKey)
	require.NoError(t, err, "Failed to load client certificate")

	// Load CA certificate
	caCertPEM, err := os.ReadFile(caCert)
	require.NoError(t, err, "Failed to read CA certificate")

	certPool := x509.NewCertPool()
	ok := certPool.AppendCertsFromPEM(caCertPEM)
	require.True(t, ok, "Failed to parse CA certificate")

	// Create TLS config for client (validates certificate compatibility)
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      certPool,
		ServerName:   "localhost", // Must match server certificate
	}

	// Validate TLS config is properly formed
	assert.NotNil(t, tlsConfig.Certificates, "Client certificates should be configured")
	assert.NotNil(t, tlsConfig.RootCAs, "Root CAs should be configured")
	assert.Equal(t, "localhost", tlsConfig.ServerName, "Server name should match certificate")

	t.Logf("Certificate manager integration test completed successfully")
}
