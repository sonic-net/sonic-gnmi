package file

import (
	"context"
	"crypto/md5"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/openconfig/gnoi/common"
	gnoi_file_pb "github.com/openconfig/gnoi/file"
	"github.com/sonic-net/sonic-gnmi/internal/imageexport"
	"github.com/sonic-net/sonic-gnmi/pkg/interceptors/dpuproxy"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
)

type managedImagePutServer struct {
	gnoi_file_pb.UnimplementedFileServer
}

func (*managedImagePutServer) Put(stream gnoi_file_pb.File_PutServer) error {
	return HandlePut(stream)
}

func TestRoutedDPUTransferUsesManagedAndLegacyDefaultPermissions(t *testing.T) {
	store := realImageStore(t)
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	gnoi_file_pb.RegisterFileServer(server, &managedImagePutServer{})
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	defer func() {
		server.Stop()
		listener.Close()
		if err := <-serveErr; err != nil && err != grpc.ErrServerStopped {
			t.Error(err)
		}
	}()
	conn, err := grpc.NewClient("passthrough:///test-dpu",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	patches := gomonkey.NewPatches()
	defer patches.Reset()
	routed := false
	patches.ApplyFunc(dpuproxy.GetDPUConnection, func(_ context.Context, index string) (*grpc.ClientConn, error) {
		if index != "0" {
			t.Fatalf("unexpected DPU index: %q", index)
		}
		routed = true
		return conn, nil
	})
	content := []byte("routed DPU image")
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(content)
	}))
	defer httpServer.Close()
	for _, path := range []string{imageexport.ExportRoot + "/dpu.bin", "/tmp/dpu.bin"} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		ctx = metadata.NewIncomingContext(ctx, metadata.Pairs(
			"x-sonic-ss-target-type", "dpu", "x-sonic-ss-target-index", "0"))
		response, err := HandleTransferToRemote(ctx, &gnoi_file_pb.TransferToRemoteRequest{
			LocalPath:      path,
			RemoteDownload: &common.RemoteDownload{Path: httpServer.URL, Protocol: common.RemoteDownload_HTTP},
		})
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		digest := md5.Sum(content)
		if !routed || string(response.GetHash().GetHash()) != string(digest[:]) {
			t.Fatal("routed transfer returned incorrect digest")
		}
		var source *os.File
		mode := os.FileMode(0600)
		if path == "/tmp/dpu.bin" {
			source, err = os.Open(filepath.Join(store.HostRoot, path))
			mode = 0644
		} else {
			source, err = store.Open(path)
		}
		if err != nil {
			t.Fatal(err)
		}
		info, statErr := source.Stat()
		data, readErr := io.ReadAll(source)
		source.Close()
		if statErr != nil || readErr != nil || info.Mode().Perm() != mode || string(data) != string(content) {
			t.Fatalf("routed publication: info=%v data=%q statError=%v readError=%v", info, data, statErr, readErr)
		}
	}
}
