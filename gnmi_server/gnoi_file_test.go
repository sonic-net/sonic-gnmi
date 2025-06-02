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
	s := createServer(t, 8183)
	go runServer(t, s)
	targetAddr := "127.0.0.1:8183"
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
	t.Run("StatFailsAsUnimplemented", func(t *testing.T) {
		_, err := sc.Stat(ctx, &file.StatRequest{})
		testErr(err, codes.Unimplemented, "Method file.Stat is unimplemented.", t)
	})
	t.Run("RemoveFailsIfRemoteFileMissing", func(t *testing.T) {
		req := &file.RemoveRequest{}
		_, err := sc.Remove(ctx, req)
		testErr(err, codes.InvalidArgument, "Invalid request: remote_file field is empty.", t)
	})
}
