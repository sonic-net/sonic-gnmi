package file

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	gnoi_file_pb "github.com/openconfig/gnoi/file"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// skipIfMntHost skips the test when /mnt/host exists on the host running the
// tests. translatePathForContainer prepends /mnt/host in that case, which
// breaks tests that point HandleStat at a t.TempDir() under /tmp because the
// real path lives at /tmp/..., not /mnt/host/tmp/....
func skipIfMntHost(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("/mnt/host"); err == nil {
		t.Skip("/mnt/host exists; HandleStat path translation makes tempdirs unreachable")
	}
}

func TestHandleStat_NilRequest(t *testing.T) {
	_, err := HandleStat(context.Background(), nil)
	if err == nil || status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
}

func TestHandleStat_EmptyPath(t *testing.T) {
	_, err := HandleStat(context.Background(), &gnoi_file_pb.StatRequest{Path: ""})
	if err == nil || status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
}

func TestHandleStat_RelativePath(t *testing.T) {
	_, err := HandleStat(context.Background(), &gnoi_file_pb.StatRequest{Path: "relative/foo"})
	if err == nil || status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
}

func TestHandleStat_NotFound(t *testing.T) {
	skipIfMntHost(t)
	_, err := HandleStat(context.Background(), &gnoi_file_pb.StatRequest{
		Path: "/tmp/definitely-does-not-exist-zzzz-9876543",
	})
	if err == nil || status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound, got %v", err)
	}
}

func TestHandleStat_RegularFile(t *testing.T) {
	skipIfMntHost(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "f.bin")
	if err := os.WriteFile(path, []byte("hello world"), 0640); err != nil {
		t.Fatal(err)
	}

	resp, err := HandleStat(context.Background(), &gnoi_file_pb.StatRequest{Path: path})
	if err != nil {
		t.Fatalf("HandleStat: %v", err)
	}
	if len(resp.Stats) != 1 {
		t.Fatalf("got %d entries, want 1", len(resp.Stats))
	}
	got := resp.Stats[0]
	if got.Path != path {
		t.Errorf("Path = %q, want %q", got.Path, path)
	}
	if got.Size != 11 {
		t.Errorf("Size = %d, want 11", got.Size)
	}
	// Permissions: 0640 → octal-as-decimal = 640.
	if got.Permissions != 640 {
		t.Errorf("Permissions = %d, want 640", got.Permissions)
	}
	if got.LastModified == 0 {
		t.Error("LastModified must be non-zero")
	}
	if got.Umask != defaultUmask {
		t.Errorf("Umask = %d, want %d", got.Umask, defaultUmask)
	}
}

func TestHandleStat_Directory(t *testing.T) {
	skipIfMntHost(t)
	dir := t.TempDir()
	names := []string{"alpha.txt", "beta.txt", "gamma.txt"}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	// Add a subdirectory to make sure directories are reported too.
	subdir := filepath.Join(dir, "subdir")
	if err := os.Mkdir(subdir, 0755); err != nil {
		t.Fatal(err)
	}

	resp, err := HandleStat(context.Background(), &gnoi_file_pb.StatRequest{Path: dir})
	if err != nil {
		t.Fatalf("HandleStat: %v", err)
	}
	if len(resp.Stats) != len(names)+1 {
		t.Fatalf("got %d entries, want %d", len(resp.Stats), len(names)+1)
	}

	seen := map[string]*gnoi_file_pb.StatInfo{}
	for _, s := range resp.Stats {
		if filepath.Dir(s.Path) != dir {
			t.Errorf("entry %q not under dir %q", s.Path, dir)
		}
		seen[filepath.Base(s.Path)] = s
	}
	for _, n := range names {
		s, ok := seen[n]
		if !ok {
			t.Errorf("missing entry %q", n)
			continue
		}
		if s.Size != 1 {
			t.Errorf("file %q size = %d, want 1", n, s.Size)
		}
	}
	if s, ok := seen["subdir"]; !ok {
		t.Error("missing subdir entry")
	} else if s.Size != 0 {
		// Per statInfoFromFileInfo, dirs report Size=0.
		t.Errorf("subdir size = %d, want 0", s.Size)
	}
}

func TestHandleStat_EmptyDirectory(t *testing.T) {
	skipIfMntHost(t)
	dir := t.TempDir()
	resp, err := HandleStat(context.Background(), &gnoi_file_pb.StatRequest{Path: dir})
	if err != nil {
		t.Fatalf("HandleStat: %v", err)
	}
	if len(resp.Stats) != 0 {
		t.Fatalf("got %d entries, want 0", len(resp.Stats))
	}
}

func TestStatInfoFromFileInfo_PermissionsOctalAsDecimal(t *testing.T) {
	skipIfMntHost(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	if err := os.WriteFile(path, []byte{}, 0); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []os.FileMode{0644, 0755, 0600, 0777, 0444} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		st, err := statInfoFromFileInfo(path, info)
		if err != nil {
			t.Fatal(err)
		}
		// Build expected by formatting mode in octal, then reading as
		// decimal — same as the production code.
		want := uint32(0)
		switch mode {
		case 0644:
			want = 644
		case 0755:
			want = 755
		case 0600:
			want = 600
		case 0777:
			want = 777
		case 0444:
			want = 444
		}
		if st.Permissions != want {
			t.Errorf("mode %o → Permissions=%d, want %d", mode, st.Permissions, want)
		}
	}
}
