package file

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	gnoi_file_pb "github.com/openconfig/gnoi/file"
	"github.com/sonic-net/sonic-gnmi/internal/imageexport"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func assertReadDenied(t *testing.T, ctx context.Context, path string) {
	t.Helper()
	response, err := HandleStat(ctx, &gnoi_file_pb.StatRequest{Path: path})
	if status.Code(err) != codes.PermissionDenied || response != nil {
		t.Fatalf("Stat(%q): response=%v error=%v", path, response, err)
	}
	stream := newFakeGetServer()
	stream.ctx = ctx
	err = HandleGet(&gnoi_file_pb.GetRequest{RemoteFile: path}, stream)
	if status.Code(err) != codes.PermissionDenied || len(stream.responses) != 0 {
		t.Fatalf("Get(%q): responses=%v error=%v", path, stream.responses, err)
	}
}

func TestUnexportedReadPathsDeniedBeforeFilesystemAccess(t *testing.T) {
	withFsStat(t, func(string) (os.FileInfo, error) {
		t.Fatal("unexported path reached filesystem probe")
		return nil, nil
	})
	for _, role := range []string{"readonly", "readwrite", "root"} {
		ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("role", role))
		for _, path := range []string{
			"/etc/shadow", "/etc/shadow.missing", "/root/private", "/proc/self/root/etc/shadow",
			"/opt/vendor/private.bin", "/var/private/new-secret", "/tmp/private-image.bin",
			"/host/config_db.json", "/host/image-test/rw/etc/shadow",
			imageexport.ExportRoot + "-other/image.bin",
		} {
			assertReadDenied(t, ctx, path)
		}
	}
}

func TestReadDirectoryListingFiltersUnavailableArtifacts(t *testing.T) {
	logical, physical := useTempHostRoot(t)
	for _, name := range []string{"public.bin", "private.bin"} {
		if err := os.WriteFile(filepath.Join(physical, name), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	previous := openImageFile
	openImageFile = func(ctx context.Context, root, path string) (*os.File, error) {
		if filepath.Base(path) == "private.bin" {
			return nil, os.ErrPermission
		}
		return previous(ctx, root, path)
	}
	response, err := HandleStat(context.Background(), &gnoi_file_pb.StatRequest{Path: logical})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Stats) != 1 || response.Stats[0].Path != filepath.Join(logical, "public.bin") {
		t.Fatalf("unavailable artifact disclosed: %v", response.Stats)
	}
}

func TestApprovedImageWorkerDenialsReturnNoMetadataOrContents(t *testing.T) {
	logical, _ := useTempHostRoot(t)
	openImageFile = func(context.Context, string, string) (*os.File, error) { return nil, os.ErrPermission }
	assertReadDenied(t, context.Background(), filepath.Join(logical, "image.bin"))
}

func TestManagedImagePrefixBoundaries(t *testing.T) {
	for _, path := range []string{"/rootfs/image.bin", "/etc/shadow-image.bin",
		"/host/image.bin", imageexport.ExportRoot + "-other/image.bin",
		imageexport.ExportRoot + "/../staging/private"} {
		if !sensitiveReadPath(path) {
			t.Errorf("unpublished pathname accepted: %s", path)
		}
	}
	for _, path := range []string{imageexport.ExportRoot, imageexport.ExportRoot + "/image.bin"} {
		if sensitiveReadPath(path) {
			t.Errorf("managed image path rejected: %s", path)
		}
	}
}
