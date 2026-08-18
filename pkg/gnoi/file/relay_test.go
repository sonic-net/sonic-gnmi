package file

import (
	"bytes"
	"crypto/md5"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	gnoi_file_pb "github.com/openconfig/gnoi/file"
	"github.com/openconfig/gnoi/types"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const testRelayPath = "/var/tmp/device-ops-agent/desired-software.json"

func useRelayRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	oldRoot := hostRoot
	oldTrustedOwnerUID := relayTrustedOwnerUID
	hostRoot = root
	relayTrustedOwnerUID = uint32(os.Geteuid())
	t.Cleanup(func() {
		hostRoot = oldRoot
		relayTrustedOwnerUID = oldTrustedOwnerUID
	})
	return root
}

func relayPutStream(path string, data []byte) *mockPutStream {
	stream := newMockPutStream()
	stream.addOpenRequest(path, 0777)
	stream.addContentRequest(data)
	digest := md5.Sum(data)
	stream.addHashRequest(digest[:])
	return stream
}

func TestHandleRelayPutSuccess(t *testing.T) {
	root := useRelayRoot(t)
	payload := []byte(`{"schemaVersion":"1"}`)
	if err := HandleRelayPut(relayPutStream(testRelayPath, payload), testRelayPath); err != nil {
		t.Fatalf("HandleRelayPut() error = %v", err)
	}
	physical := filepath.Join(root, testRelayPath)
	got, err := os.ReadFile(physical)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload = %q, want %q", got, payload)
	}
	info, err := os.Stat(physical)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode = %o, want 0600", info.Mode().Perm())
	}
}

func TestHandleRelayPutRejectsSizeHashAndTraversal(t *testing.T) {
	useRelayRoot(t)
	tests := []struct {
		name   string
		stream *mockPutStream
		path   string
		code   codes.Code
	}{
		{
			name:   "oversize",
			stream: relayPutStream(testRelayPath, bytes.Repeat([]byte("x"), relayMaxFileSize+1)),
			path:   testRelayPath,
			code:   codes.ResourceExhausted,
		},
		{
			name: "wrong method",
			stream: func() *mockPutStream {
				s := relayPutStream(testRelayPath, []byte("x"))
				s.requests[len(s.requests)-1].GetHash().Method = types.HashType_SHA256
				return s
			}(),
			path: testRelayPath,
			code: codes.InvalidArgument,
		},
		{
			name: "encoded digest",
			stream: func() *mockPutStream {
				s := newMockPutStream()
				s.addOpenRequest(testRelayPath, 0600)
				s.addContentRequest([]byte("x"))
				s.addHashRequest([]byte("9dd4e461268c8034f5c8564e155c67a6"))
				return s
			}(),
			path: testRelayPath,
			code: codes.DataLoss,
		},
		{
			name:   "traversal",
			stream: relayPutStream("/var/tmp/device-ops-agent/../escape", []byte("x")),
			path:   "/var/tmp/device-ops-agent/../escape",
			code:   codes.InvalidArgument,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := HandleRelayPut(tt.stream, tt.path)
			if status.Code(err) != tt.code {
				t.Fatalf("code = %v, want %v; error = %v", status.Code(err), tt.code, err)
			}
		})
	}
}

func TestCanonicalHostVisiblePath(t *testing.T) {
	tests := []struct {
		path string
		want string
		ok   bool
	}{
		{path: testRelayPath, want: testRelayPath, ok: true},
		{path: "/host/doa/state/relay-journal.json", want: "/host/doa/state/relay-journal.json", ok: true},
		{path: "/var/tmp/device-ops-agent/sub/../desired-software.json"},
		{path: "/var/tmp//device-ops-agent/desired-software.json"},
		{path: "/var/tmp/device-ops-agent/./desired-software.json"},
		{path: "/mnt/host/var/tmp/device-ops-agent/desired-software.json"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got, err := CanonicalHostVisiblePath(tt.path)
			if tt.ok {
				if err != nil || got != tt.want {
					t.Fatalf("got=%q error=%v, want=%q", got, err, tt.want)
				}
				return
			}
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("code=%v, want InvalidArgument", status.Code(err))
			}
		})
	}
}

func TestHandleRelayPutRejectsSymlinkParent(t *testing.T) {
	root := useRelayRoot(t)
	if err := os.MkdirAll(filepath.Join(root, "var", "tmp"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "var", "tmp", "device-ops-agent")); err != nil {
		t.Fatal(err)
	}
	err := HandleRelayPut(relayPutStream(testRelayPath, []byte("x")), testRelayPath)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied; error = %v", status.Code(err), err)
	}
}

func TestHandleRelayPutConcurrentReplacement(t *testing.T) {
	root := useRelayRoot(t)
	payloads := [][]byte{bytes.Repeat([]byte("a"), 8192), bytes.Repeat([]byte("b"), 8192)}
	start := make(chan struct{})
	errs := make(chan error, len(payloads))
	var wg sync.WaitGroup
	for _, payload := range payloads {
		payload := payload
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- HandleRelayPut(relayPutStream(testRelayPath, payload), testRelayPath)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Put error = %v", err)
		}
	}
	final, err := os.ReadFile(filepath.Join(root, testRelayPath))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(final, payloads[0]) && !bytes.Equal(final, payloads[1]) {
		t.Fatal("final file is not one complete concurrent payload")
	}
	matches, err := filepath.Glob(filepath.Join(root, "var", "tmp", "device-ops-agent", ".desired-software.json.tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary files remain: %v", matches)
	}
}

func TestHandleRelayPutFsyncFailureCleansTemporaryFile(t *testing.T) {
	root := useRelayRoot(t)
	physical := filepath.Join(root, testRelayPath)
	if err := os.MkdirAll(filepath.Dir(physical), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(physical, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	oldFsync := relayFsync
	relayFsync = func(int) error { return errors.New("sync failed") }
	t.Cleanup(func() { relayFsync = oldFsync })

	err := HandleRelayPut(relayPutStream(testRelayPath, []byte("new")), testRelayPath)
	if status.Code(err) != codes.Internal {
		t.Fatalf("code = %v, want Internal; error = %v", status.Code(err), err)
	}
	got, err := os.ReadFile(physical)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old" {
		t.Fatalf("existing file changed to %q", got)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(physical), ".desired-software.json.tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary files remain: %v", matches)
	}
}

func TestHandleRelayGetSafetyAndHash(t *testing.T) {
	root := useRelayRoot(t)
	statusPath := "/var/tmp/device-ops-agent/software-status.json"
	physical := filepath.Join(root, statusPath)
	if err := os.MkdirAll(filepath.Dir(physical), 0755); err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"revision":1}`)
	if err := os.WriteFile(physical, payload, 0600); err != nil {
		t.Fatal(err)
	}
	stream := newFakeGetServer()
	if err := HandleRelayGet(&gnoi_file_pb.GetRequest{RemoteFile: statusPath}, stream, statusPath); err != nil {
		t.Fatalf("HandleRelayGet() error = %v", err)
	}
	got, hash := collectStream(t, stream)
	wantHash := md5.Sum(payload)
	if !bytes.Equal(got, payload) || hash.GetMethod() != types.HashType_MD5 || !bytes.Equal(hash.GetHash(), wantHash[:]) {
		t.Fatalf("unexpected data or hash: data=%q hash=%v", got, hash)
	}

	if err := HandleRelayGet(&gnoi_file_pb.GetRequest{RemoteFile: testRelayPath}, newFakeGetServer(), statusPath); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("wrong path code = %v, want PermissionDenied", status.Code(err))
	}
	if err := os.Remove(physical); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", physical); err != nil {
		t.Fatal(err)
	}
	if err := HandleRelayGet(&gnoi_file_pb.GetRequest{RemoteFile: statusPath}, newFakeGetServer(), statusPath); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("symlink code = %v, want PermissionDenied; error = %v", status.Code(err), err)
	}
}

func TestHandleRelayGetRejectsOversize(t *testing.T) {
	root := useRelayRoot(t)
	statusPath := "/var/tmp/device-ops-agent/software-status.json"
	physical := filepath.Join(root, statusPath)
	if err := os.MkdirAll(filepath.Dir(physical), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(physical, bytes.Repeat([]byte("x"), relayMaxFileSize+1), 0600); err != nil {
		t.Fatal(err)
	}
	err := HandleRelayGet(&gnoi_file_pb.GetRequest{RemoteFile: statusPath}, newFakeGetServer(), statusPath)
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("code = %v, want ResourceExhausted; error = %v", status.Code(err), err)
	}
}

func TestRestrictedOperationsRequireHostRoot(t *testing.T) {
	oldRoot := hostRoot
	hostRoot = filepath.Join(t.TempDir(), "missing")
	t.Cleanup(func() { hostRoot = oldRoot })

	err := HandleRestrictedPut(relayPutStream(testRelayPath, []byte("x")), testRelayPath)
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("Put code = %v, want FailedPrecondition; error = %v", status.Code(err), err)
	}
	err = HandleRestrictedGet(&gnoi_file_pb.GetRequest{RemoteFile: testRelayPath}, newFakeGetServer(), testRelayPath)
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("Get code = %v, want FailedPrecondition; error = %v", status.Code(err), err)
	}
}

func TestPrepareRestrictedPathsCreatesVarTmpAndHostState(t *testing.T) {
	root := useRelayRoot(t)
	paths := []string{
		"/var/tmp/device-ops-agent/desired-software.json",
		"/var/tmp/device-ops-agent/software-status.json",
		"/host/doa/state/relay-journal.json",
		"/host/doa/state/relay-completion.json",
	}
	if err := PrepareRestrictedPaths(paths...); err != nil {
		t.Fatalf("PrepareRestrictedPaths() error = %v", err)
	}
	for _, path := range []string{"/var/tmp/device-ops-agent", "/host/doa/state"} {
		info, err := os.Stat(filepath.Join(root, path))
		if err != nil || !info.IsDir() {
			t.Fatalf("prepared directory %q: info=%v error=%v", path, info, err)
		}
	}
}

func TestPrepareRestrictedPathsFsyncFailure(t *testing.T) {
	root := useRelayRoot(t)
	if err := os.MkdirAll(filepath.Join(root, "var", "tmp"), 0755); err != nil {
		t.Fatal(err)
	}
	oldFsync := relayFsync
	relayFsync = func(int) error { return errors.New("sync failed") }
	t.Cleanup(func() { relayFsync = oldFsync })
	err := PrepareRestrictedPaths(testRelayPath)
	if status.Code(err) != codes.Internal {
		t.Fatalf("code = %v, want Internal; error = %v", status.Code(err), err)
	}
}

func TestPrepareRestrictedPathsCorrectsRootOwnedUnsafeExistingDirectory(t *testing.T) {
	root := useRelayRoot(t)
	managed := filepath.Join(root, "var", "tmp", "device-ops-agent")
	if err := os.MkdirAll(managed, 0777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(managed, 0777); err != nil {
		t.Fatal(err)
	}
	err := PrepareRestrictedPaths(testRelayPath)
	if err != nil {
		t.Fatalf("PrepareRestrictedPaths() error = %v", err)
	}
	info, err := os.Stat(managed)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0750 {
		t.Fatalf("corrected mode = %04o, want 0750", info.Mode().Perm())
	}
}

func TestPrepareRestrictedPathsCorrectsHostDoaStateModes(t *testing.T) {
	root := useRelayRoot(t)
	doa := filepath.Join(root, "host", "doa")
	state := filepath.Join(doa, "state")
	if err := os.MkdirAll(state, 0777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(doa, 0777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(state, 0777); err != nil {
		t.Fatal(err)
	}
	if err := PrepareRestrictedPaths("/host/doa/state/relay-journal.json"); err != nil {
		t.Fatalf("PrepareRestrictedPaths() error = %v", err)
	}
	for _, path := range []string{doa, state} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0750 {
			t.Fatalf("%s mode = %04o, want 0750", path, info.Mode().Perm())
		}
	}
}

func TestPrepareRestrictedPathsRejectsNonRootOwnedHostDoa(t *testing.T) {
	useRelayRoot(t)
	oldFstat := relayFstat
	relayFstat = func(fd int, info *unix.Stat_t) error {
		if err := oldFstat(fd, info); err != nil {
			return err
		}
		info.Uid = relayTrustedOwnerUID + 1
		return nil
	}
	t.Cleanup(func() { relayFstat = oldFstat })
	err := PrepareRestrictedPaths("/host/doa/state/relay-journal.json")
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied; error = %v", status.Code(err), err)
	}
}

func TestPrepareRestrictedPathsAcceptsSafeExistingDirectory(t *testing.T) {
	root := useRelayRoot(t)
	managed := filepath.Join(root, "host", "doa", "state")
	if err := os.MkdirAll(managed, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "host", "doa"), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(managed, 0750); err != nil {
		t.Fatal(err)
	}
	if err := PrepareRestrictedPaths("/host/doa/state/relay-journal.json"); err != nil {
		t.Fatalf("safe existing directory rejected: %v", err)
	}
}

func TestRestrictedJournalPutAndGet(t *testing.T) {
	root := useRelayRoot(t)
	path := "/host/doa/state/relay-journal.json"
	payload := []byte(`{"schemaVersion":"1"}`)
	if err := HandleRestrictedPut(relayPutStream(path, payload), path); err != nil {
		t.Fatalf("HandleRestrictedPut() error = %v", err)
	}
	info, err := os.Stat(filepath.Join(root, path))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("journal info=%v error=%v", info, err)
	}
	stream := newFakeGetServer()
	if err := HandleRestrictedGet(&gnoi_file_pb.GetRequest{RemoteFile: path}, stream, path); err != nil {
		t.Fatalf("HandleRestrictedGet() error = %v", err)
	}
	got, _ := collectStream(t, stream)
	if !bytes.Equal(got, payload) {
		t.Fatalf("journal data = %q, want %q", got, payload)
	}
}
