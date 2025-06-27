package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/config"
	pb "github.com/sonic-net/sonic-gnmi/upgrade-service/proto"
)

func TestNewFirmwareManagementServer(t *testing.T) {
	server := NewFirmwareManagementServer()
	if server == nil {
		t.Error("Expected non-nil server")
	}
	if _, ok := server.(*firmwareManagementServer); !ok {
		t.Error("Expected server to be of type *firmwareManagementServer")
	}
}

func TestFirmwareManagementServer_CleanupOldFirmware(t *testing.T) {
	// Set up config to avoid nil pointer
	tempDir := t.TempDir()
	originalConfig := config.Global
	config.Global = &config.Config{RootFS: tempDir}
	defer func() { config.Global = originalConfig }()

	// Create host directory
	hostDir := filepath.Join(tempDir, "host")
	if err := os.MkdirAll(hostDir, 0755); err != nil {
		t.Fatalf("Failed to create host directory: %v", err)
	}

	server := NewFirmwareManagementServer()
	ctx := context.Background()
	req := &pb.CleanupOldFirmwareRequest{}

	resp, err := server.CleanupOldFirmware(ctx, req)
	if err != nil {
		t.Errorf("CleanupOldFirmware returned unexpected error: %v", err)
	}

	if resp == nil {
		t.Error("Expected non-nil response")
	}

	// The actual cleanup is done by the firmware package which is already well tested
	// Here we just verify the server properly returns the response
	if resp.FilesDeleted < 0 {
		t.Error("Expected non-negative FilesDeleted")
	}
	if resp.SpaceFreedBytes < 0 {
		t.Error("Expected non-negative SpaceFreedBytes")
	}
	if resp.DeletedFiles == nil {
		t.Error("Expected non-nil DeletedFiles slice")
	}
	if resp.Errors == nil {
		t.Error("Expected non-nil Errors slice")
	}
}
