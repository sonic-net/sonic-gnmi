package imageexport

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestImageWorkerProcess(t *testing.T) {
	if os.Getenv("SONIC_IMAGE_TEST_WORKER") != "1" {
		return
	}
	for index, argument := range os.Args {
		if argument == "--" && len(os.Args[index+1:]) == 4 {
			args := os.Args[index+1:]
			if failure := os.Getenv("SONIC_IMAGE_TEST_CREATE_RESPONSE"); failure != "" && args[1] == "create" {
				fd, _, err := workerOperation(args[0], args[1], args[2], args[3])
				if err != nil {
					t.Fatal(err)
				}
				defer unix.Close(fd)
				if failure == "mismatch" {
					if err := unix.Sendmsg(3, []byte("OK "+strings.Repeat("f", 32)), unix.UnixRights(fd), nil, 0); err != nil {
						t.Fatal(err)
					}
				}
				return
			}
			ServeWorker(3, args[0], args[1], args[2], args[3])
			return
		}
	}
	t.Fatal("invalid worker test invocation")
}

func testStore(t *testing.T) Store {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("credential-drop integration requires an isolated root test container")
	}
	root, err := os.MkdirTemp("/tmp", "image-export-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	if err := os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"host", "etc"} {
		if err := os.Mkdir(filepath.Join(root, name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "etc", "passwd"), []byte(
		"sonic-image:x:65534:65534:Image worker:/nonexistent:/usr/sbin/nologin\n"), 0644); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binaryData, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	worker := filepath.Join(root, "worker.test")
	if err := os.WriteFile(worker, binaryData, 0755); err != nil {
		t.Fatal(err)
	}
	coverDirectory := ""
	coverTarget := flag.Lookup("test.gocoverdir")
	if testing.CoverMode() == "atomic" && coverTarget != nil && coverTarget.Value.String() != "" {
		coverDirectory = filepath.Join(root, "coverage")
		if err := os.Mkdir(coverDirectory, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(coverDirectory, 65534, 65534); err != nil {
			t.Fatal(err)
		}
		// Child counters must be collected before go test emits the profile.
		// Keep the main coverage directory private; copy back as the parent.
		t.Cleanup(func() {
			destination := coverTarget.Value.String()
			entries, err := os.ReadDir(coverDirectory)
			if err != nil {
				t.Error(err)
				return
			}
			for _, entry := range entries {
				data, err := os.ReadFile(filepath.Join(coverDirectory, entry.Name()))
				if err != nil {
					t.Error(err)
					continue
				}
				if err := os.WriteFile(filepath.Join(destination, entry.Name()), data, 0600); err != nil {
					t.Error(err)
				}
			}
		})
	}
	previous := newWorkerCommand
	newWorkerCommand = func(ctx context.Context, args ...string) *exec.Cmd {
		flags := []string{"-test.run=^TestImageWorkerProcess$"}
		if coverDirectory != "" {
			credential, err := (Store{HostRoot: root}).credentials()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chown(coverDirectory, int(credential.Uid), int(credential.Gid)); err != nil {
				t.Fatal(err)
			}
			flags = append(flags, "-test.gocoverdir="+coverDirectory)
		}
		command := exec.CommandContext(ctx, worker, append(append(flags, "--"), args...)...)
		command.Env = append(os.Environ(), "SONIC_IMAGE_TEST_WORKER=1")
		if coverDirectory != "" {
			command.Env = append(command.Env, "GOCOVERDIR="+coverDirectory)
		}
		return command
	}
	t.Cleanup(func() { newWorkerCommand = previous })
	store := Store{HostRoot: root}
	if err := store.Prepare(); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestPartsDeniesUnpublishedPaths(t *testing.T) {
	for _, path := range []string{"/etc/shadow", "/tmp/image.bin", "/root/private",
		"/opt/unknown-secret", "/host/image-test/rw/etc/shadow",
		ExportRoot + "-other/image.bin", ExportRoot + "/../staging/private",
		ExportRoot + "/.hidden", "relative.bin"} {
		if _, err := Parts(path, true); !errors.Is(err, os.ErrPermission) {
			t.Errorf("path %q: expected denial, got %v", path, err)
		}
	}
	for _, path := range []string{ExportRoot, ExportRoot + "/sonic.bin", ExportRoot + "/release/image.bin"} {
		if _, err := Parts(path, true); err != nil {
			t.Errorf("published path %q rejected: %v", path, err)
		}
	}
}

func TestPrivateImagePublicationWithRealCredentialDrop(t *testing.T) {
	store := testStore(t)
	path := ExportRoot + "/release/sonic.bin"
	upload, err := store.Create(path)
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		if err := upload.Abort(); err != nil {
			t.Error(err)
		}
	}()
	info, err := upload.File.Stat()
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("private staging mode: info=%v error=%v", info, err)
	}
	if _, err := store.Open(path); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("unpublished image became readable: %v", err)
	}
	if _, err := upload.File.Write([]byte("synthetic image")); err != nil {
		t.Fatal(err)
	}
	if err := upload.Commit(); err != nil {
		t.Fatal(err)
	}
	source, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	data, err := io.ReadAll(source)
	if err != nil || string(data) != "synthetic image" {
		t.Fatalf("published bytes %q, error=%v", data, err)
	}
	var attributes unix.Stat_t
	if err := unix.Fstat(int(source.Fd()), &attributes); err != nil {
		t.Fatal(err)
	}
	if attributes.Uid != 65534 || attributes.Gid != 65534 || attributes.Mode&0777 != 0600 {
		t.Fatalf("image is not privately owned by the service identity: %v", attributes)
	}
}

func TestAbortWithoutImagesDirectory(t *testing.T) {
	store := testStore(t)
	upload, err := store.Create(ExportRoot + "/aborted.bin")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(store.physical(ExportRoot)); err != nil {
		t.Fatal(err)
	}
	if err := upload.Abort(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(store.physical(StoreRoot + "/staging"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("abandoned staging: entries=%v error=%v", entries, err)
	}
}

func TestRootOwnedExportsAndMissingFilesIndistinguishable(t *testing.T) {
	store := testStore(t)
	for _, mode := range []os.FileMode{0600, 0644} {
		path := ExportRoot + "/root-file.bin"
		if err := os.WriteFile(store.physical(path), []byte("synthetic root secret"), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(store.physical(path), mode); err != nil {
			t.Fatal(err)
		}
		for _, candidate := range []string{path, ExportRoot + "/missing.bin"} {
			file, err := store.Open(candidate)
			if file != nil || !errors.Is(err, os.ErrPermission) {
				t.Fatalf("%s mode=%o: file=%v error=%v", candidate, mode, file, err)
			}
		}
	}
}

func TestRootPrivateParentCannotBeTraversedByWorker(t *testing.T) {
	store := testStore(t)
	parent := store.physical(ExportRoot + "/root-private")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "image.bin")
	if err := os.WriteFile(path, []byte("image"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(path, 65534, 65534); err != nil {
		t.Fatal(err)
	}
	if file, err := store.Open(ExportRoot + "/root-private/image.bin"); file != nil ||
		!errors.Is(err, os.ErrPermission) {
		t.Fatalf("root-private parent bypass: file=%v error=%v", file, err)
	}
}

func TestSymlinkHardlinkAndNonRegularArtifactsDenied(t *testing.T) {
	store := testStore(t)
	path := ExportRoot + "/image.bin"
	upload, err := store.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := upload.File.Write([]byte("image")); err != nil {
		t.Fatal(err)
	}
	if err := upload.Commit(); err != nil {
		t.Fatal(err)
	}
	physical := store.physical(path)
	if err := os.Symlink(physical, store.physical(ExportRoot+"/symbolic.bin")); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(physical, store.physical(ExportRoot+"/hardlink.bin")); err != nil {
		t.Fatal(err)
	}
	fifo := store.physical(ExportRoot + "/pipe")
	if err := unix.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(fifo, 65534, 65534); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"image.bin", "symbolic.bin", "hardlink.bin", "pipe"} {
		if file, err := store.Open(ExportRoot + "/" + name); file != nil ||
			!errors.Is(err, os.ErrPermission) {
			t.Fatalf("alias/nonregular %s: file=%v error=%v", name, file, err)
		}
	}
}

func TestAbortedUploadIsNotPublished(t *testing.T) {
	store := testStore(t)
	path := ExportRoot + "/aborted.bin"
	upload, err := store.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := upload.File.Write([]byte("partial")); err != nil {
		t.Fatal(err)
	}
	if err := upload.Abort(); err != nil {
		t.Fatal(err)
	}
	if file, err := store.Open(path); file != nil || !errors.Is(err, os.ErrPermission) {
		t.Fatalf("aborted artifact exposed: %v %v", file, err)
	}
	staged, err := os.ReadDir(store.physical(StoreRoot + "/staging"))
	if err != nil || len(staged) != 0 {
		t.Fatalf("staging leak: entries=%v error=%v", staged, err)
	}
}

func TestWorkerRefusesRootAndInvalidAccount(t *testing.T) {
	store := testStore(t)
	if fd, _, err := workerOperation(store.HostRoot, "open", ExportRoot, ""); fd >= 0 ||
		!errors.Is(err, os.ErrPermission) {
		t.Fatalf("worker accepted root execution: fd=%d error=%v", fd, err)
	}
	for _, record := range []string{"", "sonic-image:x:0:0:Invalid:/root:/usr/sbin/nologin\n"} {
		if err := os.WriteFile(store.physical("/etc/passwd"), []byte(record), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := store.credentials(); err == nil {
			t.Fatal("missing/root service identity accepted")
		}
	}
}

func TestPOSIXACLIsEnforcedByActualUnprivilegedOpen(t *testing.T) {
	store := testStore(t)
	path := ExportRoot + "/acl-private.bin"
	content := []byte("synthetic image")
	upload, err := store.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upload.Abort()
	if _, err := upload.File.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := upload.Commit(); err != nil {
		t.Fatal(err)
	}
	assertReadable := func() {
		t.Helper()
		file, err := store.Open(path)
		if err != nil {
			t.Fatalf("valid service-owned artifact is not readable: %v", err)
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil || !bytes.Equal(data, content) {
			t.Fatalf("artifact read: data=%q error=%v", data, err)
		}
	}
	assertReadable()
	ancestor := store.physical("/host")
	var acl bytes.Buffer
	if err := binary.Write(&acl, binary.LittleEndian, uint32(2)); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct {
		Tag, Perm uint16
		ID        uint32
	}{{1, 7, ^uint32(0)}, {2, 0, 65534}, {4, 5, ^uint32(0)},
		{16, 5, ^uint32(0)}, {32, 5, ^uint32(0)}} {
		if err := binary.Write(&acl, binary.LittleEndian, entry); err != nil {
			t.Fatal(err)
		}
	}
	if err := unix.Setxattr(ancestor, "system.posix_acl_access", acl.Bytes(), 0); err != nil {
		if errors.Is(err, unix.ENOTSUP) {
			t.Skip("test filesystem has no POSIX ACL support")
		}
		t.Fatal(err)
	}
	file, err := store.Open(path)
	if file != nil {
		defer file.Close()
	}
	if file != nil || !errors.Is(err, os.ErrPermission) {
		t.Fatalf("ACL bypass by root parent process: file=%v error=%v", file, err)
	}
	if err := unix.Removexattr(ancestor, "system.posix_acl_access"); err != nil {
		t.Fatal(err)
	}
	assertReadable()
}

func TestUnsafeStoreIsNotRepermissioned(t *testing.T) {
	store := testStore(t)
	root := store.physical(ExportRoot)
	if err := os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := store.Prepare(); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("unsafe store accepted: %v", err)
	}
	info, err := os.Stat(root)
	if err != nil || info.Mode().Perm() != 0755 {
		t.Fatalf("existing permissions were silently changed: %v %v", info, err)
	}
}

func TestUnexportedPathDeniedBeforeAccountLookup(t *testing.T) {
	store := Store{HostRoot: "/missing-host-root"}
	if _, err := store.Open("/opt/unknown-secret"); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("unexported path reached filesystem/account lookup: %v", err)
	}

	if _, err := store.Create(strings.Repeat("/", 2) + "etc/shadow"); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("unexported creation reached provisioning: %v", err)
	}
}

func TestInvalidWorkerOperationsAndTokensDenied(t *testing.T) {
	store := testStore(t)
	for _, operation := range []string{"invalid", "publish", "abort"} {
		if _, _, err := store.request(operation, ExportRoot+"/image.bin", "invalid"); !errors.Is(err, os.ErrPermission) {
			t.Fatalf("invalid %s request accepted: %v", operation, err)
		}
	}
}

func TestWorkerStartupAndMissingAccountErrorsAreExplicit(t *testing.T) {
	store := testStore(t)
	newWorkerCommand = func(ctx context.Context, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, filepath.Join(store.HostRoot, "missing-worker"), args...)
	}
	if _, err := store.Open(ExportRoot + "/image.bin"); err == nil || errors.Is(err, os.ErrPermission) {
		t.Fatalf("missing worker did not produce an explicit runtime error: %v", err)
	}
	if err := os.Remove(filepath.Join(store.HostRoot, "etc", "passwd")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Open(ExportRoot + "/image.bin"); err == nil || errors.Is(err, os.ErrPermission) {
		t.Fatalf("missing account did not produce an explicit runtime error: %v", err)
	}
}
