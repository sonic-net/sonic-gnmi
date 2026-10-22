package client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Namespace -> file mapping tests (WS4) against WS2's recordsLiveFileName /
// discoverRotated and WS1's recordsNamespaceAllowed, using the fixtures under
// testdata/records/multi_asic/ and testdata/records/scenario/. No multi-ASIC
// device exists this week, so this is the only place the asicN branch is
// proven.

const (
	recordsScenarioDir  = "../testdata/records/scenario"
	recordsMultiAsicDir = "../testdata/records/multi_asic"
)

// recFilesOldestFirst resolves one namespace/source pair under dir the way a
// replay walks it: rotated files oldest first, then the live file (which may
// not exist).
func recFilesOldestFirst(t *testing.T, dir, ns, src string) (live string, rotated []rotatedFile) {
	t.Helper()
	ft, err := NewFileTailer(dir, ns, src)
	if err != nil {
		t.Fatalf("NewFileTailer(%s,%s,%s): %v", dir, ns, src, err)
	}
	rotated, err = discoverRotated(dir, filepath.Base(ft.LivePath()))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("discoverRotated: %v", err)
	}
	return ft.LivePath(), rotated
}

func TestRecordsNamespaceAllowed(t *testing.T) {
	multi := []string{"", "asic0", "asic1"} // what sdcfg reports on a 2-ASIC box
	single := []string{""}

	if !recordsNamespaceAllowed("localhost", single) {
		t.Error("localhost on single-ASIC box rejected")
	}
	if !recordsNamespaceAllowed("localhost", multi) {
		t.Error("localhost on multi-ASIC box rejected")
	}
	if !recordsNamespaceAllowed("asic1", multi) {
		t.Error("asic1 on multi-ASIC box rejected")
	}
	if recordsNamespaceAllowed("asic0", single) {
		t.Error("asic0 on single-ASIC box accepted")
	}
	if recordsNamespaceAllowed("asic7", multi) {
		t.Error("asic7 on 2-ASIC box accepted")
	}
	// The message must help the operator: spell the default as localhost.
	if got := formatNamespaceList(multi); got != "localhost, asic0, asic1" {
		t.Errorf("formatNamespaceList = %q", got)
	}
}

// Each asicN namespace must read only its own pair of files. The fixture
// files carry namespace-specific prefixes so leakage is detectable.
func TestRecordsFilesMultiAsic(t *testing.T) {
	marker := map[string]string{"asic0": "10.0.0.0/24", "asic1": "10.1.1.0/24"}
	for ns, own := range marker {
		for _, src := range []string{RecordsSourceSwss, RecordsSourceSairedis} {
			live, rotated := recFilesOldestFirst(t, recordsMultiAsicDir, ns, src)
			wantName := src + "." + ns + ".rec"
			if filepath.Base(live) != wantName {
				t.Errorf("%s/%s live = %s, want %s", ns, src, filepath.Base(live), wantName)
			}
			if len(rotated) != 0 {
				t.Errorf("%s/%s rotated = %v, want none", ns, src, rotated)
			}
			data, err := os.ReadFile(live)
			if err != nil {
				t.Fatalf("read %s: %v", live, err)
			}
			if !strings.Contains(string(data), own) {
				t.Errorf("%s missing its own prefix %s", live, own)
			}
			for other, foreign := range marker {
				if other != ns && strings.Contains(string(data), foreign) {
					t.Errorf("%s contains %s's prefix %s: namespaces leak", live, other, foreign)
				}
			}
		}
	}
}

// localhost maps to the plain names, and there are no plain-named files in
// the multi-ASIC fixture, so a localhost subscription there finds nothing.
func TestRecordsFilesLocalhostOnMultiAsicDir(t *testing.T) {
	live, rotated := recFilesOldestFirst(t, recordsMultiAsicDir, "localhost", RecordsSourceSwss)
	if filepath.Base(live) != "swss.rec" || len(rotated) != 0 {
		t.Errorf("got live=%s rotated=%v", live, rotated)
	}
	if _, err := os.Stat(live); !os.IsNotExist(err) {
		t.Errorf("expected %s to be absent in multi_asic fixture, stat err=%v", live, err)
	}
}

// Rotated files come back oldest first so replay can read them in order.
func TestRecordsFilesScenarioRotationOrder(t *testing.T) {
	for _, src := range []string{RecordsSourceSwss, RecordsSourceSairedis} {
		live, rotated := recFilesOldestFirst(t, recordsScenarioDir, "localhost", src)
		if filepath.Base(live) != src+".rec" {
			t.Errorf("live = %s", live)
		}
		var names []string
		for _, r := range rotated {
			names = append(names, filepath.Base(r.path))
		}
		want := []string{src + ".rec.2.gz", src + ".rec.1"}
		if strings.Join(names, ",") != strings.Join(want, ",") {
			t.Errorf("rotated = %v, want %v", names, want)
		}
		if !rotated[0].gz || rotated[1].gz {
			t.Errorf("gzip flags wrong for %v", names)
		}
		if rotated[0].index != 2 || rotated[1].index != 1 {
			t.Errorf("indexes wrong: %d,%d", rotated[0].index, rotated[1].index)
		}
	}
}

// Files that are not logrotate products (samples, backups) are never replayed,
// and a base.N next to base.N.gz (mid-compress) yields the uncompressed one.
func TestDiscoverRotatedIgnoresNonRotatedSiblings(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"swss.rec", "swss.rec.1", "swss.rec.3.gz", "swss.rec.2.gz", "swss.rec.2",
		"swss.rec.sample", "swss.rec.bak", "swss.rec.0", "swss.recording", "sairedis.rec.1"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "swss.rec.4"), 0o755); err != nil {
		t.Fatal(err)
	}
	rotated, err := discoverRotated(dir, "swss.rec")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range rotated {
		names = append(names, filepath.Base(r.path))
	}
	if got := strings.Join(names, ","); got != "swss.rec.3.gz,swss.rec.2,swss.rec.1" {
		t.Errorf("rotated = %s", got)
	}
}

// A missing directory is not fatal at construction: orchagent may not have
// started yet and Run retries.
func TestNewFileTailerMissingDir(t *testing.T) {
	ft, err := NewFileTailer(filepath.Join(t.TempDir(), "nope"), "asic0", RecordsSourceSairedis)
	if err != nil {
		t.Fatalf("missing dir: %v", err)
	}
	if filepath.Base(ft.LivePath()) != "sairedis.asic0.rec" {
		t.Errorf("live=%s", ft.LivePath())
	}
}
