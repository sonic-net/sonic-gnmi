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

func TestHandleStatValidation(t *testing.T) {
	tests := []struct {
		name string
		req  *gnoi_file_pb.StatRequest
	}{
		{name: "nil request"},
		{name: "empty path", req: &gnoi_file_pb.StatRequest{}},
		{name: "relative path", req: &gnoi_file_pb.StatRequest{Path: "tmp/file"}},
		{name: "host mount", req: &gnoi_file_pb.StatRequest{Path: "/mnt/host/tmp/file"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := HandleStat(context.Background(), test.req)
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("code = %v, want InvalidArgument; err=%v", status.Code(err), err)
			}
		})
	}
}

func TestHandleStatRegularFile(t *testing.T) {
	logical, physical := useTempHostRoot(t)
	physicalPath := filepath.Join(physical, "file.bin")
	logicalPath := filepath.Join(logical, "file.bin")
	if err := os.WriteFile(physicalPath, []byte("hello world"), 0640); err != nil {
		t.Fatal(err)
	}
	response, err := HandleStat(context.Background(), &gnoi_file_pb.StatRequest{Path: logicalPath})
	if err != nil {
		t.Fatalf("HandleStat: %v", err)
	}
	if len(response.Stats) != 1 {
		t.Fatalf("got %d entries, want 1", len(response.Stats))
	}
	got := response.Stats[0]
	if got.Path != logicalPath || got.Size != 11 || got.Permissions != 640 || got.LastModified == 0 || got.Umask != defaultUmask {
		t.Fatalf("unexpected stat: %+v", got)
	}
}

func TestHandleStatDirectory(t *testing.T) {
	logical, physical := useTempHostRoot(t)
	if err := os.WriteFile(filepath.Join(physical, "file"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(physical, "subdir"), 0755); err != nil {
		t.Fatal(err)
	}
	response, err := HandleStat(context.Background(), &gnoi_file_pb.StatRequest{Path: logical})
	if err != nil {
		t.Fatalf("HandleStat: %v", err)
	}
	if len(response.Stats) != 2 {
		t.Fatalf("got %d entries, want file and subdir", len(response.Stats))
	}
	seen := map[string]uint64{}
	for _, stat := range response.Stats {
		seen[filepath.Base(stat.Path)] = stat.Size
	}
	if seen["file"] != 1 || seen["subdir"] != 0 {
		t.Fatalf("unexpected directory stats: %v", seen)
	}
}

func TestHandleStatFilesystemFailures(t *testing.T) {
	logical, _ := useTempHostRoot(t)
	_, err := HandleStat(context.Background(), &gnoi_file_pb.StatRequest{Path: filepath.Join(logical, "missing")})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("missing file code = %v, want NotFound", status.Code(err))
	}

	previous := fsStat
	fsStat = func(path string) (os.FileInfo, error) {
		return nil, &os.PathError{Op: "stat", Path: path, Err: os.ErrPermission}
	}
	t.Cleanup(func() { fsStat = previous })
	_, err = HandleStat(context.Background(), &gnoi_file_pb.StatRequest{Path: "/tmp/denied"})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("permission error code = %v, want PermissionDenied", status.Code(err))
	}
}
