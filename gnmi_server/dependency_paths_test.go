package gnmi

import (
	"path/filepath"
	"testing"
)

func TestDbJournalPath(t *testing.T) {
	original := hostVarLogPath
	t.Cleanup(func() { hostVarLogPath = original })

	hostVarLogPath = HostVarLogPath
	if got := dbJournalPath("CONFIG_DB"); got != "/var/log/config_db.txt" {
		t.Fatalf("dbJournalPath() = %q, want /var/log/config_db.txt", got)
	}

	hostVarLogPath = t.TempDir()
	want := filepath.Join(hostVarLogPath, "config_db.txt")
	if got := dbJournalPath("CONFIG_DB"); got != want {
		t.Fatalf("dbJournalPath() = %q, want %q", got, want)
	}
}

func TestHealthzArtifactPath(t *testing.T) {
	original := healthzHostRoot
	t.Cleanup(func() { healthzHostRoot = original })

	healthzHostRoot = healthzDefaultHostRoot
	if got := healthzArtifactPath("/tmp/dump/debug.tar.gz"); got != "/mnt/host/tmp/dump/debug.tar.gz" {
		t.Fatalf("healthzArtifactPath() = %q, want /mnt/host/tmp/dump/debug.tar.gz", got)
	}

	healthzHostRoot = t.TempDir()
	want := filepath.Join(healthzHostRoot, "tmp/dump/debug.tar.gz")
	if got := healthzArtifactPath("/tmp/dump/debug.tar.gz"); got != want {
		t.Fatalf("healthzArtifactPath() = %q, want %q", got, want)
	}
}
