package gnmi

import (
	"context"
	"crypto/tls"
	"github.com/openconfig/gnoi/file"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"testing"
)

// TestFileServer tests implementation of gnoi.File server.
func TestFileServer(t *testing.T) {
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.Stop()
	targetAddr := "127.0.0.1:8081"
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}
	conn, err := grpc.Dial(targetAddr, opts...)
	if err != nil {
		t.Fatalf("Dialing to %s failed: %v", targetAddr, err)
	}
	defer conn.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sc := file.NewFileClient(conn)

	t.Run("GetFailsAsUnimplemented", func(t *testing.T) {
		stream, err := sc.Get(ctx, &file.GetRequest{})
		if err != nil {
			t.Error(err.Error())
		}
		if stream == nil {
			t.Fatal("stream is nil, possibly due to connection or TLS error")
		}
		_, err = stream.Recv()
		testErr(err, codes.Unimplemented, "Method file.Get is unimplemented.", t)
	})
	t.Run("TransferToRemoteFailsAsUnimplemented", func(t *testing.T) {
		_, err := sc.TransferToRemote(ctx, &file.TransferToRemoteRequest{})
		testErr(err, codes.Unimplemented, "Method file.TransferToRemote is unimplemented.", t)
	})
	t.Run("PutFailsAsUnimplemented", func(t *testing.T) {
		stream, err := sc.Put(ctx)
		if err != nil {
			t.Error(err.Error())
		}
		_, err = stream.CloseAndRecv()
		testErr(err, codes.Unimplemented, "Method file.Put is unimplemented.", t)
	})
	t.Run("RemoveFailsIfRemoteFileMissing", func(t *testing.T) {
		req := &file.RemoveRequest{}
		_, err := sc.Remove(ctx, req)
		testErr(err, codes.InvalidArgument, "Invalid request: remote_file field is empty.", t)
	})
	t.Run("RemoveFailsIfRequestIsNil", func(t *testing.T) {
		srv := &FileServer{}
		_, err := srv.Remove(context.Background(), nil)
		testErr(err, codes.InvalidArgument, "Invalid nil request.", t)
	})
}
