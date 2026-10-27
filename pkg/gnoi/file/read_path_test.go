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
	if status.Code(err) != codes.PermissionDenied || response != nil ||
		status.Convert(err).Message() != "file read access denied" {
		t.Fatalf("Stat(%q): response=%v error=%v", path, response, err)
	}
	stream := newFakeGetServer()
	err = HandleGet(&gnoi_file_pb.GetRequest{RemoteFile: path}, stream)
	if status.Code(err) != codes.PermissionDenied || len(stream.responses) != 0 ||
		status.Convert(err).Message() != "file read access denied" {
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

func TestPublicReadThroughSearchOnlyDirectory(t *testing.T) {
	logical, physical := useTempHostRoot(t)
	parent := filepath.Join(physical, "search-only")
	if err := os.Mkdir(parent, 0755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(parent, 0755) })
	file := filepath.Join(parent, "image")
	if err := os.WriteFile(file, []byte("public"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0111); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "public" {
		t.Fatalf("ordinary open through search-only directory: %q %v", data, err)
	}
	assertPublicRead(t, filepath.Join(logical, "search-only", "image"), []byte("public"))
	assertReadDenied(t, filepath.Join(logical, "search-only"))
}

func TestPublicReadChecksOriginalSymlinkAncestors(t *testing.T) {
	logical, physical := useTempHostRoot(t)
	if err := os.WriteFile(filepath.Join(physical, "image"), []byte("public"), 0644); err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(physical, "private")
	if err := os.Mkdir(private, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../image", filepath.Join(private, "link")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(logical, "private", "link")
	assertPublicRead(t, path, []byte("public"))
	if err := os.Chmod(private, 0700); err != nil {
		t.Fatal(err)
	}
	assertReadDenied(t, path)
	assertReadDenied(t, logical+"/private/../image")
	if err := os.Symlink("private/../image", filepath.Join(physical, "detour")); err != nil {
		t.Fatal(err)
	}
	assertReadDenied(t, filepath.Join(logical, "detour"))
	if err := os.Chmod(private, 0755); err != nil {
		t.Fatal(err)
	}
	setPublicReadDenyACL(t, private, true)
	assertReadDenied(t, path)
	if err := unix.Removexattr(private, "system.posix_acl_access"); err != nil {
		t.Fatal(err)
	}
	assertPublicRead(t, path, []byte("public"))
}

func TestPublicReadPrefixProbesAndLinkLimits(t *testing.T) {
	logical, physical := useTempHostRoot(t)
	if err := os.WriteFile(filepath.Join(physical, "secret"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"secret/child", "missing/child"} {
		assertReadDenied(t, logical+"/"+path)
	}
	for name, target := range map[string]string{"loop": "loop", "escape": "../../.."} {
		if err := os.Symlink(target, filepath.Join(physical, name)); err != nil {
			t.Fatal(err)
		}
		assertReadDenied(t, filepath.Join(logical, name))
	}
	if err := os.Symlink(filepath.Join(logical, "secret"), filepath.Join(physical, "absolute")); err != nil {
		t.Fatal(err)
	}
	assertReadDenied(t, filepath.Join(logical, "absolute"))
	if err := os.Chmod(filepath.Join(physical, "secret"), 0644); err != nil {
		t.Fatal(err)
	}
	assertPublicRead(t, filepath.Join(logical, "absolute"), []byte("private"))
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
			if target == "ancestor" {
				aclPath = physical
			}
			setPublicReadDenyACL(t, aclPath, target == "ancestor")
			assertReadDenied(t, path)
			if err := unix.Removexattr(aclPath, "system.posix_acl_access"); err != nil {
				t.Fatal(err)
			}
			assertPublicRead(t, path, []byte("image"))
		})
	}
}

func setPublicReadDenyACL(t *testing.T, path string, directory bool) {
	t.Helper()
	owner, other := uint16(6), uint16(4)
	if directory {
		owner, other = 7, 5
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
	if err := unix.Setxattr(path, "system.posix_acl_access", acl, 0); err != nil {
		if errors.Is(err, unix.ENOTSUP) {
			t.Skip("test filesystem has no POSIX ACL support")
		}
		t.Fatal(err)
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

func TestPublicStatPreservesOriginalPaths(t *testing.T) {
	logical, physical := useTempHostRoot(t)
	for _, directory := range []string{"target/sub", "target/list", "list"} {
		if err := os.MkdirAll(filepath.Join(physical, directory), 0755); err != nil {
			t.Fatal(err)
		}
	}
	content := []byte("the symlink-selected file")
	for path, data := range map[string][]byte{
		"target/list/entry": content, "target/entry": content,
		"list/entry": []byte("decoy"), "entry": []byte("another decoy"),
	} {
		if err := os.WriteFile(filepath.Join(physical, path), data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("target/sub", filepath.Join(physical, "alias")); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, path, reported string
		count                int
	}{
		{"regular", "/alias/../list/entry", "/alias/../list/entry", 1},
		{"directory", "/alias/../list", "/alias/../list/entry", 1},
		{"symlink-parent", "/alias/..", "/alias/../entry", 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			response, err := HandleStat(context.Background(), &gnoi_file_pb.StatRequest{Path: logical + test.path})
			if err != nil || len(response.GetStats()) != test.count {
				t.Fatalf("Stat: response=%v error=%v", response, err)
			}
			reported := logical + test.reported
			for _, entry := range response.Stats {
				if entry.Path == reported {
					if entry.Size != uint64(len(content)) {
						t.Fatalf("wrong inode metadata: %v", entry)
					}
					assertPublicRead(t, entry.Path, content)
					return
				}
			}
			t.Fatalf("missing original round-trip path %q: %v", reported, response)
		})
	}
}

func TestPublicStatUsesValidatedDirectory(t *testing.T) {
	for _, mode := range []os.FileMode{0644, 0600} {
		t.Run(mode.String(), func(t *testing.T) {
			logical, physical := useTempHostRoot(t)
			directory := filepath.Join(physical, "listing")
			if err := os.Mkdir(directory, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, "entry"), []byte("public"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, "private"), []byte("secret"), 0600); err != nil {
				t.Fatal(err)
			}
			readDir := fsReadDir
			fsReadDir = func(file *os.File) ([]os.DirEntry, error) {
				entries, err := readDir(file)
				if err != nil {
					return nil, err
				}
				if err := os.Rename(directory, directory+"-moved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(directory, 0755); err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{"entry", "private"} {
					if err := os.WriteFile(filepath.Join(directory, name), []byte("unrelated replacement contents"), mode); err != nil {
						t.Fatal(err)
					}
				}
				return entries, nil
			}
			t.Cleanup(func() { fsReadDir = readDir })
			response, err := HandleStat(context.Background(), &gnoi_file_pb.StatRequest{Path: logical + "/listing"})
			if err != nil || len(response.GetStats()) != 1 ||
				response.Stats[0].Path != logical+"/listing/entry" || response.Stats[0].Size != 6 {
				t.Fatalf("directory replacement changed metadata: %v %v", response, err)
			}
		})
	}
}

func TestPublicReadChildBoundaries(t *testing.T) {
	logical, physical := useTempHostRoot(t)
	parent, err := openPublicReadFile(logical)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	for _, name := range []string{"", ".", "..", "../image", "child/image"} {
		if file, err := openPublicReadChild(parent, name); !os.IsPermission(err) || file != nil {
			t.Fatalf("invalid child name %q: file=%v error=%v", name, file, err)
		}
	}
	outside, err := os.Open("/tmp")
	if err != nil {
		t.Fatal(err)
	}
	defer outside.Close()
	before, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if file, err := openPublicReadChild(outside, "image"); !os.IsPermission(err) || file != nil {
			t.Fatalf("outside host root: file=%v error=%v", file, err)
		}
	}
	after, err := os.ReadDir("/proc/self/fd")
	if err != nil || len(before) != len(after) {
		t.Fatalf("rejected ancestry leaked descriptors: before=%d after=%d error=%v", len(before), len(after), err)
	}
	if err := os.Chmod(physical, 0700); err != nil {
		t.Fatal(err)
	}
	if file, err := openPublicReadChild(parent, "image"); !os.IsPermission(err) || file != nil {
		t.Fatalf("private pinned parent: file=%v error=%v", file, err)
	}
	if err := os.Chmod(physical, 0755); err != nil {
		t.Fatal(err)
	}
	setPublicReadDenyACL(t, physical, true)
	if file, err := openPublicReadChild(parent, "image"); !os.IsPermission(err) || file != nil {
		t.Fatalf("ACL-restricted pinned parent: file=%v error=%v", file, err)
	}
}

func TestPublicStatChildSymlinkPolicy(t *testing.T) {
	logical, physical := useTempHostRoot(t)
	for _, name := range []string{"listing", "private-dir", "acl-dir"} {
		if err := os.Mkdir(filepath.Join(physical, name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for name, mode := range map[string]os.FileMode{"public": 0644, "private": 0600} {
		if err := os.WriteFile(filepath.Join(physical, name), []byte("image"), mode); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"private-dir", "acl-dir"} {
		if err := os.Symlink("../public", filepath.Join(physical, name, "link")); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(physical, "private-dir"), 0700); err != nil {
		t.Fatal(err)
	}
	setPublicReadDenyACL(t, filepath.Join(physical, "acl-dir"), true)
	for name, target := range map[string]string{
		"relative": "../public", "absolute": logical + "/public",
		"private": "../private", "private-ancestor": "../private-dir/link",
		"acl-ancestor": "../acl-dir/link", "escape": "/../public", "loop": "loop",
	} {
		if err := os.Symlink(target, filepath.Join(physical, "listing", name)); err != nil {
			t.Fatal(err)
		}
	}
	response, err := HandleStat(context.Background(), &gnoi_file_pb.StatRequest{Path: logical + "/listing"})
	if err != nil || len(response.GetStats()) != 2 {
		t.Fatalf("public symlink listing: %v %v", response, err)
	}
	for _, entry := range response.Stats {
		if entry.Path != logical+"/listing/relative" && entry.Path != logical+"/listing/absolute" {
			t.Fatalf("private or out-of-root entry exposed: %v", entry)
		}
		assertPublicRead(t, entry.Path, []byte("image"))
	}
}
