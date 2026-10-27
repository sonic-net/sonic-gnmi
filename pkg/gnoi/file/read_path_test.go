package file

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	gnoi_file_pb "github.com/openconfig/gnoi/file"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func assertReadDenied(t *testing.T, path string) {
	t.Helper()
	response, err := HandleStat(context.Background(), &gnoi_file_pb.StatRequest{Path: path})
	if status.Code(err) != codes.PermissionDenied || response != nil {
		t.Fatalf("Stat(%q): response=%v error=%v", path, response, err)
	}
	stream := newFakeGetServer()
	err = HandleGet(&gnoi_file_pb.GetRequest{RemoteFile: path}, stream)
	if status.Code(err) != codes.PermissionDenied || len(stream.responses) != 0 {
		t.Fatalf("Get(%q): responses=%v error=%v", path, stream.responses, err)
	}
}

func assertPublicRead(t *testing.T, path string, content []byte) {
	t.Helper()
	response, err := HandleStat(context.Background(), &gnoi_file_pb.StatRequest{Path: path})
	if err != nil || len(response.GetStats()) != 1 || response.Stats[0].Size != uint64(len(content)) {
		t.Fatalf("Stat(%q): response=%v error=%v", path, response, err)
	}
	stream := newFakeGetServer()
	if err := HandleGet(&gnoi_file_pb.GetRequest{RemoteFile: path}, stream); err != nil {
		t.Fatalf("Get(%q): %v", path, err)
	}
	got, _ := collectStream(t, stream)
	if string(got) != string(content) {
		t.Fatalf("Get(%q): contents=%q want=%q", path, got, content)
	}
}

func TestPublicReadPermissions(t *testing.T) {
	logical, physical := useTempHostRoot(t)
	content := []byte("public fixture")
	file := filepath.Join(physical, "image.bin")
	if err := os.WriteFile(file, content, 0644); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []os.FileMode{0644, 0444, 0666, 0600, 0640, 0660, 0400, 0000} {
		t.Run(mode.String(), func(t *testing.T) {
			if err := os.Chmod(file, mode); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(logical, "image.bin")
			if mode&0004 != 0 {
				assertPublicRead(t, path, content)
			} else {
				assertReadDenied(t, path)
			}
		})
	}
	assertReadDenied(t, filepath.Join(logical, "missing.bin"))
}

func TestPublicReadHostRootMapping(t *testing.T) {
	useTempHostRoot(t)
	response, err := HandleStat(context.Background(), &gnoi_file_pb.StatRequest{Path: "/"})
	if err != nil || len(response.GetStats()) != 1 || response.Stats[0].Path != "/tmp" {
		t.Fatalf("host-root listing: %v %v", response, err)
	}
}

func TestPublicReadWithoutHostMount(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "public-read-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	if err := os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	previous := hostRoot
	hostRoot = filepath.Join(root, "missing-host-mount")
	t.Cleanup(func() { hostRoot = previous })
	path := filepath.Join(root, "image")
	if err := os.WriteFile(path, []byte("public"), 0644); err != nil {
		t.Fatal(err)
	}
	assertPublicRead(t, path, []byte("public"))
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	assertReadDenied(t, path)
}

func TestPublicReadRequiresTraversableParents(t *testing.T) {
	logical, physical := useTempHostRoot(t)
	parent := filepath.Join(physical, "private")
	if err := os.Mkdir(parent, 0755); err != nil {
		t.Fatal(err)
	}
	content := []byte("image")
	if err := os.WriteFile(filepath.Join(parent, "image.bin"), content, 0644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(logical, "private", "image.bin")
	assertPublicRead(t, path, content)
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	assertReadDenied(t, path)
	assertReadDenied(t, filepath.Join(logical, "private", "missing.bin"))
}

func TestPublicReadChecksSymlinkTargets(t *testing.T) {
	logical, physical := useTempHostRoot(t)
	for _, item := range []struct {
		name string
		mode os.FileMode
	}{{"public", 0644}, {"private", 0600}} {
		if err := os.WriteFile(filepath.Join(physical, item.name), []byte(item.name), item.mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(item.name, filepath.Join(physical, item.name+"-link")); err != nil {
			t.Fatal(err)
		}
	}
	assertPublicRead(t, filepath.Join(logical, "public-link"), []byte("public"))
	assertReadDenied(t, filepath.Join(logical, "private-link"))
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("outside host view"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(physical, "escape")); err != nil {
		t.Fatal(err)
	}
	assertReadDenied(t, filepath.Join(logical, "escape"))
}

func TestPublicStatFiltersPrivateChildren(t *testing.T) {
	logical, physical := useTempHostRoot(t)
	for _, item := range []struct {
		name string
		mode os.FileMode
	}{{"public", 0644}, {"private", 0600}} {
		if err := os.WriteFile(filepath.Join(physical, item.name), []byte("image"), item.mode); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(physical, "private-dir"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("private", filepath.Join(physical, "private-link")); err != nil {
		t.Fatal(err)
	}
	response, err := HandleStat(context.Background(), &gnoi_file_pb.StatRequest{Path: logical})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Stats) != 1 || response.Stats[0].Path != filepath.Join(logical, "public") {
		t.Fatalf("private metadata exposed: %v", response.Stats)
	}
	if err := os.Chmod(physical, 0711); err != nil {
		t.Fatal(err)
	}
	assertReadDenied(t, logical)
}

func TestPublicReadRejectsAccessACLs(t *testing.T) {
	for _, target := range []string{"file", "ancestor"} {
		t.Run(target, func(t *testing.T) {
			logical, physical := useTempHostRoot(t)
			file := filepath.Join(physical, "image")
			if err := os.WriteFile(file, []byte("image"), 0644); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(logical, "image")
			assertPublicRead(t, path, []byte("image"))
			aclPath := file
			owner, other := uint16(6), uint16(4)
			if target == "ancestor" {
				aclPath, owner, other = physical, 7, 5
			}
			acl := binary.LittleEndian.AppendUint32(nil, 2)
			for _, entry := range []struct {
				tag, perm uint16
				id        uint32
			}{{1, owner, ^uint32(0)}, {2, 0, 65534}, {4, other, ^uint32(0)},
				{16, other, ^uint32(0)}, {32, other, ^uint32(0)}} {
				acl = binary.LittleEndian.AppendUint16(acl, entry.tag)
				acl = binary.LittleEndian.AppendUint16(acl, entry.perm)
				acl = binary.LittleEndian.AppendUint32(acl, entry.id)
			}
			if err := unix.Setxattr(aclPath, "system.posix_acl_access", acl, 0); err != nil {
				if errors.Is(err, unix.ENOTSUP) {
					t.Skip("test filesystem has no POSIX ACL support")
				}
				t.Fatal(err)
			}
			assertReadDenied(t, path)
			if err := unix.Removexattr(aclPath, "system.posix_acl_access"); err != nil {
				t.Fatal(err)
			}
			assertPublicRead(t, path, []byte("image"))
		})
	}
}

func TestPublicReadUsesValidatedDescriptor(t *testing.T) {
	for _, operation := range []string{"Get", "Stat"} {
		t.Run(operation, func(t *testing.T) {
			logical, physical := useTempHostRoot(t)
			path := filepath.Join(logical, "image")
			file := filepath.Join(physical, "image")
			if err := os.WriteFile(file, []byte("public"), 0644); err != nil {
				t.Fatal(err)
			}
			replacement := filepath.Join(physical, "replacement")
			if err := os.WriteFile(replacement, []byte("root-only private contents"), 0600); err != nil {
				t.Fatal(err)
			}
			open := fsOpenRead
			withFsOpenRead(t, func(path string) (*os.File, error) {
				descriptor, err := open(path)
				if err != nil {
					return nil, err
				}
				if err := os.Rename(replacement, file); err != nil {
					descriptor.Close()
					t.Fatal(err)
				}
				return descriptor, nil
			})
			if operation == "Get" {
				stream := newFakeGetServer()
				if err := HandleGet(&gnoi_file_pb.GetRequest{RemoteFile: path}, stream); err != nil {
					t.Fatal(err)
				}
				got, _ := collectStream(t, stream)
				if string(got) != "public" {
					t.Fatalf("unchecked replacement was read: %q", got)
				}
			} else {
				response, err := HandleStat(context.Background(), &gnoi_file_pb.StatRequest{Path: path})
				if err != nil || len(response.GetStats()) != 1 || response.Stats[0].Size != 6 {
					t.Fatalf("unchecked replacement was statted: %v %v", response, err)
				}
			}
		})
	}
}
