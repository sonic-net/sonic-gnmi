package gnmi

import (
	"os"
	"path/filepath"
	"strings"
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

func TestDbJournalRotationUsesFileDirectory(t *testing.T) {
	original := hostVarLogPath
	t.Cleanup(func() { hostVarLogPath = original })

	journalDir := t.TempDir()
	fileName := filepath.Join(journalDir, "config_db.txt")
	file, err := os.OpenFile(fileName, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		t.Fatal(err)
	}
	dbj := &DbJournal{database: "CONFIG_DB", file: file, fileName: fileName}
	t.Cleanup(func() {
		if dbj.file != nil {
			dbj.file.Close()
		}
	})
	if err := file.Truncate(maxFileSize); err != nil {
		t.Fatal(err)
	}
	oldBackup := filepath.Join(journalDir, "config_db_20000101000000.gz")
	if err := os.WriteFile(oldBackup, nil, 0644); err != nil {
		t.Fatal(err)
	}

	otherDir := t.TempDir()
	otherBackup := filepath.Join(otherDir, "config_db_20000101000000.gz")
	if err := os.WriteFile(otherBackup, nil, 0644); err != nil {
		t.Fatal(err)
	}
	hostVarLogPath = otherDir
	if err := dbj.rotateFile(); err != nil {
		t.Fatalf("rotateFile() error = %v", err)
	}

	files, err := os.ReadDir(journalDir)
	if err != nil {
		t.Fatal(err)
	}
	foundBackup := false
	for _, file := range files {
		if strings.HasPrefix(file.Name(), "config_db_") && strings.HasSuffix(file.Name(), ".gz") {
			foundBackup = true
		}
	}
	if !foundBackup {
		t.Fatal("rotateFile() did not create a backup beside the journal file")
	}
	if _, err := os.Stat(oldBackup); !os.IsNotExist(err) {
		t.Fatalf("rotateFile() did not remove the oldest journal backup: %v", err)
	}
	if _, err := os.Stat(otherBackup); err != nil {
		t.Fatalf("rotateFile() removed a backup from current hostVarLogPath: %v", err)
	}
}

func TestNewServerRejectsInvalidSharedMemoryKey(t *testing.T) {
	t.Setenv("SONIC_GNMI_SHM_KEY", "invalid")
	server, err := NewServer(&Config{}, nil, nil)
	if err == nil {
		if server != nil {
			server.ForceStop()
		}
		t.Fatal("NewServer() accepted an invalid shared-memory key")
	}
	if !strings.Contains(err.Error(), "invalid SONIC_GNMI_SHM_KEY") {
		t.Fatalf("NewServer() error = %v, want invalid shared-memory key error", err)
	}
}
