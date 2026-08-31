package authzpolicy

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileSourceLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.json")
	policy := `{"name":"test","allow_rules":[{"name":"allow-all"}]}`
	if err := os.WriteFile(path, []byte(policy), 0600); err != nil {
		t.Fatal(err)
	}

	interceptor, err := (FileSource{Path: path, RefreshInterval: time.Hour}).Load(context.Background())
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	interceptor.Close()
	interceptor.Close()
}

func TestFileSourceRejectsInvalidInitialPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(path, []byte(`{"name":"invalid"}`), 0600); err != nil {
		t.Fatal(err)
	}

	if _, err := (FileSource{Path: path, RefreshInterval: time.Hour}).Load(context.Background()); err == nil {
		t.Fatal("Load() succeeded with an invalid policy")
	}
}
