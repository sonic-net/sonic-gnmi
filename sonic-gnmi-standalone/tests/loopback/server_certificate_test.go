package loopback

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/reflection/grpc_reflection_v1alpha"

	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/server"
	serverConfig "github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/server/config"
)

func TestServerCertificateIntegration(t *testing.T) {
	// Initialize minimal global config for tests that don't set explicit builder values
	if serverConfig.Global == nil {
		serverConfig.Global = &serverConfig.Config{}
	}

	t.Run("ServerBuilderWithCertificateFiles", testServerBuilderWithCertificateFiles)
	t.Run("ServerBuilderWithSONiCCertificates", testServerBuilderWithSONiCCertificates)
	t.Run("ServerBuilderClientAuthRejection", testServerBuilderClientAuthRejection)
	t.Run("ServerBuilderInvalidCertificates", testServerBuilderInvalidCertificates)
}

// testServerBuilderWithCertificateFiles tests the server builder with file-based certificates.
func testServerBuilderWithCertificateFiles(t *testing.T) {
	// Create temp directory and generate certificates
	tempDir := t.TempDir()
	certDir := filepath.Join(tempDir, "certs")
	err := os.MkdirAll(certDir, 0o755)
	require.NoError(t, err)

	serverCertFile, serverKeyFile, clientCertFile, clientKeyFile, caCertFile := generateMTLSCertificates(t, certDir)

	// Find available port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	// Create server using builder pattern with certificate files
	// Note: For testing, we don't set up GNMI_CLIENT_CERT table, so client CN verification will fail
	// This test focuses on certificate loading, not client auth
	srv, err := server.NewServerBuilder().
		WithAddress(addr).
		WithRootFS(tempDir).
		WithCertificateFiles(serverCertFile, serverKeyFile, caCertFile).
		Build()
	require.NoError(t, err, "Failed to build server with certificate files")
	defer srv.Stop()

	// Start server
	serverStarted := make(chan error, 1)
	go func() {
		serverStarted <- srv.Start()
	}()

	// Give server time to start
	time.Sleep(500 * time.Millisecond)

	// Check if server had an error starting
	select {
	case err := <-serverStarted:
		if err != nil {
			t.Fatalf("Server failed to start: %v", err)
		}
	default:
		// Server is still running, which is expected
	}

	// Test mTLS connection with the builder-created server
	validateMTLSConnection(t, addr, clientCertFile, clientKeyFile, caCertFile)
}

// testServerBuilderWithSONiCCertificates tests the server builder with SONiC ConfigDB certificates.
func testServerBuilderWithSONiCCertificates(t *testing.T) {
	// Start miniredis for testing
	mr := miniredis.RunT(t)
	defer mr.Close()

	// Create temp directory and generate certificates
	tempDir := t.TempDir()
	certDir := filepath.Join(tempDir, "certs")
	err := os.MkdirAll(certDir, 0o755)
	require.NoError(t, err)

	serverCertFile, serverKeyFile, clientCertFile, clientKeyFile, caCertFile := generateMTLSCertificates(t, certDir)

	// Populate ConfigDB with certificate paths
	ctx := context.Background()
	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
		DB:   4, // ConfigDB database
	})
	defer client.Close()

	err = client.HSet(ctx, "GNMI|certs",
		"server_crt", serverCertFile,
		"server_key", serverKeyFile,
		"ca_crt", caCertFile,
	).Err()
	require.NoError(t, err, "Failed to populate ConfigDB")

	// Add authorized client CN for mTLS testing (matches cert_helpers.go client cert)
	err = client.HSet(ctx, "GNMI_CLIENT_CERT|test.client.gnmi.sonic",
		"role@", "gnmi_readwrite",
	).Err()
	require.NoError(t, err, "Failed to add client CN authorization")

	// Find available port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	// Create server using builder pattern with SONiC certificates
	// Now with proper client CN authorization set up in ConfigDB
	srv, err := server.NewServerBuilder().
		WithAddress(addr).
		WithRootFS(tempDir).
		WithSONiCCertificates(mr.Addr(), 4).
		WithClientCertPolicy(true). // Require client certs with CN verification
		Build()
	require.NoError(t, err, "Failed to build server with SONiC certificates")
	defer srv.Stop()

	// Start server
	serverStarted := make(chan error, 1)
	go func() {
		serverStarted <- srv.Start()
	}()

	// Give server time to start
	time.Sleep(500 * time.Millisecond)

	// Check if server had an error starting
	select {
	case err := <-serverStarted:
		if err != nil {
			t.Fatalf("Server failed to start: %v", err)
		}
	default:
		// Server is still running, which is expected
	}

	// Test mTLS connection with the builder-created server
	validateMTLSConnection(t, addr, clientCertFile, clientKeyFile, caCertFile)
}

// testServerBuilderClientAuthRejection tests that unauthorized client CNs are rejected.
func testServerBuilderClientAuthRejection(t *testing.T) {
	// Start miniredis for testing
	mr := miniredis.RunT(t)
	defer mr.Close()

	// Create temp directory and generate certificates
	tempDir := t.TempDir()
	certDir := filepath.Join(tempDir, "certs")
	err := os.MkdirAll(certDir, 0o755)
	require.NoError(t, err)

	serverCertFile, serverKeyFile, _, _, caCertFile := generateMTLSCertificates(t, certDir)

	// Generate a completely separate CA and client cert that will be unauthorized
	unauthorizedCADir := filepath.Join(tempDir, "unauthorized_ca")
	err = os.MkdirAll(unauthorizedCADir, 0o755)
	require.NoError(t, err)

	// Generate unauthorized client certificate with different CA (and thus different CN)
	unauthorizedClientCert, unauthorizedClientKey, _, _, unauthorizedCA := generateMTLSCertificates(t, unauthorizedCADir)
	_ = unauthorizedCA // Unused but needed to avoid too many blank identifiers

	// Populate ConfigDB with certificate paths
	ctx := context.Background()
	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
		DB:   4, // ConfigDB database
	})
	defer client.Close()

	err = client.HSet(ctx, "GNMI|certs",
		"server_crt", serverCertFile,
		"server_key", serverKeyFile,
		"ca_crt", caCertFile,
	).Err()
	require.NoError(t, err, "Failed to populate ConfigDB")

	// Add ONLY authorized client CN (test.client.gnmi.sonic from cert_helpers.go)
	err = client.HSet(ctx, "GNMI_CLIENT_CERT|test.client.gnmi.sonic",
		"role@", "gnmi_readwrite",
	).Err()
	require.NoError(t, err, "Failed to add authorized client CN")

	// Find available port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	// Create server with client CN verification
	srv, err := server.NewServerBuilder().
		WithAddress(addr).
		WithRootFS(tempDir).
		WithSONiCCertificates(mr.Addr(), 4).
		WithClientCertPolicy(true). // Require client certs with CN verification
		Build()
	require.NoError(t, err, "Failed to build server")
	defer srv.Stop()

	// Start server
	serverStarted := make(chan error, 1)
	go func() {
		serverStarted <- srv.Start()
	}()

	// Give server time to start
	time.Sleep(500 * time.Millisecond)

	// Check if server had an error starting
	select {
	case err := <-serverStarted:
		if err != nil {
			t.Fatalf("Server failed to start: %v", err)
		}
	default:
		// Server is still running, which is expected
	}

	// Test that unauthorized client is rejected
	testUnauthorizedClientRejection(t, addr, unauthorizedClientCert, unauthorizedClientKey, caCertFile)
}

// testServerBuilderInvalidCertificates tests various invalid certificate scenarios.
func testServerBuilderInvalidCertificates(t *testing.T) {
	// Create temp directory
	tempDir := t.TempDir()
	certDir := filepath.Join(tempDir, "certs")
	err := os.MkdirAll(certDir, 0o755)
	require.NoError(t, err)

	// Generate valid certificates first
	serverCertFile, serverKeyFile, _, _, caCertFile := generateMTLSCertificates(t, certDir)

	tests := []struct {
		name           string
		setupClient    func() (string, string) // Returns clientCert, clientKey
		expectConnFail bool
	}{
		{
			name: "NoClientCert",
			setupClient: func() (string, string) {
				return "", "" // No client certificate
			},
			expectConnFail: true,
		},
		{
			name: "WrongCACert",
			setupClient: func() (string, string) {
				// Generate client cert with different CA
				wrongCADir := filepath.Join(tempDir, "wrong_ca")
				err := os.MkdirAll(wrongCADir, 0o755)
				require.NoError(t, err)

				wrongClientCert, wrongClientKey, _, _, wrongCA := generateMTLSCertificates(t, wrongCADir)
				_ = wrongCA // Unused
				return wrongClientCert, wrongClientKey
			},
			expectConnFail: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Find available port
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			port := listener.Addr().(*net.TCPAddr).Port
			listener.Close()
			addr := fmt.Sprintf("127.0.0.1:%d", port)

			// Create server requiring client certs
			srv, err := server.NewServerBuilder().
				WithAddress(addr).
				WithRootFS(tempDir).
				WithCertificateFiles(serverCertFile, serverKeyFile, caCertFile).
				WithClientCertPolicy(true). // Require client certs
				Build()
			require.NoError(t, err, "Failed to build server")
			defer srv.Stop()

			// Start server
			serverStarted := make(chan error, 1)
			go func() {
				serverStarted <- srv.Start()
			}()

			time.Sleep(500 * time.Millisecond)

			// Check if server had an error starting
			select {
			case err := <-serverStarted:
				if err != nil {
					t.Fatalf("Server failed to start: %v", err)
				}
			default:
				// Server is still running, which is expected
			}

			// Setup client and test connection
			clientCertFile, clientKeyFile := tt.setupClient()
			testInvalidClientConnection(t, addr, clientCertFile, clientKeyFile, caCertFile, tt.expectConnFail)
		})
	}
}

// testUnauthorizedClientRejection tests that a client with valid cert but unauthorized CN is rejected.
func testUnauthorizedClientRejection(t *testing.T, addr, clientCertFile, clientKeyFile, caCertFile string) {
	// Load client certificate and key
	clientCert, err := tls.LoadX509KeyPair(clientCertFile, clientKeyFile)
	require.NoError(t, err)

	// Load CA certificate
	caCertBytes, err := ioutil.ReadFile(caCertFile)
	require.NoError(t, err)

	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(caCertBytes)

	// Create mTLS client configuration
	config := &tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      caCertPool,
		ServerName:   "localhost",
	}
	creds := credentials.NewTLS(config)

	// Connection should succeed at TLS level but fail at application level
	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(creds))
	require.NoError(t, err)
	defer conn.Close()

	// Try to use gRPC reflection - this should fail due to CN authorization
	client := grpc_reflection_v1alpha.NewServerReflectionClient(conn)
	stream, err := client.ServerReflectionInfo(context.Background())

	if err != nil {
		// Connection-level failure is acceptable for unauthorized clients
		return
	}

	// If stream creation succeeded, the RPC should fail
	err = stream.Send(&grpc_reflection_v1alpha.ServerReflectionRequest{
		MessageRequest: &grpc_reflection_v1alpha.ServerReflectionRequest_ListServices{},
	})

	// Either Send or Recv should fail for unauthorized client
	if err == nil {
		_, err = stream.Recv()
	}

	// We expect some kind of authorization error
	assert.Error(t, err, "Expected authorization error for unauthorized client CN")
}

// testInvalidClientConnection tests connection failures with invalid client certificates.
func testInvalidClientConnection(
	t *testing.T, addr, clientCertFile, clientKeyFile, caCertFile string, expectFail bool,
) {
	// Load CA certificate for server verification
	caCertBytes, err := ioutil.ReadFile(caCertFile)
	require.NoError(t, err)

	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(caCertBytes)

	config := &tls.Config{
		RootCAs:    caCertPool,
		ServerName: "localhost",
	}

	// Add client certificate if provided
	if clientCertFile != "" && clientKeyFile != "" {
		clientCert, err := tls.LoadX509KeyPair(clientCertFile, clientKeyFile)
		if err == nil { // Only add if cert can be loaded
			config.Certificates = []tls.Certificate{clientCert}
		}
	}

	creds := credentials.NewTLS(config)

	// Try to connect
	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(creds))

	if expectFail {
		if err != nil {
			// Connection failed as expected
			return
		}
		defer conn.Close()

		// If connection succeeded, the RPC should fail
		client := grpc_reflection_v1alpha.NewServerReflectionClient(conn)
		stream, err := client.ServerReflectionInfo(context.Background())

		if err != nil {
			// RPC failed as expected
			return
		}

		err = stream.Send(&grpc_reflection_v1alpha.ServerReflectionRequest{
			MessageRequest: &grpc_reflection_v1alpha.ServerReflectionRequest_ListServices{},
		})

		assert.Error(t, err, "Expected connection or RPC to fail with invalid client certificate")
	} else {
		require.NoError(t, err, "Expected connection to succeed")
		defer conn.Close()
	}
}
