package gnmi

import (
	"bytes"
	"context"
	"crypto/md5"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	filepb "github.com/openconfig/gnoi/file"
	"github.com/openconfig/gnoi/types"
	gnoifile "github.com/sonic-net/sonic-gnmi/pkg/gnoi/file"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func fileTestClient(t *testing.T, server *FileServer, uds bool) filepb.FileClient {
	t.Helper()
	buffer := bufconn.Listen(1024 * 1024)
	var listener net.Listener = buffer
	dial := func(context.Context, string) (net.Conn, error) { return buffer.Dial() }
	if uds {
		buffer.Close()
		var err error
		listener, err = net.Listen("unix", filepath.Join(t.TempDir(), "file.sock"))
		if err != nil {
			t.Fatal(err)
		}
		dial = func(ctx context.Context, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", listener.Addr().String())
		}
	}
	s := grpc.NewServer()
	filepb.RegisterFileServer(s, server)
	go func() { _ = s.Serve(listener) }()
	t.Cleanup(s.Stop)
	conn, err := grpc.NewClient("passthrough:///file-test",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(dial))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return filepb.NewFileClient(conn)
}

// Exercise the entire service with real files and gRPC streams, without a
// Server, Config, other service, or process-wide function patch.
func TestFileIndependentService(t *testing.T) {
	allow := func(ctx context.Context) (context.Context, error) { return ctx, nil }
	server := &FileServer{
		authorizeRead: allow, authorizeWrite: allow,
		stat: gnoifile.HandleStat, get: gnoifile.HandleGet,
		put: gnoifile.HandlePut, remove: gnoifile.HandleFileRemove,
	}
	verifyFileService(t, fileTestClient(t, server, false))
}

func TestFileProductionServiceUDS(t *testing.T) {
	verifyFileService(t, fileTestClient(t, newFileServer(&Config{}), true))
}

func verifyFileService(t *testing.T, client filepb.FileClient) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Public read access checks every ancestor. Use a public temp directory
	// directly under /tmp rather than changing testing.T's private ancestors.
	root := ""
	if info, err := os.Stat("/mnt/host"); err == nil && info.IsDir() {
		root = "/mnt/host"
	}
	dir, err := os.MkdirTemp(root+"/tmp", "file-service-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	physicalPath := filepath.Join(dir, "payload")
	path := strings.TrimPrefix(physicalPath, root)
	data := bytes.Repeat([]byte("file DI payload"), 10000)
	digest := md5.Sum(data)
	put := func(path string, hash []byte) error {
		stream, err := client.Put(ctx)
		if err != nil {
			return err
		}
		for _, req := range []*filepb.PutRequest{
			{Request: &filepb.PutRequest_Open{Open: &filepb.PutRequest_Details{RemoteFile: path, Permissions: 0644}}},
			{Request: &filepb.PutRequest_Contents{Contents: data}},
			{Request: &filepb.PutRequest_Hash{Hash: &types.HashType{Method: types.HashType_MD5, Hash: hash}}},
		} {
			if err := stream.Send(req); err != nil {
				return err
			}
		}
		_, err = stream.CloseAndRecv()
		return err
	}
	if err := put(path, digest[:]); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(physicalPath)
	if err != nil || !bytes.Equal(actual, data) {
		t.Fatalf("uploaded file mismatch: %v", err)
	}
	stat, err := client.Stat(ctx, &filepb.StatRequest{Path: path})
	if err != nil || len(stat.GetStats()) != 1 || stat.Stats[0].Size != uint64(len(data)) || stat.Stats[0].Permissions != 644 {
		t.Fatalf("Stat() = %v, %v", stat, err)
	}
	stream, err := client.Get(ctx, &filepb.GetRequest{RemoteFile: path})
	if err != nil {
		t.Fatal(err)
	}
	var downloaded []byte
	var finalHash *types.HashType
	chunks := 0
	for {
		response, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if finalHash != nil {
			t.Fatal("response received after final hash")
		}
		if chunk := response.GetContents(); chunk != nil {
			if len(chunk) > 64*1024 {
				t.Fatalf("oversized chunk: %d", len(chunk))
			}
			downloaded = append(downloaded, chunk...)
			chunks++
		} else {
			finalHash = response.GetHash()
		}
	}
	if !bytes.Equal(downloaded, data) || chunks < 2 || finalHash == nil || finalHash.Method != types.HashType_MD5 || !bytes.Equal(finalHash.Hash, digest[:]) {
		t.Fatal("Get contents/chunks/final hash mismatch")
	}
	if err := put(path, []byte("bad hash")); status.Code(err) != codes.DataLoss {
		t.Fatalf("bad upload: %v", err)
	}
	actual, err = os.ReadFile(physicalPath)
	if err != nil || !bytes.Equal(actual, data) {
		t.Fatalf("failed upload changed existing file: %v", err)
	}
	if _, err := os.Stat(physicalPath + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("failed upload left temporary file: %v", err)
	}
	if err := os.Chmod(physicalPath, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Stat(ctx, &filepb.StatRequest{Path: path}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("private file Stat: %v", err)
	}
	if _, err := client.Stat(ctx, &filepb.StatRequest{Path: "/mnt/host/tmp/file"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("container-internal path: %v", err)
	}
	if _, err := client.TransferToRemote(ctx, &filepb.TransferToRemoteRequest{}); status.Code(err) != codes.Unimplemented {
		t.Fatalf("TransferToRemote: %v", err)
	}
	if _, err := client.Remove(ctx, &filepb.RemoveRequest{RemoteFile: path}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(physicalPath); !os.IsNotExist(err) {
		t.Fatalf("Remove left file: %v", err)
	}
	t.Log("File service verified: real Put/Stat/multi-chunk Get/Remove, hash-failure cleanup, private/path denial, unsupported transfer")
}

type fileUnreadPutStream struct{ filepb.File_PutServer }

func (*fileUnreadPutStream) Context() context.Context { return context.Background() }
func (*fileUnreadPutStream) Recv() (*filepb.PutRequest, error) {
	panic("denied Put must not receive client data")
}

type fileUnsentGetStream struct{ filepb.File_GetServer }

func (*fileUnsentGetStream) Context() context.Context { return context.Background() }
func (*fileUnsentGetStream) Send(*filepb.GetResponse) error {
	panic("denied Get must not send data")
}

func TestFileIndependentDenialBeforeEffects(t *testing.T) {
	readCalls, writeCalls := 0, 0
	denied := status.Error(codes.PermissionDenied, "denied")
	server := &FileServer{
		authorizeRead: func(ctx context.Context) (context.Context, error) {
			readCalls++
			return ctx, denied
		},
		authorizeWrite: func(ctx context.Context) (context.Context, error) {
			writeCalls++
			return ctx, denied
		},
		// Nil operations make any file access before denial fail the test.
	}
	ctx := context.Background()
	_, statErr := server.Stat(ctx, &filepb.StatRequest{})
	getErr := server.Get(&filepb.GetRequest{}, &fileUnsentGetStream{})
	putErr := server.Put(&fileUnreadPutStream{})
	_, removeErr := server.Remove(ctx, &filepb.RemoveRequest{})
	_, transferErr := server.TransferToRemote(ctx, &filepb.TransferToRemoteRequest{})
	for name, err := range map[string]error{"Stat": statErr, "Get": getErr, "Put": putErr, "Remove": removeErr, "TransferToRemote": transferErr} {
		if err != denied {
			t.Fatalf("%s: %v, want original auth denial", name, err)
		}
	}
	if readCalls != 2 || writeCalls != 3 {
		t.Fatalf("authorization calls: read=%d write=%d", readCalls, writeCalls)
	}
	if _, err := server.Remove(ctx, nil); status.Code(err) != codes.InvalidArgument || writeCalls != 3 {
		t.Fatalf("Remove(nil) ordering changed: %v, write calls=%d", err, writeCalls)
	}
}
