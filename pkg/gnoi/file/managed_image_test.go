package file

import (
	"context"
	"crypto/md5"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/openconfig/gnoi/common"
	gnoi_file_pb "github.com/openconfig/gnoi/file"
	"github.com/sonic-net/sonic-gnmi/internal/imageexport"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func realImageStore(t *testing.T) imageexport.Store {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("native credential-drop integration requires an isolated root test environment")
	}
	root, err := os.MkdirTemp("/tmp", "gnoi-image-export-")
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
	if err := os.WriteFile(filepath.Join(root, "etc/passwd"), []byte(
		"sonic-image:x:65534:65534:Image worker:/nonexistent:/usr/sbin/nologin\n"), 0644); err != nil {
		t.Fatal(err)
	}
	worker := filepath.Join(root, "sonic-image-worker")
	command := exec.Command("go", "build", "-o", worker,
		"github.com/sonic-net/sonic-gnmi/cmd/sonic-image-worker")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build production image worker: %v\n%s", err, output)
	}
	previousRoot := hostRoot
	hostRoot = root
	t.Cleanup(func() { hostRoot = previousRoot })

	// Relocate only the executable. Production credential drops, filesystem
	// checks, descriptor transfer, publication and handler code run unchanged.
	patches := gomonkey.ApplyFunc(exec.CommandContext,
		func(_ context.Context, name string, args ...string) *exec.Cmd {
			if name != "/usr/sbin/sonic-image-worker" {
				t.Fatalf("unexpected worker command %q", name)
			}
			return exec.Command(worker, args...)
		})
	t.Cleanup(patches.Reset)
	return imageexport.Store{HostRoot: root}
}

func assertNoPrivateStaging(t *testing.T, store imageexport.Store) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(store.HostRoot, imageexport.StoreRoot, "staging"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("private staging was not cleaned: entries=%v error=%v", entries, err)
	}
}

func TestManagedImagePutGetStatWithProductionWorker(t *testing.T) {
	store := realImageStore(t)
	path := imageexport.ExportRoot + "/sonic.bin"
	content := []byte("synthetic SONiC image")
	digest := md5.Sum(content)
	put := newMockPutStream()
	put.addOpenRequest(path, 0)
	put.addContentRequest(content)
	put.addHashRequest(digest[:])
	if err := HandlePut(put); err != nil {
		t.Fatal(err)
	}
	stream := newFakeGetServer()
	if err := HandleGet(&gnoi_file_pb.GetRequest{RemoteFile: path}, stream); err != nil {
		t.Fatal(err)
	}
	actual, hash := collectStream(t, stream)
	if string(actual) != string(content) || string(hash.Hash) != string(digest[:]) {
		t.Fatalf("Get returned unexpected image bytes or digest")
	}
	private := filepath.Join(store.HostRoot, imageexport.ExportRoot, "root-only.bin")
	if err := os.WriteFile(private, []byte("synthetic root-only secret"), 0600); err != nil {
		t.Fatal(err)
	}
	assertReadDenied(t, context.Background(), imageexport.ExportRoot+"/root-only.bin")
	assertReadDenied(t, context.Background(), imageexport.ExportRoot+"/missing.bin")
	response, err := HandleStat(context.Background(),
		&gnoi_file_pb.StatRequest{Path: imageexport.ExportRoot})
	if err != nil || len(response.Stats) != 1 || response.Stats[0].Path != path ||
		response.Stats[0].Permissions != 600 {
		t.Fatalf("Stat disclosed an unavailable artifact: response=%v error=%v", response, err)
	}
	assertNoPrivateStaging(t, store)
}

func TestManagedImagePutFailureDoesNotPublish(t *testing.T) {
	store := realImageStore(t)
	path := imageexport.ExportRoot + "/failed.bin"
	stream := newMockPutStream()
	stream.addOpenRequest(path, 0600)
	stream.addContentRequest([]byte("incomplete"))
	stream.addHashRequest([]byte("incorrect hash"))
	if err := HandlePut(stream); status.Code(err) != codes.DataLoss {
		t.Fatalf("expected failed integrity verification, got %v", err)
	}
	assertReadDenied(t, context.Background(), path)
	assertNoPrivateStaging(t, store)
}

func TestManagedImageHTTPTransferAndRollback(t *testing.T) {
	store := realImageStore(t)
	for _, truncated := range []bool{false, true} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if truncated {
				w.Header().Set("Content-Length", "100")
			}
			w.Write([]byte("synthetic image"))
		}))
		path := imageexport.ExportRoot + "/download.bin"
		request := &gnoi_file_pb.TransferToRemoteRequest{
			LocalPath: path,
			RemoteDownload: &common.RemoteDownload{
				Path: server.URL, Protocol: common.RemoteDownload_HTTP,
			},
		}
		response, err := HandleTransferToRemote(context.Background(), request)
		server.Close()
		if truncated {
			if status.Code(err) != codes.Internal || response != nil {
				t.Fatalf("truncated transfer reported success: response=%v error=%v", response, err)
			}
		} else if err != nil || response == nil {
			t.Fatalf("managed transfer failed: %v", err)
		}
		source, err := store.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		data := make([]byte, len("synthetic image"))
		_, readErr := source.Read(data)
		source.Close()
		if readErr != nil || string(data) != "synthetic image" {
			t.Fatalf("failed transfer damaged published image: data=%q error=%v", data, readErr)
		}
		assertNoPrivateStaging(t, store)
	}
}

func TestManagedImagePrivateModeRequired(t *testing.T) {
	stream := newMockPutStream()
	stream.addOpenRequest(imageexport.ExportRoot+"/sonic.bin", 0644)
	if err := HandlePut(stream); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("world-readable managed image accepted: %v", err)
	}
}

func TestManagedImagePrivateStoreHasNoLegacyWriteBypass(t *testing.T) {
	for _, path := range []string{imageexport.StoreRoot + "/staging/token",
		imageexport.ExportRoot + "/.hidden", imageexport.StoreRoot + "/other.bin"} {
		stream := newMockPutStream()
		stream.addOpenRequest(path, 0)
		if err := HandlePut(stream); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("Put(%q) entered legacy write path: %v", path, err)
		}
		response, err := HandleTransferToRemote(context.Background(),
			&gnoi_file_pb.TransferToRemoteRequest{
				LocalPath: path,
				RemoteDownload: &common.RemoteDownload{
					Path: "http://127.0.0.1:1/must-not-contact", Protocol: common.RemoteDownload_HTTP,
				},
			})
		if response != nil || status.Code(err) != codes.PermissionDenied {
			t.Fatalf("Transfer(%q) entered legacy write path: response=%v error=%v", path, response, err)
		}
	}
}
