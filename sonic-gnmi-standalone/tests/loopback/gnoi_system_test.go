package loopback

import (
	"crypto/md5"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	clientGnoi "github.com/sonic-net/sonic-gnmi/sonic-gnmi-standalone/pkg/client/gnoi"
)

// TestGNOISystemSetPackageLoopback tests the complete client-server loopback
// for the gNOI System SetPackage RPC with HTTP download functionality.
func TestGNOISystemSetPackageLoopback(t *testing.T) {
	// Create test package content
	testContent := []byte("test package content for gNOI SetPackage loopback test")
	testMD5 := fmt.Sprintf("%x", md5.Sum(testContent))

	// Setup test infrastructure
	tempDir := t.TempDir()
	packagePath := filepath.Join(tempDir, "test-package.bin")
	httpServer := SetupHTTPTestServer(testContent)
	defer httpServer.Close()

	testServer := SetupInsecureTestServer(t, tempDir, []string{"gnoi.system"})
	defer testServer.Stop()

	client := SetupGNOIClient(t, testServer.Addr, false)
	defer client.Close()

	// Test SetPackage RPC loopback
	ctx, cancel := WithTestTimeout(30 * time.Second)
	defer cancel()

	setPackageParams := &clientGnoi.SetPackageParams{
		URL:      httpServer.URL,
		Filename: packagePath,
		MD5:      testMD5,
		Version:  "1.0.0",
		Activate: false,
	}

	err := client.SetPackage(ctx, setPackageParams)
	require.NoError(t, err, "SetPackage RPC failed")

	// Verify package was downloaded and installed correctly
	installedContent, err := os.ReadFile(packagePath)
	require.NoError(t, err, "Failed to read installed package")

	assert.Equal(t, testContent, installedContent, "Package content mismatch")

	// Verify MD5 checksum
	actualMD5 := fmt.Sprintf("%x", md5.Sum(installedContent))
	assert.Equal(t, testMD5, actualMD5, "MD5 checksum mismatch")
}

// TestGNOISystemSetPackageLoopback_InvalidMD5 tests error handling for invalid MD5 checksums.
func TestGNOISystemSetPackageLoopback_InvalidMD5(t *testing.T) {
	testContent := []byte("test package content for MD5 validation")
	wrongMD5 := "00000000000000000000000000000000" // Intentionally wrong (32 chars)

	// Setup test infrastructure
	tempDir := t.TempDir()
	httpServer := SetupHTTPTestServer(testContent)
	defer httpServer.Close()

	testServer := SetupInsecureTestServer(t, tempDir, []string{"gnoi.system"})
	defer testServer.Stop()

	client := SetupGNOIClient(t, testServer.Addr, false)
	defer client.Close()

	// Test SetPackage with wrong MD5 - should fail
	ctx, cancel := WithTestTimeout(10 * time.Second)
	defer cancel()

	params := &clientGnoi.SetPackageParams{
		URL:      httpServer.URL,
		Filename: filepath.Join(tempDir, "test-invalid-md5.bin"),
		MD5:      wrongMD5,
	}

	err := client.SetPackage(ctx, params)
	assert.Error(t, err, "SetPackage should fail with invalid MD5")
	assert.Contains(t, err.Error(), "MD5 validation failed", "Error should mention MD5 validation failure")
}

// TestGNOISystemSetPackageLoopback_DownloadFailure tests error handling for download failures.
func TestGNOISystemSetPackageLoopback_DownloadFailure(t *testing.T) {
	// Setup test infrastructure
	tempDir := t.TempDir()
	testServer := SetupInsecureTestServer(t, tempDir, []string{"gnoi.system"})
	defer testServer.Stop()

	client := SetupGNOIClient(t, testServer.Addr, false)
	defer client.Close()

	// Test SetPackage with invalid URL - should fail
	ctx, cancel := WithTestTimeout(10 * time.Second)
	defer cancel()

	params := &clientGnoi.SetPackageParams{
		URL:      "http://invalid-url-that-does-not-exist.example.com/package.bin",
		Filename: filepath.Join(tempDir, "test-download-fail.bin"),
		MD5:      "d41d8cd98f00b204e9800998ecf8427e", // Empty file MD5
	}

	err := client.SetPackage(ctx, params)
	assert.Error(t, err, "SetPackage should fail with invalid URL")
	assert.Contains(t, err.Error(), "failed to download package", "Error should mention download failure")
}

// TestGNOISystemSetPackageLoopback_AbsolutePath tests package installation with absolute paths.
func TestGNOISystemSetPackageLoopback_AbsolutePath(t *testing.T) {
	testContent := []byte("test package for absolute path installation")
	testMD5 := fmt.Sprintf("%x", md5.Sum(testContent))

	// Setup test infrastructure
	tempDir := t.TempDir()
	httpServer := SetupHTTPTestServer(testContent)
	defer httpServer.Close()

	testServer := SetupInsecureTestServer(t, tempDir, []string{"gnoi.system"})
	defer testServer.Stop()

	client := SetupGNOIClient(t, testServer.Addr, false)
	defer client.Close()

	// Test with absolute path (should be prefixed with rootFS)
	ctx, cancel := WithTestTimeout(30 * time.Second)
	defer cancel()

	params := &clientGnoi.SetPackageParams{
		URL:      httpServer.URL,
		Filename: "/opt/packages/absolute-path-test.bin", // Absolute path
		MD5:      testMD5,
		Version:  "3.0.0",
	}

	err := client.SetPackage(ctx, params)
	require.NoError(t, err)

	// Verify package was installed at rootFS + absolute path
	expectedPath := filepath.Join(tempDir, "/opt/packages/absolute-path-test.bin")
	installedContent, err := os.ReadFile(expectedPath)
	require.NoError(t, err)
	assert.Equal(t, testContent, installedContent)
}
