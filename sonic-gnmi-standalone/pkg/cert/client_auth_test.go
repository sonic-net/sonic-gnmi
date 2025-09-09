package cert

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var ctx = context.Background()

func TestNewClientAuthManager(t *testing.T) {
	t.Run("valid_config", func(t *testing.T) {
		manager := NewClientAuthManager("localhost:6379", 4, "GNMI_CLIENT_CERT")
		assert.NotNil(t, manager)

		// Test that the manager can be used (fields are private)
		cns := manager.GetAuthorizedCNs()
		assert.NotNil(t, cns)
		assert.Empty(t, cns) // Should be empty initially
	})

	t.Run("empty_config_table_uses_default", func(t *testing.T) {
		manager := NewClientAuthManager("localhost:6379", 4, "")
		assert.NotNil(t, manager)

		// Should use default table name
		cns := manager.GetAuthorizedCNs()
		assert.NotNil(t, cns)
		assert.Empty(t, cns)
	})

	t.Run("empty_redis_addr", func(t *testing.T) {
		manager := NewClientAuthManager("", 4, "GNMI_CLIENT_CERT")
		assert.NotNil(t, manager)

		// Manager is created but LoadClientCertConfig will fail
		cns := manager.GetAuthorizedCNs()
		assert.NotNil(t, cns)
		assert.Empty(t, cns)
	})
}

func TestLoadClientCertConfig(t *testing.T) {
	// Start miniredis for testing
	mr := miniredis.RunT(t)
	defer mr.Close()

	t.Run("successful_load", func(t *testing.T) {
		// Setup test data in Redis
		client := redis.NewClient(&redis.Options{
			Addr: mr.Addr(),
			DB:   4,
		})
		defer client.Close()

		// Add test client certificate entries
		err := client.HSet(ctx, "GNMI_CLIENT_CERT|test.client1.com", "role@", "gnmi_readwrite").Err()
		require.NoError(t, err)
		err = client.HSet(ctx, "GNMI_CLIENT_CERT|test.client2.com", "role@", "gnmi_config_db_readwrite").Err()
		require.NoError(t, err)

		// Create manager and load config
		manager := NewClientAuthManager(mr.Addr(), 4, "GNMI_CLIENT_CERT")
		require.NotNil(t, manager)

		err = manager.LoadClientCertConfig()
		require.NoError(t, err)

		// Verify loaded data
		authorizedCNs := manager.GetAuthorizedCNs()
		assert.Contains(t, authorizedCNs, "test.client1.com")
		assert.Contains(t, authorizedCNs, "test.client2.com")
		assert.Len(t, authorizedCNs, 2)
	})

	t.Run("redis_connection_failure", func(t *testing.T) {
		// Use invalid Redis address
		manager := NewClientAuthManager("invalid-host:6379", 4, "GNMI_CLIENT_CERT")
		require.NotNil(t, manager)

		err := manager.LoadClientCertConfig()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to scan client certificates")
	})

	t.Run("empty_config_table", func(t *testing.T) {
		// Use fresh Redis instance for this test
		mrEmpty := miniredis.RunT(t)
		defer mrEmpty.Close()

		manager := NewClientAuthManager(mrEmpty.Addr(), 4, "GNMI_CLIENT_CERT")
		require.NotNil(t, manager)

		// Don't add any data to Redis
		err := manager.LoadClientCertConfig()
		require.NoError(t, err)

		// Should have empty authorized CNs
		authorizedCNs := manager.GetAuthorizedCNs()
		assert.Empty(t, authorizedCNs)
	})
}

func TestVerifyClientCertificate(t *testing.T) {
	// Start miniredis for testing
	mr := miniredis.RunT(t)
	defer mr.Close()

	// Setup test data in Redis
	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
		DB:   4,
	})
	defer client.Close()

	// Add authorized client CN
	err := client.HSet(ctx, "GNMI_CLIENT_CERT|test.authorized.com", "role@", "gnmi_readwrite").Err()
	require.NoError(t, err)

	// Create and load manager
	manager := NewClientAuthManager(mr.Addr(), 4, "GNMI_CLIENT_CERT")
	require.NotNil(t, manager)
	err = manager.LoadClientCertConfig()
	require.NoError(t, err)

	t.Run("no_certificate_provided", func(t *testing.T) {
		err := manager.VerifyClientCertificate([][]byte{}, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no client certificate provided")
	})

	t.Run("invalid_certificate_data", func(t *testing.T) {
		invalidCertData := []byte("invalid-certificate-data")
		err := manager.VerifyClientCertificate([][]byte{invalidCertData}, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to parse client certificate")
	})

	t.Run("certificate_without_common_name", func(t *testing.T) {
		// Generate certificate without CN
		cert := generateTestCertificate(t, "")
		err := manager.VerifyClientCertificate([][]byte{cert.Raw}, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "client certificate has no common name")
	})

	t.Run("unauthorized_common_name", func(t *testing.T) {
		// Generate certificate with unauthorized CN
		cert := generateTestCertificate(t, "unauthorized.client.com")
		err := manager.VerifyClientCertificate([][]byte{cert.Raw}, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "client CN unauthorized.client.com is not authorized")
	})

	t.Run("authorized_common_name", func(t *testing.T) {
		// Generate certificate with authorized CN
		cert := generateTestCertificate(t, "test.authorized.com")
		err := manager.VerifyClientCertificate([][]byte{cert.Raw}, nil)
		assert.NoError(t, err)
	})
}

func TestGetAuthorizedCNs(t *testing.T) {
	// Start miniredis for testing
	mr := miniredis.RunT(t)
	defer mr.Close()

	// Setup test data in Redis
	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
		DB:   4,
	})
	defer client.Close()

	// Add multiple client certificate entries
	testCNs := []string{
		"client1.example.com",
		"client2.example.com",
		"client3.example.com",
	}

	for _, cn := range testCNs {
		err := client.HSet(ctx, "GNMI_CLIENT_CERT|"+cn, "role@", "gnmi_readwrite").Err()
		require.NoError(t, err)
	}

	// Create and load manager
	manager := NewClientAuthManager(mr.Addr(), 4, "GNMI_CLIENT_CERT")
	require.NotNil(t, manager)
	err := manager.LoadClientCertConfig()
	require.NoError(t, err)

	// Test GetAuthorizedCNs
	authorizedCNs := manager.GetAuthorizedCNs()
	assert.Len(t, authorizedCNs, 3)
	for _, expectedCN := range testCNs {
		assert.Contains(t, authorizedCNs, expectedCN)
	}

	// Test concurrent access safety
	t.Run("concurrent_access", func(t *testing.T) {
		done := make(chan bool, 10)
		for i := 0; i < 10; i++ {
			go func() {
				cns := manager.GetAuthorizedCNs()
				assert.Len(t, cns, 3)
				done <- true
			}()
		}

		// Wait for all goroutines
		for i := 0; i < 10; i++ {
			<-done
		}
	})
}

func TestAddClientCN(t *testing.T) {
	manager := NewClientAuthManager("localhost:6379", 4, "GNMI_CLIENT_CERT")
	require.NotNil(t, manager)

	t.Run("add_new_client_cn", func(t *testing.T) {
		manager.AddClientCN("new.client.com", "gnmi_readwrite")

		authorizedCNs := manager.GetAuthorizedCNs()
		assert.Contains(t, authorizedCNs, "new.client.com")
	})

	t.Run("update_existing_client_cn", func(t *testing.T) {
		// Add initial CN
		manager.AddClientCN("existing.client.com", "gnmi_read")

		// Update with new role
		manager.AddClientCN("existing.client.com", "gnmi_readwrite")

		authorizedCNs := manager.GetAuthorizedCNs()
		assert.Contains(t, authorizedCNs, "existing.client.com")

		// The CN should still be authorized (role updated)
		cert := generateTestCertificate(t, "existing.client.com")
		err := manager.VerifyClientCertificate([][]byte{cert.Raw}, nil)
		assert.NoError(t, err)
	})

	t.Run("concurrent_add", func(t *testing.T) {
		done := make(chan bool, 10)
		for i := 0; i < 10; i++ {
			go func(id int) {
				cn := fmt.Sprintf("concurrent%d.client.com", id)
				manager.AddClientCN(cn, "gnmi_readwrite")
				done <- true
			}(i)
		}

		// Wait for all goroutines
		for i := 0; i < 10; i++ {
			<-done
		}

		authorizedCNs := manager.GetAuthorizedCNs()
		for i := 0; i < 10; i++ {
			expectedCN := fmt.Sprintf("concurrent%d.client.com", i)
			assert.Contains(t, authorizedCNs, expectedCN)
		}
	})
}

// generateTestCertificate creates a test X.509 certificate with specified CN.
func generateTestCertificate(t *testing.T, commonName string) *x509.Certificate {
	// Generate private key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	// Create certificate template
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization:  []string{"Test Corp"},
			Country:       []string{"US"},
			Province:      []string{""},
			Locality:      []string{"San Francisco"},
			StreetAddress: []string{""},
			PostalCode:    []string{""},
			CommonName:    commonName,
		},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:    x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)},
		DNSNames:    []string{"localhost"},
	}

	// Create certificate
	certBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	require.NoError(t, err)

	// Parse certificate
	cert, err := x509.ParseCertificate(certBytes)
	require.NoError(t, err)

	return cert
}
