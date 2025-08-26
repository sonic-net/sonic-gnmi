package loopback

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
)

func TestServerConnectionModes(t *testing.T) {
	t.Run("InsecureConnection", testInsecureConnection)
	t.Run("TLSConnection", testTLSConnection)
	t.Run("MTLSConnection", testMTLSConnection)
}

func testInsecureConnection(t *testing.T) {
	// Setup test server
	tempDir := t.TempDir()
	testServer := SetupInsecureTestServer(t, tempDir, []string{})
	defer testServer.Stop()

	// Create insecure client connection
	conn, err := grpc.Dial(testServer.Addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()

	// Test gRPC reflection service
	client := grpc_reflection_v1alpha.NewServerReflectionClient(conn)
	stream, err := client.ServerReflectionInfo(context.Background())
	require.NoError(t, err)

	// Send a list services request
	err = stream.Send(&grpc_reflection_v1alpha.ServerReflectionRequest{
		MessageRequest: &grpc_reflection_v1alpha.ServerReflectionRequest_ListServices{},
	})
	require.NoError(t, err)

	// Receive response
	resp, err := stream.Recv()
	require.NoError(t, err)

	// Verify we get a list of services response
	services := resp.GetListServicesResponse()
	assert.NotNil(t, services)
	assert.Greater(t, len(services.Service), 0)
}

func testTLSConnection(t *testing.T) {
	// Setup test certificates and server
	tempDir := t.TempDir()
	certDir := filepath.Join(tempDir, "certs")
	err := os.MkdirAll(certDir, 0o755)
	require.NoError(t, err)

	serverCertFile, serverKeyFile := generateTestCertificates(t, certDir, false)
	testServer := SetupTLSTestServer(t, tempDir, serverCertFile, serverKeyFile, []string{})
	defer testServer.Stop()

	// Create TLS client connection with insecure credentials (for self-signed cert)
	config := &tls.Config{
		InsecureSkipVerify: true,
	}
	creds := credentials.NewTLS(config)

	conn, err := grpc.Dial(testServer.Addr, grpc.WithTransportCredentials(creds))
	require.NoError(t, err)
	defer conn.Close()

	// Test gRPC reflection service
	client := grpc_reflection_v1alpha.NewServerReflectionClient(conn)
	stream, err := client.ServerReflectionInfo(context.Background())
	require.NoError(t, err)

	// Send a list services request
	err = stream.Send(&grpc_reflection_v1alpha.ServerReflectionRequest{
		MessageRequest: &grpc_reflection_v1alpha.ServerReflectionRequest_ListServices{},
	})
	require.NoError(t, err)

	// Receive response
	resp, err := stream.Recv()
	require.NoError(t, err)

	// Verify we get a list of services response
	services := resp.GetListServicesResponse()
	assert.NotNil(t, services)
	assert.Greater(t, len(services.Service), 0)
}

func testMTLSConnection(t *testing.T) {
	// Setup test certificates and server
	tempDir := t.TempDir()
	certDir := filepath.Join(tempDir, "certs")
	err := os.MkdirAll(certDir, 0o755)
	require.NoError(t, err)

	serverCertFile, serverKeyFile, clientCertFile, clientKeyFile, caCertFile := generateMTLSCertificates(t, certDir)
	testServer := SetupMTLSTestServer(t, tempDir, serverCertFile, serverKeyFile, caCertFile, []string{})
	defer testServer.Stop()

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
		ServerName:   "localhost", // Must match certificate
	}
	creds := credentials.NewTLS(config)

	conn, err := grpc.Dial(testServer.Addr, grpc.WithTransportCredentials(creds))
	require.NoError(t, err)
	defer conn.Close()

	// Test gRPC reflection service
	client := grpc_reflection_v1alpha.NewServerReflectionClient(conn)
	stream, err := client.ServerReflectionInfo(context.Background())
	require.NoError(t, err)

	// Send a list services request
	err = stream.Send(&grpc_reflection_v1alpha.ServerReflectionRequest{
		MessageRequest: &grpc_reflection_v1alpha.ServerReflectionRequest_ListServices{},
	})
	require.NoError(t, err)

	// Receive response
	resp, err := stream.Recv()
	require.NoError(t, err)

	// Verify we get a list of services response
	services := resp.GetListServicesResponse()
	assert.NotNil(t, services)
	assert.Greater(t, len(services.Service), 0)
}
