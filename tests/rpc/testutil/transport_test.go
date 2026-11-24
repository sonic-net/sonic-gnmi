//go:build e2e

package testutil

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func TestMutualTLSLifecycle(t *testing.T) {
	var address string
	t.Run("server", func(t *testing.T) {
		ca := NewCertificateAuthority(t)
		service := health.NewServer()
		service.SetServingStatus("test", healthpb.HealthCheckResponse_SERVING)
		server := StartServer(t, ca.ServerTLS(t), func(s *grpc.Server) {
			healthpb.RegisterHealthServer(s, service)
		})
		address = server.Address
		client := healthpb.NewHealthClient(NewClientConn(t, address, ca.ClientTLS(t, "reader")))
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		response, err := client.Check(ctx, &healthpb.HealthCheckRequest{Service: "test"})
		check(t, err)
		if response.GetStatus() != healthpb.HealthCheckResponse_SERVING {
			t.Fatalf("health status = %v, want SERVING", response.GetStatus())
		}

		// Trust the server's CA but present a client signed by an unrelated CA.
		untrusted := NewCertificateAuthority(t).ClientTLS(t, "untrusted")
		untrusted.RootCAs = ca.pool
		badClient := healthpb.NewHealthClient(NewClientConn(t, address, untrusted))
		response, err = badClient.Check(ctx, &healthpb.HealthCheckRequest{Service: "test"})
		if err == nil || response != nil {
			t.Fatalf("untrusted client received a response: %v, %v", response, err)
		}
	})
	if address == "" {
		t.Fatal("server did not start")
	}
	conn, err := net.DialTimeout("tcp", address, time.Second)
	if err == nil {
		conn.Close()
		t.Fatal("listener is still open after server cleanup")
	}
}
