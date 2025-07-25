package loopback

import (
	"context"
	"crypto/md5"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/client/config"
	clientGnoi "github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/client/gnoi"
	"github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/server"
	serverConfig "github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/server/config"
)

// TestGNOISystemSetPackageLoopback tests the complete client-server loopback
// for the gNOI System SetPackage RPC with HTTP download functionality.
func TestGNOISystemSetPackageLoopback(t *testing.T) {
	// Create test package content
	testContent := []byte("test package content for gNOI SetPackage loopback test")
	testMD5 := fmt.Sprintf("%x", md5.Sum(testContent))

	// Create HTTP test server to serve the package
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(testContent)
	}))
	defer httpServer.Close()

	// Create temporary directory for package installation
	tempDir := t.TempDir()
	packagePath := filepath.Join(tempDir, "test-package.bin")

	// Find an available port for gRPC server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	serverAddr := fmt.Sprintf("127.0.0.1:%d", port)

	// Initialize server configuration
	serverConfig.Global = &serverConfig.Config{
		Addr:        serverAddr,
		RootFS:      tempDir, // Use temp dir as root filesystem
		TLSEnabled:  false,
		TLSCertFile: "",
		TLSKeyFile:  "",
	}

	// Create and start gRPC server with gNOI System service
	srv, err := server.NewServerBuilder().
		EnableGNOISystem().
		Build()
	require.NoError(t, err, "Failed to create server")

	// Start server in background
	go func() {
		if err := srv.Start(); err != nil {
			t.Logf("Server error: %v", err)
		}
	}()

	// Give server time to start and bind to port
	time.Sleep(200 * time.Millisecond)

	// Verify server is listening
	conn, err := grpc.Dial(serverAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err, "Failed to connect to gRPC server")
	conn.Close()

	// Create gNOI client
	clientConfig := &config.Config{
		Address: serverAddr,
		TLS:     false,
	}

	client, err := clientGnoi.NewSystemClient(clientConfig)
	require.NoError(t, err, "Failed to create gNOI client")
	defer client.Close()

	// Test SetPackage RPC loopback
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	setPackageParams := &clientGnoi.SetPackageParams{
		URL:      httpServer.URL,
		Filename: packagePath,
		MD5:      testMD5,
		Version:  "1.0.0",
		Activate: false,
	}

	err = client.SetPackage(ctx, setPackageParams)
	require.NoError(t, err, "SetPackage RPC failed")

	// Verify package was downloaded and installed correctly
	installedContent, err := os.ReadFile(packagePath)
	require.NoError(t, err, "Failed to read installed package")

	assert.Equal(t, testContent, installedContent, "Package content mismatch")

	// Verify MD5 checksum
	actualMD5 := fmt.Sprintf("%x", md5.Sum(installedContent))
	assert.Equal(t, testMD5, actualMD5, "MD5 checksum mismatch")

	// Stop server
	srv.Stop()
}

// TestGNOISystemSetPackageLoopback_InvalidMD5 tests error handling for invalid MD5 checksums.
func TestGNOISystemSetPackageLoopback_InvalidMD5(t *testing.T) {
	testContent := []byte("test package content for MD5 validation")
	wrongMD5 := "00000000000000000000000000000000" // Intentionally wrong (32 chars)

	// Create HTTP test server
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(testContent)
	}))
	defer httpServer.Close()

	// Setup server
	tempDir := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	serverAddr := fmt.Sprintf("127.0.0.1:%d", port)

	serverConfig.Global = &serverConfig.Config{
		Addr:       serverAddr,
		RootFS:     tempDir,
		TLSEnabled: false,
	}

	srv, err := server.NewServerBuilder().
		EnableGNOISystem().
		Build()
	require.NoError(t, err)

	go srv.Start()
	time.Sleep(200 * time.Millisecond)

	// Create client
	clientConfig := &config.Config{
		Address: serverAddr,
		TLS:     false,
	}

	client, err := clientGnoi.NewSystemClient(clientConfig)
	require.NoError(t, err)
	defer client.Close()

	// Test SetPackage with wrong MD5 - should fail
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	params := &clientGnoi.SetPackageParams{
		URL:      httpServer.URL,
		Filename: filepath.Join(tempDir, "test-invalid-md5.bin"),
		MD5:      wrongMD5,
	}

	err = client.SetPackage(ctx, params)
	assert.Error(t, err, "SetPackage should fail with invalid MD5")
	assert.Contains(t, err.Error(), "MD5 validation failed", "Error should mention MD5 validation failure")

	srv.Stop()
}

// TestGNOISystemSetPackageLoopback_DownloadFailure tests error handling for download failures.
func TestGNOISystemSetPackageLoopback_DownloadFailure(t *testing.T) {
	// Setup server
	tempDir := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	serverAddr := fmt.Sprintf("127.0.0.1:%d", port)

	serverConfig.Global = &serverConfig.Config{
		Addr:       serverAddr,
		RootFS:     tempDir,
		TLSEnabled: false,
	}

	srv, err := server.NewServerBuilder().
		EnableGNOISystem().
		Build()
	require.NoError(t, err)

	go srv.Start()
	time.Sleep(200 * time.Millisecond)

	// Create client
	clientConfig := &config.Config{
		Address: serverAddr,
		TLS:     false,
	}

	client, err := clientGnoi.NewSystemClient(clientConfig)
	require.NoError(t, err)
	defer client.Close()

	// Test SetPackage with invalid URL - should fail
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	params := &clientGnoi.SetPackageParams{
		URL:      "http://invalid-url-that-does-not-exist.example.com/package.bin",
		Filename: filepath.Join(tempDir, "test-download-fail.bin"),
		MD5:      "d41d8cd98f00b204e9800998ecf8427e", // Empty file MD5
	}

	err = client.SetPackage(ctx, params)
	assert.Error(t, err, "SetPackage should fail with invalid URL")
	assert.Contains(t, err.Error(), "failed to download package", "Error should mention download failure")

	srv.Stop()
}

// TestGNOISystemSetPackageLoopback_AbsolutePath tests package installation with absolute paths.
func TestGNOISystemSetPackageLoopback_AbsolutePath(t *testing.T) {
	testContent := []byte("test package for absolute path installation")
	testMD5 := fmt.Sprintf("%x", md5.Sum(testContent))

	// HTTP server
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(testContent)
	}))
	defer httpServer.Close()

	// Setup server
	tempDir := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	serverAddr := fmt.Sprintf("127.0.0.1:%d", port)

	serverConfig.Global = &serverConfig.Config{
		Addr:       serverAddr,
		RootFS:     tempDir,
		TLSEnabled: false,
	}

	srv, err := server.NewServerBuilder().
		EnableGNOISystem().
		Build()
	require.NoError(t, err)

	go srv.Start()
	time.Sleep(200 * time.Millisecond)

	// Create client
	clientConfig := &config.Config{
		Address: serverAddr,
		TLS:     false,
	}

	client, err := clientGnoi.NewSystemClient(clientConfig)
	require.NoError(t, err)
	defer client.Close()

	// Test with absolute path (should be prefixed with rootFS)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	params := &clientGnoi.SetPackageParams{
		URL:      httpServer.URL,
		Filename: "/opt/packages/absolute-path-test.bin", // Absolute path
		MD5:      testMD5,
		Version:  "3.0.0",
	}

	err = client.SetPackage(ctx, params)
	require.NoError(t, err)

	// Verify package was installed at rootFS + absolute path
	expectedPath := filepath.Join(tempDir, "/opt/packages/absolute-path-test.bin")
	installedContent, err := os.ReadFile(expectedPath)
	require.NoError(t, err)
	assert.Equal(t, testContent, installedContent)

	srv.Stop()
}
