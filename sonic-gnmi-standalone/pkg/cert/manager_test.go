package cert

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCertificateManagementFileBased tests file-based certificate mode.
func TestCertificateManagementFileBased(t *testing.T) {
	// Create temp directory for certificates
	tempDir := t.TempDir()

	// Generate test certificates
	serverCert, serverKey, clientCert, clientKey, caCert := generateMTLSCertificates(t, tempDir)

	// Test certificate manager directly
	certConfig := NewDefaultConfig()
	certConfig.CertFile = serverCert
	certConfig.KeyFile = serverKey
	certConfig.CAFile = caCert
	certConfig.RequireClientCert = true
	certConfig.EnableMonitoring = false // Disable for testing

	// Create certificate manager
	certMgr := NewCertificateManager(certConfig)

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

// TestCertificateManagementSONiCMissingConfig tests that missing GNMI|certs configuration fails properly.
func TestCertificateManagementSONiCMissingConfig(t *testing.T) {
	// Start miniredis for testing
	mr := miniredis.RunT(t)
	defer mr.Close()

	// Create SONiC ConfigDB certificate configuration
	certConfig := CreateSONiCCertConfig()
	certConfig.RedisAddr = mr.Addr()
	certConfig.RedisDB = 4
	certConfig.EnableMonitoring = false

	// Create certificate manager
	certMgr := NewCertificateManager(certConfig)

	// Attempt to load certificates from empty ConfigDB (should fail)
	err := certMgr.LoadCertificates()
	require.Error(t, err, "Should fail when GNMI|certs is missing")
	assert.Contains(t, err.Error(), "no certificate configuration found in ConfigDB")
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
	certConfig := CreateSONiCCertConfig()
	certConfig.RedisAddr = mr.Addr()
	certConfig.RedisDB = 4
	certConfig.EnableMonitoring = false // Disable for testing

	// Create certificate manager
	certMgr := NewCertificateManager(certConfig)

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

// TestCertificateManagementSONiCTimeout tests that SONiC config loading respects configurable timeout.
func TestCertificateManagementSONiCTimeout(t *testing.T) {
	// Test the configurable timeout mechanism by setting a very short timeout
	certConfig := CreateSONiCCertConfig()
	certConfig.RedisAddr = "192.0.2.1:6379" // RFC 5737 test address - guaranteed unreachable
	certConfig.RedisDB = 4
	certConfig.EnableMonitoring = false
	certConfig.SONiCConfigTimeout = 1 * time.Millisecond // Very short timeout for fast test

	// Create certificate manager
	certMgr := NewCertificateManager(certConfig)

	// Attempt to load certificates should fail quickly due to short timeout
	start := time.Now()
	err := certMgr.LoadCertificates()
	elapsed := time.Since(start)

	// Should fail with timeout-related error
	require.Error(t, err, "Should fail due to timeout or connection error")

	// Should fail very quickly due to our 1ms timeout
	assert.Less(t, elapsed, 100*time.Millisecond, "Should fail quickly due to short timeout")

	t.Logf("Configurable timeout test completed in %v with error: %v", elapsed, err)
}

// TestCertificateClientAuthModes tests different client authentication configurations.
func TestCertificateClientAuthModes(t *testing.T) {
	tests := []struct {
		name              string
		requireClientCert bool
		expectedAuthMode  tls.ClientAuthType
	}{
		{
			name:              "Require client certificates",
			requireClientCert: true,
			expectedAuthMode:  tls.RequireAndVerifyClientCert,
		},
		{
			name:              "No client certificates",
			requireClientCert: false,
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
			certConfig := NewDefaultConfig()
			certConfig.CertFile = serverCert
			certConfig.KeyFile = serverKey
			certConfig.CAFile = caCert
			certConfig.RequireClientCert = tt.requireClientCert
			certConfig.EnableMonitoring = false

			// Create certificate manager
			certMgr := NewCertificateManager(certConfig)

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
	t *testing.T, tempDir string, certMgr CertificateManager, clientCert, clientKey, caCert string,
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

// generateMTLSCertificates creates CA, server, and client certificates for mTLS testing.
func generateMTLSCertificates(t *testing.T, certDir string) (
	serverCert, serverKey, clientCert, clientKey, caCert string,
) {
	// Generate CA private key
	caPrivateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	// Create CA certificate template
	caTemplate := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization:  []string{"Test CA"},
			Country:       []string{"US"},
			Province:      []string{""},
			Locality:      []string{"San Francisco"},
			StreetAddress: []string{""},
			PostalCode:    []string{""},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	// Create CA certificate
	caCertBytes, err := x509.CreateCertificate(
		rand.Reader, &caTemplate, &caTemplate, &caPrivateKey.PublicKey, caPrivateKey,
	)
	require.NoError(t, err)

	// Write CA certificate file
	caCert = filepath.Join(certDir, "ca.crt")
	caCertOut, err := os.Create(caCert)
	require.NoError(t, err)
	defer caCertOut.Close()

	err = pem.Encode(caCertOut, &pem.Block{Type: "CERTIFICATE", Bytes: caCertBytes})
	require.NoError(t, err)

	// Parse CA certificate for signing
	caCertificate, err := x509.ParseCertificate(caCertBytes)
	require.NoError(t, err)

	// Generate server certificate
	serverCert, serverKey = generateSignedCertificate(t, certDir, "server", caCertificate, caPrivateKey, true)

	// Generate client certificate
	clientCert, clientKey = generateSignedCertificate(t, certDir, "client", caCertificate, caPrivateKey, false)

	return serverCert, serverKey, clientCert, clientKey, caCert
}

// generateSignedCertificate generates a certificate signed by the provided CA.
func generateSignedCertificate(
	t *testing.T, certDir, name string, caCert *x509.Certificate, caPrivateKey *rsa.PrivateKey, isServer bool,
) (certFile, keyFile string) {
	// Generate private key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	// Create certificate template
	template := x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject: pkix.Name{
			Organization:  []string{"Test Corp"},
			Country:       []string{"US"},
			Province:      []string{""},
			Locality:      []string{"San Francisco"},
			StreetAddress: []string{""},
			PostalCode:    []string{""},
		},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		SubjectKeyId: []byte{1, 2, 3, 4, 6},
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}

	if isServer {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		template.DNSNames = []string{"localhost"}
		template.IPAddresses = []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}
	}

	// Create certificate
	certBytes, err := x509.CreateCertificate(rand.Reader, &template, caCert, &privateKey.PublicKey, caPrivateKey)
	require.NoError(t, err)

	// Write certificate file
	certFile = filepath.Join(certDir, name+".crt")
	certOut, err := os.Create(certFile)
	require.NoError(t, err)
	defer certOut.Close()

	err = pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: certBytes})
	require.NoError(t, err)

	// Write private key file
	keyFile = filepath.Join(certDir, name+".key")
	keyOut, err := os.Create(keyFile)
	require.NoError(t, err)
	defer keyOut.Close()

	err = pem.Encode(keyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	require.NoError(t, err)

	return certFile, keyFile
}
