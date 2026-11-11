//go:build e2e

package testutil

import (
	"crypto/tls"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

type RunningServer struct{ Address string }

// startServer owns transport only. Registration and business dependencies belong
// to the caller; no service or interceptor is implicitly installed.
func StartServer(t *testing.T, tlsConfig *tls.Config, register func(*grpc.Server), options ...grpc.ServerOption) *RunningServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	check(t, err)
	server := grpc.NewServer(append(options, grpc.Creds(credentials.NewTLS(tlsConfig)))...)
	done := make(chan error, 1)
	t.Cleanup(func() {
		server.Stop()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("server shutdown: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("server failed to stop")
		}
		t.Log("group cleanup: gRPC server stopped")
	})
	register(server)
	go func() { done <- server.Serve(listener) }()
	return &RunningServer{Address: listener.Addr().String()}
}

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
