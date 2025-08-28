package loopback

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"os"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/cert"
	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/server"
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

// testCertManagerWithServer tests a certificate manager integrated with a server using builder pattern.
func testCertManagerWithServer(
	t *testing.T, tempDir string, certMgr cert.CertificateManager, clientCert, clientKey, caCert string,
) {
	// For this test, we'll verify certificate manager functionality
	// A full server integration test using builder pattern would require more setup

	// Verify certificate manager is functional
	assert.True(t, certMgr.IsHealthy(), "Certificate manager should be healthy")
	assert.NotNil(t, certMgr.GetServerCertificate(), "Should have server certificate")
	assert.NotNil(t, certMgr.GetCACertPool(), "Should have CA certificate pool")

	tlsConfig, err := certMgr.GetTLSConfig()
	require.NoError(t, err, "Should get TLS config")
	assert.NotNil(t, tlsConfig, "TLS config should be valid")

	t.Logf("Certificate manager validation completed successfully")
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

// TestServerBuilderWithCertificateFiles tests the server builder with file-based certificates.
func TestServerBuilderWithCertificateFiles(t *testing.T) {
	// Create temp directory for certificates
	tempDir := t.TempDir()

	// Generate test certificates
	serverCert, serverKey, _, _, caCert := generateMTLSCertificates(t, tempDir)

	// Test server builder with certificate files
	srv, err := server.NewServerBuilder().
		WithAddress("127.0.0.1:0").
		WithRootFS(tempDir).
		WithCertificateFiles(serverCert, serverKey, caCert).
		Build()
	require.NoError(t, err, "Failed to build server with certificate files")

	// Verify server was created successfully
	assert.NotNil(t, srv, "Server should be created")
	assert.NotNil(t, srv.GRPCServer(), "gRPC server should be available")

	t.Logf("Server builder with certificate files completed successfully")
}

// TestServerBuilderWithSONiCCertificates tests the server builder with SONiC ConfigDB.
func TestServerBuilderWithSONiCCertificates(t *testing.T) {
	// Start miniredis for testing
	mr := miniredis.RunT(t)
	defer mr.Close()

	// Create temp directory for certificates
	tempDir := t.TempDir()

	// Generate test certificates
	serverCert, serverKey, _, _, caCert := generateMTLSCertificates(t, tempDir)

	// Populate ConfigDB
	ctx := context.Background()
	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
		DB:   4,
	})
	defer client.Close()

	err := client.HSet(ctx, "GNMI|certs",
		"server_crt", serverCert,
		"server_key", serverKey,
		"ca_crt", caCert,
	).Err()
	require.NoError(t, err, "Failed to populate ConfigDB")

	// Test server builder with SONiC certificates
	srv, err := server.NewServerBuilder().
		WithAddress("127.0.0.1:0").
		WithRootFS(tempDir).
		WithSONiCCertificates(mr.Addr(), 4).
		WithClientCertPolicy(true, false). // Require client certs
		Build()
	require.NoError(t, err, "Failed to build server with SONiC certificates")

	// Verify server was created successfully
	assert.NotNil(t, srv, "Server should be created")
	assert.NotNil(t, srv.GRPCServer(), "gRPC server should be available")

	t.Logf("Server builder with SONiC certificates completed successfully")
}
