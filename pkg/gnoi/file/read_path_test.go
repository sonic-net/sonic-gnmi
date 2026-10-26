package file

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	gnoi_file_pb "github.com/openconfig/gnoi/file"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func assertReadDenied(t *testing.T, path string) {
	t.Helper()
	response, err := HandleStat(context.Background(), &gnoi_file_pb.StatRequest{Path: path})
	if status.Code(err) != codes.PermissionDenied || response != nil {
		t.Fatalf("Stat(%q): got response %v, error %v", path, response, err)
	}
	stream := newFakeGetServer()
	err = HandleGet(&gnoi_file_pb.GetRequest{RemoteFile: path}, stream)
	if status.Code(err) != codes.PermissionDenied || len(stream.responses) != 0 {
		t.Fatalf("Get(%q): got %d responses, error %v", path, len(stream.responses), err)
	}
}

func TestSensitiveReadPathsDeniedBeforeProbe(t *testing.T) {
	useTempHostRoot(t)
	withFsStat(t, func(string) (os.FileInfo, error) {
		t.Fatal("sensitive path reached filesystem probe")
		return nil, nil
	})
	for _, path := range []string{
		"/etc/shadow", "//etc/shadow", "/tmp/../etc/shadow",
		"/etc/gshadow-", "/etc/sudoers.d/missing", "/root/missing",
		"/etc/shadow.bak", "/etc/gshadow.old", "/etc/sudoers.dpkg-old",
		"/host/image-SONiC-test/rw/etc/shadow",
		"/host/image-SONiC-test/rw/root/secret",
		"/proc/self/root/etc/shadow", "/dev/fd/0", "/run/missing",
		"/etc/sonic/config_db.json", "/etc/ssl/private/missing",
		"/home/admin/.ssh/id_ed25519", "/home/admin/.gnupg/missing",
		"/host/config_db.json.before_test_module", "/host/minigraph.xml",
		"/host/glome/glome.conf", "/etc/ssh/ssh_host_rsa_key",
	} {
		t.Run(path, func(t *testing.T) { assertReadDenied(t, path) })
	}
}

func TestSensitiveReadSymlinksDoNotRevealExistence(t *testing.T) {
	for _, exists := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "present"}[exists], func(t *testing.T) {
			logical, physical := useTempHostRoot(t)
			target := filepath.Join(hostRoot, "etc", "sonic", "private")
			if exists {
				if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(target, []byte("synthetic secret"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(target, filepath.Join(physical, "alias")); err != nil {
				t.Fatal(err)
			}
			assertReadDenied(t, filepath.Join(logical, "alias"))
		})
	}
}

func TestSensitiveReadDirectoryAliasAndHardlink(t *testing.T) {
	logical, physical := useTempHostRoot(t)
	protected := filepath.Join(hostRoot, "etc", "sonic")
	if err := os.MkdirAll(protected, 0755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(protected, "private")
	if err := os.WriteFile(secret, []byte("synthetic secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(protected, filepath.Join(physical, "directory")); err != nil {
		t.Fatal(err)
	}
	assertReadDenied(t, filepath.Join(logical, "directory", "private"))
	if err := os.Link(secret, filepath.Join(physical, "hardlink")); err != nil {
		t.Fatal(err)
	}
	assertReadDenied(t, filepath.Join(logical, "hardlink"))
}

func TestSensitiveReadDirectoryListingFiltersProtectedEntries(t *testing.T) {
	useTempHostRoot(t)
	etc := filepath.Join(hostRoot, "etc")
	if err := os.MkdirAll(filepath.Join(etc, "sonic"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"shadow", "passwd"} {
		if err := os.WriteFile(filepath.Join(etc, name), []byte("fixture"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(etc, "shadow"), filepath.Join(etc, "alias")); err != nil {
		t.Fatal(err)
	}
	response, err := HandleStat(context.Background(), &gnoi_file_pb.StatRequest{Path: "/etc"})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Stats) != 1 || response.Stats[0].Path != "/etc/passwd" {
		t.Fatalf("protected entry appeared in listing: %v", response.Stats)
	}
}

func TestSensitiveReadDirectorySwap(t *testing.T) {
	logical, physical := useTempHostRoot(t)
	staging := filepath.Join(physical, "staging")
	protected := filepath.Join(hostRoot, "etc", "sonic")
	for _, dir := range []string{staging, protected} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "image.bin"), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	previous := openReadFile
	t.Cleanup(func() { openReadFile = previous })
	openReadFile = func(path string, flags int, mode os.FileMode) (*os.File, error) {
		if err := os.Rename(staging, staging+"-old"); err != nil {
			return nil, err
		}
		if err := os.Symlink(protected, staging); err != nil {
			return nil, err
		}
		return previous(path, flags, mode)
	}
	stream := newFakeGetServer()
	err := HandleGet(&gnoi_file_pb.GetRequest{RemoteFile: filepath.Join(logical, "staging", "image.bin")}, stream)
	if status.Code(err) != codes.PermissionDenied || len(stream.responses) != 0 {
		t.Fatalf("directory-swap bypass: responses=%v err=%v", stream.responses, err)
	}
}

func TestSensitiveReadPrivateImageAndSafeSymlink(t *testing.T) {
	logical, physical := useTempHostRoot(t)
	image := filepath.Join(physical, "image.bin")
	if err := os.WriteFile(image, []byte("private image"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(image, filepath.Join(physical, "alias.bin")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"image.bin", "alias.bin"} {
		path := filepath.Join(logical, name)
		if _, err := HandleStat(context.Background(), &gnoi_file_pb.StatRequest{Path: path}); err != nil {
			t.Fatal(err)
		}
		stream := newFakeGetServer()
		if err := HandleGet(&gnoi_file_pb.GetRequest{RemoteFile: path}, stream); err != nil {
			t.Fatal(err)
		}
		data, _ := collectStream(t, stream)
		if string(data) != "private image" {
			t.Fatalf("wrong image contents: %q", data)
		}
	}
}

func TestSensitiveReadPathBoundaries(t *testing.T) {
	for _, path := range []string{
		"/rootfs/image.bin", "/etc/shadow-image.bin",
		"/etc/ssh/ssh_host_rsa_key.pub", "/host/image.bin",
		"/host/image-SONiC-test/rw/tmp/image.bin",
	} {
		if sensitiveReadPath(path) {
			t.Errorf("unrelated path blocked: %s", path)
		}
	}

}

func TestSensitiveReadSymlinkedHostRoot(t *testing.T) {
	logical, physical := useTempHostRoot(t)
	alias := filepath.Join(t.TempDir(), "host")
	if err := os.Symlink(hostRoot, alias); err != nil {
		t.Fatal(err)
	}
	hostRoot = alias
	if err := os.WriteFile(filepath.Join(physical, "image.bin"), []byte("image"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(logical, "image.bin")
	if _, err := HandleStat(context.Background(), &gnoi_file_pb.StatRequest{Path: path}); err != nil {
		t.Fatal(err)
	}
	if err := HandleGet(&gnoi_file_pb.GetRequest{RemoteFile: path}, newFakeGetServer()); err != nil {
		t.Fatal(err)
	}
	assertReadDenied(t, "/etc/shadow")
}

func TestSensitiveReadStatNonRegularFiles(t *testing.T) {
	logical, physical := useTempHostRoot(t)
	if err := syscall.Mkfifo(filepath.Join(physical, "pipe"), 0600); err != nil {
		t.Fatal(err)
	}
	socket, err := net.Listen("unix", filepath.Join(physical, "socket"))
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	for _, name := range []string{"pipe", "socket"} {
		path := filepath.Join(logical, name)
		response, err := HandleStat(context.Background(), &gnoi_file_pb.StatRequest{Path: path})
		if err != nil || len(response.GetStats()) != 1 {
			t.Fatalf("Stat(%q): response %v, error %v", path, response, err)
		}
	}
}

func TestSensitiveReadFinalSymlinkSwap(t *testing.T) {
	for _, method := range []string{"Stat", "Get"} {
		t.Run(method, func(t *testing.T) {
			logical, physical := useTempHostRoot(t)
			image := filepath.Join(physical, "image.bin")
			if err := os.WriteFile(image, []byte("image"), 0600); err != nil {
				t.Fatal(err)
			}
			previous := openReadFile
			t.Cleanup(func() { openReadFile = previous })
			openReadFile = func(path string, flags int, mode os.FileMode) (*os.File, error) {
				if err := os.Remove(image); err != nil {
					return nil, err
				}
				if err := os.Symlink(filepath.Join(hostRoot, "etc", "shadow"), image); err != nil {
					return nil, err
				}
				return previous(path, flags, mode)
			}
			path := filepath.Join(logical, "image.bin")
			if method == "Stat" {
				response, err := HandleStat(context.Background(), &gnoi_file_pb.StatRequest{Path: path})
				if status.Code(err) != codes.PermissionDenied || response != nil {
					t.Fatalf("Stat swap: response %v, error %v", response, err)
				}
			} else {
				stream := newFakeGetServer()
				err := HandleGet(&gnoi_file_pb.GetRequest{RemoteFile: path}, stream)
				if status.Code(err) != codes.PermissionDenied || len(stream.responses) != 0 {
					t.Fatalf("Get swap: responses %v, error %v", stream.responses, err)
				}
			}
		})
	}
}
