package cert

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClientAuthIntegrationWithConfigDB tests the complete flow from ConfigDB to client certificate verification.
func TestClientAuthIntegrationWithConfigDB(t *testing.T) {
	testCases := []struct {
		name          string
		configDBSetup func(*redis.Client)
		clientCertCN  string
		provideCert   bool
		expectedError string
		shouldSucceed bool
	}{
		{
			name: "authorized_client_with_cert",
			configDBSetup: func(client *redis.Client) {
				client.HSet(context.Background(), "GNMI_CLIENT_CERT|authorized.client.sonic", "role@", "gnmi_readwrite")
			},
			clientCertCN:  "authorized.client.sonic",
			provideCert:   true,
			expectedError: "",
			shouldSucceed: true,
		},
		{
			name: "unauthorized_client_with_cert",
			configDBSetup: func(client *redis.Client) {
				// Don't add this CN to ConfigDB
			},
			clientCertCN:  "unauthorized.client",
			provideCert:   true,
			expectedError: "client CN unauthorized.client is not authorized",
			shouldSucceed: false,
		},
		{
			name: "no_client_cert_provided",
			configDBSetup: func(client *redis.Client) {
				// ConfigDB setup doesn't matter for this test
			},
			clientCertCN:  "",
			provideCert:   false,
			expectedError: "no client certificate provided",
			shouldSucceed: false,
		},
		{
			name: "cert_with_empty_cn",
			configDBSetup: func(client *redis.Client) {
				// ConfigDB setup doesn't matter for this test
			},
			clientCertCN:  "", // Empty CN
			provideCert:   true,
			expectedError: "client certificate has no common name",
			shouldSucceed: false,
		},
		{
			name: "multiple_authorized_clients",
			configDBSetup: func(client *redis.Client) {
				client.HSet(context.Background(), "GNMI_CLIENT_CERT|client1.sonic", "role@", "gnmi_readwrite")
				client.HSet(context.Background(), "GNMI_CLIENT_CERT|client2.sonic", "role@", "gnmi_readonly")
			},
			clientCertCN:  "client2.sonic",
			provideCert:   true,
			expectedError: "",
			shouldSucceed: true,
		},
		{
			name: "client_with_special_characters_in_cn",
			configDBSetup: func(client *redis.Client) {
				client.HSet(context.Background(), "GNMI_CLIENT_CERT|test-client_123.example.com", "role@", "gnmi_admin")
			},
			clientCertCN:  "test-client_123.example.com",
			provideCert:   true,
			expectedError: "",
			shouldSucceed: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup miniredis
			mr := miniredis.RunT(t)
			defer mr.Close()

			// Setup ConfigDB state
			client := redis.NewClient(&redis.Options{
				Addr: mr.Addr(),
				DB:   4, // ConfigDB database
			})
			tc.configDBSetup(client)
			client.Close()

			// Create ClientAuthManager and load config
			authMgr := NewClientAuthManager(mr.Addr(), 4, "GNMI_CLIENT_CERT")
			err := authMgr.LoadClientCertConfig()
			require.NoError(t, err, "Failed to load client cert config")

			// Generate test certificate with specific CN (if cert should be provided)
			var rawCerts [][]byte
			if tc.provideCert {
				cert := generateTestCertWithCN(t, tc.clientCertCN)
				rawCerts = [][]byte{cert.Raw}
			}

			// Test VerifyClientCertificate directly
			result := authMgr.VerifyClientCertificate(rawCerts, nil)

			// Assert expected result
			if tc.shouldSucceed {
				assert.NoError(t, result, "Expected verification to succeed")
			} else {
				require.Error(t, result, "Expected verification to fail")
				assert.Contains(t, result.Error(), tc.expectedError, "Error message should match expected")
			}
		})
	}
}

// TestClientAuthManagerConfigDBIntegration tests the ConfigDB loading functionality.
func TestClientAuthManagerConfigDBIntegration(t *testing.T) {
	testCases := []struct {
		name                string
		configDBSetup       func(*redis.Client)
		expectedClientCount int
		expectedClients     map[string]string // CN -> Role mapping
	}{
		{
			name: "empty_configdb",
			configDBSetup: func(client *redis.Client) {
				// No setup - empty ConfigDB
			},
			expectedClientCount: 0,
			expectedClients:     map[string]string{},
		},
		{
			name: "single_client",
			configDBSetup: func(client *redis.Client) {
				client.HSet(context.Background(), "GNMI_CLIENT_CERT|single.client", "role@", "gnmi_readwrite")
			},
			expectedClientCount: 1,
			expectedClients: map[string]string{
				"single.client": "gnmi_readwrite",
			},
		},
		{
			name: "multiple_clients",
			configDBSetup: func(client *redis.Client) {
				client.HSet(context.Background(), "GNMI_CLIENT_CERT|client1", "role@", "gnmi_readwrite")
				client.HSet(context.Background(), "GNMI_CLIENT_CERT|client2", "role@", "gnmi_readonly")
				client.HSet(context.Background(), "GNMI_CLIENT_CERT|client3", "role@", "gnmi_admin")
			},
			expectedClientCount: 3,
			expectedClients: map[string]string{
				"client1": "gnmi_readwrite",
				"client2": "gnmi_readonly",
				"client3": "gnmi_admin",
			},
		},
		{
			name: "mixed_keys_only_gnmi_client_cert",
			configDBSetup: func(client *redis.Client) {
				// Add GNMI_CLIENT_CERT entries
				client.HSet(context.Background(), "GNMI_CLIENT_CERT|valid.client", "role@", "gnmi_readwrite")
				// Add non-matching entries (should be ignored)
				client.HSet(context.Background(), "OTHER_TABLE|should.ignore", "role@", "some_role")
				client.HSet(context.Background(), "GNMI|gnmi", "client_auth", "true")
			},
			expectedClientCount: 1,
			expectedClients: map[string]string{
				"valid.client": "gnmi_readwrite",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup miniredis
			mr := miniredis.RunT(t)
			defer mr.Close()

			// Setup ConfigDB state
			client := redis.NewClient(&redis.Options{
				Addr: mr.Addr(),
				DB:   4,
			})
			tc.configDBSetup(client)
			client.Close()

			// Create ClientAuthManager and load config
			authMgr := NewClientAuthManager(mr.Addr(), 4, "GNMI_CLIENT_CERT")
			err := authMgr.LoadClientCertConfig()
			require.NoError(t, err, "Failed to load client cert config")

			// Verify loaded clients
			authorizedCNs := authMgr.GetAuthorizedCNs()
			assert.Len(t, authorizedCNs, tc.expectedClientCount, "Unexpected number of authorized clients")

			// Verify each expected client is loaded with correct role
			for expectedCN := range tc.expectedClients {
				assert.Contains(t, authorizedCNs, expectedCN, "Expected CN not found in authorized list")

				// Test that verification would succeed for this CN
				cert := generateTestCertWithCN(t, expectedCN)
				result := authMgr.VerifyClientCertificate([][]byte{cert.Raw}, nil)
				assert.NoError(t, result, "Expected verification to succeed for %s", expectedCN)
			}
		})
	}
}

// generateTestCertWithCN creates a test certificate with the specified Common Name.
func generateTestCertWithCN(t *testing.T, commonName string) *x509.Certificate {
	// Generate private key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err, "Failed to generate private key")

	// Create certificate template
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName:   commonName,
			Organization: []string{"Test Org"},
			Country:      []string{"US"},
			Province:     []string{"CA"},
			Locality:     []string{"San Francisco"},
		},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:    x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}

	// Create certificate
	certBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	require.NoError(t, err, "Failed to create certificate")

	// Parse certificate
	cert, err := x509.ParseCertificate(certBytes)
	require.NoError(t, err, "Failed to parse certificate")

	return cert
}

// generateClientTestCertFiles creates test certificate files and returns their paths.
func generateClientTestCertFiles(t *testing.T, commonName string) (certFile, keyFile string) {
	tempDir := t.TempDir()

	// Generate private key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err, "Failed to generate private key")

	// Create certificate template
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName:   commonName,
			Organization: []string{"Test Org"},
			Country:      []string{"US"},
			Province:     []string{"CA"},
			Locality:     []string{"San Francisco"},
		},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:    x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}

	// Create certificate
	certBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	require.NoError(t, err, "Failed to create certificate")

	// Write certificate file
	certFile = filepath.Join(tempDir, "client.crt")
	certOut, err := os.Create(certFile)
	require.NoError(t, err, "Failed to create cert file")
	defer certOut.Close()

	err = pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: certBytes})
	require.NoError(t, err, "Failed to write certificate")

	// Write private key file
	keyFile = filepath.Join(tempDir, "client.key")
	keyOut, err := os.Create(keyFile)
	require.NoError(t, err, "Failed to create key file")
	defer keyOut.Close()

	err = pem.Encode(keyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	require.NoError(t, err, "Failed to write private key")

	return certFile, keyFile
}
