package file

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gnoi_file_pb "github.com/openconfig/gnoi/file"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// statTestRoot returns a (logicalRoot, physicalRoot) pair that the test
// can use to build fixtures.
//
// When /mnt/host exists (containerized / SONiC-style host), HandleStat
// translates an incoming logical path P into /mnt/host+P before touching
// the filesystem. So a test that wants HandleStat to actually find its
// fixture must:
//   - put files at /mnt/host+P (the *physical* root), and
//   - send P (the *logical* root) in the StatRequest.
//
// When /mnt/host is absent the two roots coincide.
//
// The helper builds a unique subdirectory under /tmp via os.MkdirTemp on
// the physical root, registers cleanup, and returns:
//   - logical:  what the test should put in StatRequest.Path
//   - physical: where the test should actually create files/dirs
func statTestRoot(t *testing.T) (logical, physical string) {
	t.Helper()
	physBase := "/tmp"
	if _, err := os.Stat("/mnt/host"); err == nil {
		physBase = "/mnt/host/tmp"
		if err := os.MkdirAll(physBase, 0755); err != nil {
			t.Fatalf("ensure /mnt/host/tmp: %v", err)
		}
	}
	phys, err := os.MkdirTemp(physBase, "stat-test-*")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(phys) })

	logi := phys
	if strings.HasPrefix(phys, "/mnt/host") {
		logi = strings.TrimPrefix(phys, "/mnt/host")
	}
	return logi, phys
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

func TestHandleStat_RejectsMntHostPrefix(t *testing.T) {
	for _, p := range []string{"/mnt/host", "/mnt/host/tmp/foo"} {
		_, err := HandleStat(context.Background(), &gnoi_file_pb.StatRequest{Path: p})
		if err == nil || status.Code(err) != codes.InvalidArgument {
			t.Errorf("path %q: expected InvalidArgument, got %v", p, err)
		}
	}
}

func TestHandleStat_NotFound(t *testing.T) {
	logi, _ := statTestRoot(t)
	_, err := HandleStat(context.Background(), &gnoi_file_pb.StatRequest{
		Path: filepath.Join(logi, "definitely-does-not-exist-zzzz-9876543"),
	})
	if err == nil || status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound, got %v", err)
	}
}

func TestHandleStat_RegularFile(t *testing.T) {
	logi, phys := statTestRoot(t)
	physPath := filepath.Join(phys, "f.bin")
	if err := os.WriteFile(physPath, []byte("hello world"), 0640); err != nil {
		t.Fatal(err)
	}
	logiPath := filepath.Join(logi, "f.bin")

	resp, err := HandleStat(context.Background(), &gnoi_file_pb.StatRequest{Path: logiPath})
	if err != nil {
		t.Fatalf("HandleStat: %v", err)
	}
	if len(resp.Stats) != 1 {
		t.Fatalf("got %d entries, want 1", len(resp.Stats))
	}
	got := resp.Stats[0]
	if got.Path != logiPath {
		t.Errorf("Path = %q, want %q", got.Path, logiPath)
	}
	if got.Size != 11 {
		t.Errorf("Size = %d, want 11", got.Size)
	}
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
	logi, phys := statTestRoot(t)
	names := []string{"alpha.txt", "beta.txt", "gamma.txt"}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(phys, n), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(phys, "subdir"), 0755); err != nil {
		t.Fatal(err)
	}

	resp, err := HandleStat(context.Background(), &gnoi_file_pb.StatRequest{Path: logi})
	if err != nil {
		t.Fatalf("HandleStat: %v", err)
	}
	if len(resp.Stats) != len(names)+1 {
		t.Fatalf("got %d entries, want %d", len(resp.Stats), len(names)+1)
	}

	seen := map[string]*gnoi_file_pb.StatInfo{}
	for _, s := range resp.Stats {
		if filepath.Dir(s.Path) != logi {
			t.Errorf("entry %q not under dir %q", s.Path, logi)
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
		t.Errorf("subdir size = %d, want 0", s.Size)
	}
}

func TestHandleStat_EmptyDirectory(t *testing.T) {
	logi, _ := statTestRoot(t)
	resp, err := HandleStat(context.Background(), &gnoi_file_pb.StatRequest{Path: logi})
	if err != nil {
		t.Fatalf("HandleStat: %v", err)
	}
	if len(resp.Stats) != 0 {
		t.Fatalf("got %d entries, want 0", len(resp.Stats))
	}
}

func TestStatInfoFromFileInfo_PermissionsOctalAsDecimal(t *testing.T) {
	_, phys := statTestRoot(t)
	path := filepath.Join(phys, "f")
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
		var want uint32
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

// withFsStat installs a fake fsStat for the duration of the test. Tests
// use this to cover error branches (permission denied, generic Internal)
// that the root-on-tmpfs environment can't trigger via real os.Stat.
func withFsStat(t *testing.T, fake func(string) (os.FileInfo, error)) {
	t.Helper()
	prev := fsStat
	fsStat = fake
	t.Cleanup(func() { fsStat = prev })
}

// withFsReadDir is the os.ReadDir equivalent of withFsStat.
func withFsReadDir(t *testing.T, fake func(string) ([]os.DirEntry, error)) {
	t.Helper()
	prev := fsReadDir
	fsReadDir = fake
	t.Cleanup(func() { fsReadDir = prev })
}

func TestHandleStat_StatPermissionDenied(t *testing.T) {
	withFsStat(t, func(path string) (os.FileInfo, error) {
		return nil, &os.PathError{Op: "stat", Path: path, Err: os.ErrPermission}
	})
	_, err := HandleStat(context.Background(), &gnoi_file_pb.StatRequest{Path: "/tmp/x"})
	if err == nil || status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied, got %v", err)
	}
}

func TestHandleStat_StatGenericError(t *testing.T) {
	// e.g. ELOOP from a symlink loop is neither IsNotExist nor IsPermission;
	// HandleStat must surface it as Internal.
	withFsStat(t, func(path string) (os.FileInfo, error) {
		return nil, &os.PathError{Op: "stat", Path: path, Err: os.ErrInvalid}
	})
	_, err := HandleStat(context.Background(), &gnoi_file_pb.StatRequest{Path: "/tmp/x"})
	if err == nil || status.Code(err) != codes.Internal {
		t.Fatalf("expected Internal, got %v", err)
	}
}

// fakeDirInfo is a minimal os.FileInfo for a directory; the only fields
// HandleStat looks at on the parent are IsDir() and Mode(), so the rest
// can be zero-valued.
type fakeDirInfo struct{}

func (fakeDirInfo) Name() string       { return "x" }
func (fakeDirInfo) Size() int64        { return 0 }
func (fakeDirInfo) Mode() os.FileMode  { return os.ModeDir | 0755 }
func (fakeDirInfo) ModTime() time.Time { return time.Time{} }
func (fakeDirInfo) IsDir() bool        { return true }
func (fakeDirInfo) Sys() interface{}   { return nil }

func TestHandleStat_ReadDirNotExistRace(t *testing.T) {
	// Stat says directory exists; ReadDir then races with a removal.
	// HandleStat must downgrade Internal to NotFound to keep transient
	// races from looking like server errors.
	withFsStat(t, func(string) (os.FileInfo, error) { return fakeDirInfo{}, nil })
	withFsReadDir(t, func(path string) ([]os.DirEntry, error) {
		return nil, &os.PathError{Op: "readdirent", Path: path, Err: os.ErrNotExist}
	})
	_, err := HandleStat(context.Background(), &gnoi_file_pb.StatRequest{Path: "/tmp/x"})
	if err == nil || status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound on race, got %v", err)
	}
}

func TestHandleStat_ReadDirPermissionDenied(t *testing.T) {
	withFsStat(t, func(string) (os.FileInfo, error) { return fakeDirInfo{}, nil })
	withFsReadDir(t, func(path string) ([]os.DirEntry, error) {
		return nil, &os.PathError{Op: "readdirent", Path: path, Err: os.ErrPermission}
	})
	_, err := HandleStat(context.Background(), &gnoi_file_pb.StatRequest{Path: "/tmp/x"})
	if err == nil || status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied, got %v", err)
	}
}

func TestHandleStat_ReadDirGenericError(t *testing.T) {
	withFsStat(t, func(string) (os.FileInfo, error) { return fakeDirInfo{}, nil })
	withFsReadDir(t, func(path string) ([]os.DirEntry, error) {
		return nil, &os.PathError{Op: "readdirent", Path: path, Err: os.ErrInvalid}
	})
	_, err := HandleStat(context.Background(), &gnoi_file_pb.StatRequest{Path: "/tmp/x"})
	if err == nil || status.Code(err) != codes.Internal {
		t.Fatalf("expected Internal, got %v", err)
	}
}
