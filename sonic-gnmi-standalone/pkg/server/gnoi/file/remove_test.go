package file

import (
	"context"
	"os"
	"testing"

	"github.com/openconfig/gnoi/file"
)

// TestFileServer_Remove tests the Remove method of FileServer.
func TestFileServer_Remove(t *testing.T) {
	srv := &FileServer{}

	// Create a temporary file to be removed.
	tmp := "test_remove.tmp"
	if err := os.WriteFile(tmp, []byte("hello"), 0644); err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}

	// Call Remove.
	resp, err := srv.Remove(context.Background(), &file.RemoveRequest{
		RemoteFile: tmp,
	})
	if err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
	if resp == nil {
		t.Fatalf("Remove returned nil response")
	}

	// Check that the file was removed.
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("file still exists after Remove")
	}
}

// TestFileServer_Remove_EmptyPath tests Remove with an empty path.
func TestFileServer_Remove_EmptyPath(t *testing.T) {
	srv := &FileServer{}
	_, err := srv.Remove(context.Background(), &file.RemoveRequest{
		RemoteFile: "",
	})
	if err == nil {
		t.Fatalf("expected error for empty path, got nil")
	}
}

// TestFileServer_Remove_NonexistentFile tests Remove with a non-existent file.
func TestFileServer_Remove_NonexistentFile(t *testing.T) {
	srv := &FileServer{}
	_, err := srv.Remove(context.Background(), &file.RemoveRequest{
		RemoteFile: "nonexistent_file.tmp",
	})
	if err == nil {
		t.Fatalf("expected error for nonexistent file, got nil")
	}
}
