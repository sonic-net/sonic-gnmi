package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/config"
	pb "github.com/sonic-net/sonic-gnmi/upgrade-service/proto"
)

func TestNewSystemInfoServer(t *testing.T) {
	server := NewSystemInfoServer()
	if server == nil {
		t.Error("Expected non-nil server")
	}

	// Verify it implements the interface
	var _ pb.SystemInfoServer = server
}

type platformTestCase struct {
	name                       string
	machineConfContent         string
	expectedPlatformIdentifier string
	expectedVendor             string
	expectedModel              string
	expectError                bool
}

func runPlatformTest(t *testing.T, test platformTestCase) {
	// Set up temporary directory and machine.conf
	tempDir := t.TempDir()
	machineConfPath := filepath.Join(tempDir, "host", "machine.conf")

	// Create directory
	err := os.MkdirAll(filepath.Dir(machineConfPath), 0755)
	if err != nil {
		t.Fatalf("Failed to create directory: %v", err)
	}

	// Write machine.conf
	err = os.WriteFile(machineConfPath, []byte(test.machineConfContent), 0644)
	if err != nil {
		t.Fatalf("Failed to write machine.conf: %v", err)
	}

	// Mock config
	originalConfig := config.Global
	config.Global = &config.Config{RootFS: tempDir}
	defer func() { config.Global = originalConfig }()

	// Create server and call method
	server := NewSystemInfoServer()
	ctx := context.Background()
	resp, err := server.GetPlatformType(ctx, &pb.GetPlatformTypeRequest{})

	// Validate results
	if test.expectError {
		if err == nil {
			t.Error("Expected an error but got nil")
		}
		return
	}

	if err != nil {
		t.Errorf("Did not expect an error but got: %v", err)
		return
	}

	if resp == nil {
		t.Error("Expected a response but got nil")
		return
	}

	if resp.PlatformIdentifier != test.expectedPlatformIdentifier {
		t.Errorf("Expected platform identifier %v but got %v",
			test.expectedPlatformIdentifier, resp.PlatformIdentifier)
	}
	if resp.Vendor != test.expectedVendor {
		t.Errorf("Expected vendor %v but got %v",
			test.expectedVendor, resp.Vendor)
	}
	if resp.Model != test.expectedModel {
		t.Errorf("Expected model %v but got %v",
			test.expectedModel, resp.Model)
	}
}

func TestSystemInfoServer_GetPlatformType_Mellanox(t *testing.T) {
	test := platformTestCase{
		name: "Success - Mellanox SN4600",
		machineConfContent: `onie_platform=x86_64-mlnx_msn4600c-r0
onie_machine=mlnx_msn4600c
onie_arch=x86_64
onie_switch_asic=mlnx`,
		expectedPlatformIdentifier: "mellanox_sn4600",
		expectedVendor:             "Mellanox",
		expectedModel:              "sn4600",
		expectError:                false,
	}
	runPlatformTest(t, test)
}

func TestSystemInfoServer_GetPlatformType_Arista(t *testing.T) {
	test := platformTestCase{
		name: "Success - Arista 7060",
		machineConfContent: `aboot_vendor=arista
aboot_platform=x86_64-arista_7060x6_64pe
aboot_machine=arista_7060x6_64pe
aboot_arch=x86_64`,
		expectedPlatformIdentifier: "arista_7060",
		expectedVendor:             "arista",
		expectedModel:              "7060",
		expectError:                false,
	}
	runPlatformTest(t, test)
}

func TestSystemInfoServer_GetPlatformType_Dell(t *testing.T) {
	test := platformTestCase{
		name: "Success - Dell S6100",
		machineConfContent: `onie_platform=x86_64-dell_s6100-r0
onie_machine=dell_s6100
onie_arch=x86_64
onie_switch_asic=broadcom`,
		expectedPlatformIdentifier: "dell_s6100",
		expectedVendor:             "dell",
		expectedModel:              "s6100",
		expectError:                false,
	}
	runPlatformTest(t, test)
}

func TestSystemInfoServer_GetPlatformType_KVM(t *testing.T) {
	test := platformTestCase{
		name: "Success - KVM Platform",
		machineConfContent: `onie_platform=x86_64-kvm_x86_64-r0
onie_machine=kvm_x86_64
onie_arch=x86_64
onie_switch_asic=qemu`,
		expectedPlatformIdentifier: "x86_64-kvm_x86_64-r0",
		expectedVendor:             "kvm",
		expectedModel:              "unknown",
		expectError:                false,
	}
	runPlatformTest(t, test)
}

func TestSystemInfoServer_GetPlatformType_FileNotFound(t *testing.T) {
	// Mock config with non-existent path
	originalConfig := config.Global
	config.Global = &config.Config{RootFS: "/non-existent-path"}
	defer func() { config.Global = originalConfig }()

	server := NewSystemInfoServer()
	ctx := context.Background()
	resp, err := server.GetPlatformType(ctx, &pb.GetPlatformTypeRequest{})

	if err == nil {
		t.Error("Expected error for non-existent file, got nil")
	}
	if resp != nil {
		t.Error("Expected nil response for error case")
	}
}

func TestSystemInfoServer_GetPlatformType_ContextCancellation(t *testing.T) {
	server := NewSystemInfoServer()

	// Create cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	resp, err := server.GetPlatformType(ctx, &pb.GetPlatformTypeRequest{})

	if err == nil {
		t.Error("Expected error for cancelled context, got nil")
	}
	if resp != nil {
		t.Error("Expected nil response for cancelled context")
	}
}

func TestSystemInfoServer_GetDiskSpace_DefaultPaths(t *testing.T) {
	// Mock config with temp directory
	tempDir := t.TempDir()
	originalConfig := config.Global
	config.Global = &config.Config{RootFS: tempDir}
	defer func() { config.Global = originalConfig }()

	// Create some of the expected directories in tempDir
	hostDir := filepath.Join(tempDir, "host")
	tmpDir := filepath.Join(tempDir, "tmp")
	err := os.MkdirAll(hostDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create host directory: %v", err)
	}
	err = os.MkdirAll(tmpDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create tmp directory: %v", err)
	}

	server := NewSystemInfoServer()
	ctx := context.Background()
	resp, err := server.GetDiskSpace(ctx, &pb.GetDiskSpaceRequest{})

	if err != nil {
		t.Errorf("Did not expect an error but got: %v", err)
		return
	}

	if resp == nil {
		t.Error("Expected a response but got nil")
		return
	}

	// Should have results for 3 default paths
	if len(resp.Filesystems) != 3 {
		t.Errorf("Expected 3 filesystems, got %d", len(resp.Filesystems))
	}

	// Check that we have entries for each expected path
	pathsSeen := make(map[string]bool)
	for _, fs := range resp.Filesystems {
		pathsSeen[fs.Path] = true

		// If no error, should have valid disk space values
		if fs.ErrorMessage == "" {
			if fs.TotalMb <= 0 {
				t.Errorf("Expected positive total space for %s, got %d", fs.Path, fs.TotalMb)
			}
			if fs.FreeMb < 0 {
				t.Errorf("Expected non-negative free space for %s, got %d", fs.Path, fs.FreeMb)
			}
			if fs.UsedMb < 0 {
				t.Errorf("Expected non-negative used space for %s, got %d", fs.Path, fs.UsedMb)
			}
			// Used + Free should equal Total
			if fs.UsedMb+fs.FreeMb != fs.TotalMb {
				t.Errorf("Used + Free != Total for %s: %d + %d != %d",
					fs.Path, fs.UsedMb, fs.FreeMb, fs.TotalMb)
			}
		}
	}

	expectedPaths := []string{"/", "/host", "/tmp"}
	for _, expectedPath := range expectedPaths {
		if !pathsSeen[expectedPath] {
			t.Errorf("Expected to see path %s in response", expectedPath)
		}
	}
}

func TestSystemInfoServer_GetDiskSpace_CustomPaths(t *testing.T) {
	// Mock config with temp directory
	tempDir := t.TempDir()
	originalConfig := config.Global
	config.Global = &config.Config{RootFS: tempDir}
	defer func() { config.Global = originalConfig }()

	// Create a custom test directory
	customDir := filepath.Join(tempDir, "custom")
	err := os.MkdirAll(customDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create custom directory: %v", err)
	}

	server := NewSystemInfoServer()
	ctx := context.Background()

	// Request specific paths
	req := &pb.GetDiskSpaceRequest{
		Paths: []string{"/custom"},
	}
	resp, err := server.GetDiskSpace(ctx, req)

	if err != nil {
		t.Errorf("Did not expect an error but got: %v", err)
		return
	}

	if resp == nil {
		t.Error("Expected a response but got nil")
		return
	}

	// Should have result for 1 custom path
	if len(resp.Filesystems) != 1 {
		t.Errorf("Expected 1 filesystem, got %d", len(resp.Filesystems))
	}

	fs := resp.Filesystems[0]
	if fs.Path != "/custom" {
		t.Errorf("Expected path /custom, got %s", fs.Path)
	}

	// Should be successful since we created the directory
	if fs.ErrorMessage != "" {
		t.Errorf("Expected no error for existing path, got: %s", fs.ErrorMessage)
	}

	if fs.TotalMb <= 0 {
		t.Errorf("Expected positive total space, got %d", fs.TotalMb)
	}
}

func TestSystemInfoServer_GetDiskSpace_NonexistentPath(t *testing.T) {
	// Mock config with temp directory
	tempDir := t.TempDir()
	originalConfig := config.Global
	config.Global = &config.Config{RootFS: tempDir}
	defer func() { config.Global = originalConfig }()

	server := NewSystemInfoServer()
	ctx := context.Background()

	// Request a non-existent path
	req := &pb.GetDiskSpaceRequest{
		Paths: []string{"/nonexistent"},
	}
	resp, err := server.GetDiskSpace(ctx, req)

	if err != nil {
		t.Errorf("Did not expect an error but got: %v", err)
		return
	}

	if resp == nil {
		t.Error("Expected a response but got nil")
		return
	}

	// Should have result for 1 path with error
	if len(resp.Filesystems) != 1 {
		t.Errorf("Expected 1 filesystem, got %d", len(resp.Filesystems))
	}

	fs := resp.Filesystems[0]
	if fs.Path != "/nonexistent" {
		t.Errorf("Expected path /nonexistent, got %s", fs.Path)
	}

	// Should have an error message
	if fs.ErrorMessage == "" {
		t.Error("Expected error message for nonexistent path")
	}

	// Disk space values should be zero when there's an error
	if fs.TotalMb != 0 || fs.FreeMb != 0 || fs.UsedMb != 0 {
		t.Errorf("Expected zero disk space values for error case, got total=%d, free=%d, used=%d",
			fs.TotalMb, fs.FreeMb, fs.UsedMb)
	}
}

func TestSystemInfoServer_GetDiskSpace_ContextCancellation(t *testing.T) {
	server := NewSystemInfoServer()

	// Create cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	resp, err := server.GetDiskSpace(ctx, &pb.GetDiskSpaceRequest{})

	if err == nil {
		t.Error("Expected error for cancelled context, got nil")
	}
	if resp != nil {
		t.Error("Expected nil response for cancelled context")
	}
}
