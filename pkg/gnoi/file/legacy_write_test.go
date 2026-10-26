package file

import (
	"context"
	"crypto/md5"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/openconfig/gnoi/common"
	gnoi_file_pb "github.com/openconfig/gnoi/file"
	"github.com/sonic-net/sonic-gnmi/internal/imageexport"
	"golang.org/x/sys/unix"
)

func publishLegacyTestImage(t *testing.T, store imageexport.Store) string {
	t.Helper()
	path := imageexport.ExportRoot + "/sonic.bin"
	upload, err := store.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer abortImage(upload)
	if _, err := upload.File.Write([]byte("original image")); err != nil {
		t.Fatal(err)
	}
	if err := upload.Commit(); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertLegacyImageUnchanged(t *testing.T, store imageexport.Store, path string) {
	t.Helper()
	source, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	data, err := io.ReadAll(source)
	if err != nil || string(data) != "original image" {
		t.Fatalf("legacy alias damaged image: data=%q error=%v", data, err)
	}
}

func TestLegacyPutTransferAndRemoveRejectPrivateAliases(t *testing.T) {
	store := realImageStore(t)
	path := publishLegacyTestImage(t, store)
	tmp := filepath.Join(store.HostRoot, "tmp")
	if err := os.Mkdir(tmp, 0755); err != nil {
		t.Fatal(err)
	}
	image := filepath.Join(store.HostRoot, path)
	for _, kind := range []string{"directory", "final", "hardlink", "journal"} {
		alias := filepath.Join(tmp, "alias")
		logical := "/tmp/alias"
		switch kind {
		case "directory":
			if err := os.Symlink(filepath.Dir(image), alias); err != nil {
				t.Fatal(err)
			}
			logical += "/sonic.bin"
		case "journal":
			if err := os.Symlink(filepath.Join(store.HostRoot, imageexport.StoreRoot), alias); err != nil {
				t.Fatal(err)
			}
			logical += "/ownership.lock"
		case "final":
			if err := os.Symlink(image, alias); err != nil {
				t.Fatal(err)
			}
		case "hardlink":
			if err := os.Link(image, alias); err != nil {
				t.Fatal(err)
			}
		}
		content := []byte("must not overwrite original")
		digest := md5.Sum(content)
		stream := newMockPutStream()
		stream.addOpenRequest(logical, 0644)
		stream.addContentRequest(content)
		stream.addHashRequest(digest[:])
		if err := HandlePut(stream); err == nil {
			t.Fatalf("Put accepted %s alias", kind)
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "100")
			w.Write([]byte("partial"))
		}))
		_, err := HandleTransferToRemote(context.Background(), &gnoi_file_pb.TransferToRemoteRequest{
			LocalPath:      logical,
			RemoteDownload: &common.RemoteDownload{Path: server.URL, Protocol: common.RemoteDownload_HTTP},
		})
		server.Close()
		if err == nil {
			t.Fatalf("Transfer accepted %s alias", kind)
		}
		if _, err := HandleFileRemove(context.Background(),
			&gnoi_file_pb.RemoveRequest{RemoteFile: logical}); err == nil {
			t.Fatalf("Remove accepted %s alias", kind)
		}
		if err := os.Remove(alias); err != nil {
			t.Fatal(err)
		}
		assertLegacyImageUnchanged(t, store, path)
	}
}

func TestLegacyParentSwapUsesPinnedDirectory(t *testing.T) {
	store := realImageStore(t)
	path := publishLegacyTestImage(t, store)
	parent := filepath.Join(store.HostRoot, "tmp", "parent")
	if err := os.MkdirAll(parent, 0755); err != nil {
		t.Fatal(err)
	}
	upload, err := createLegacyUpload("/tmp/parent/sonic.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer upload.Abort()
	if err := os.Rename(parent, parent+"-moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(store.HostRoot, imageexport.ExportRoot), parent); err != nil {
		t.Fatal(err)
	}
	if _, err := upload.File.Write([]byte("legacy upload")); err != nil {
		t.Fatal(err)
	}
	if err := upload.Commit(0644); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(filepath.Join(parent+"-moved", "sonic.bin"))
	if err != nil || string(actual) != "legacy upload" {
		t.Fatalf("publication did not use pinned parent: data=%q error=%v", actual, err)
	}
	assertLegacyImageUnchanged(t, store, path)
}

func TestLegacyFinalSwapCannotTruncateOrRemovePublishedImage(t *testing.T) {
	store := realImageStore(t)
	path := publishLegacyTestImage(t, store)
	destination := filepath.Join(store.HostRoot, "tmp", "target.bin")
	upload, err := createLegacyUpload("/tmp/target.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer upload.Abort()
	if _, err := upload.File.Write([]byte("legacy upload")); err != nil {
		t.Fatal(err)
	}
	patches := gomonkey.NewPatches()
	patches.ApplyFunc(unix.Renameat, func(oldFD int, old string, newFD int, new string) error {
		if err := os.Symlink(filepath.Join(store.HostRoot, path), destination); err != nil {
			t.Fatal(err)
		}
		patches.Reset()
		return unix.Renameat(oldFD, old, newFD, new)
	})
	err = upload.Commit(0644)
	patches.Reset()
	if err != nil {
		t.Fatal(err)
	}
	assertLegacyImageUnchanged(t, store, path)
	actual, err := os.ReadFile(destination)
	if err != nil || string(actual) != "legacy upload" {
		t.Fatalf("atomic publication followed final symlink: data=%q error=%v", actual, err)
	}
}

func TestLegacyHTTPOverwritePreservesNonRootOwnership(t *testing.T) {
	store := realImageStore(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("replacement"))
	}))
	defer server.Close()
	for _, mode := range []os.FileMode{0600, 0640} {
		destination := filepath.Join(store.HostRoot, "tmp", "owned.bin")
		if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, []byte("original"), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(destination, 65532, 65531); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(destination, mode); err != nil {
			t.Fatal(err)
		}
		if _, err := HandleTransferToRemote(context.Background(), &gnoi_file_pb.TransferToRemoteRequest{
			LocalPath:      "/tmp/owned.bin",
			RemoteDownload: &common.RemoteDownload{Path: server.URL, Protocol: common.RemoteDownload_HTTP},
		}); err != nil {
			t.Fatal(err)
		}
		var info unix.Stat_t
		if err := unix.Stat(destination, &info); err != nil {
			t.Fatal(err)
		}
		if info.Uid != 65532 || info.Gid != 65531 || info.Mode&0777 != uint32(mode) {
			t.Fatalf("ownership/mode changed during overwrite: %+v", info)
		}
		data, err := os.ReadFile(destination)
		if err != nil || string(data) != "replacement" {
			t.Fatalf("replacement not published: data=%q error=%v", data, err)
		}
	}
}
