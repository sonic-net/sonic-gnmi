//go:build e2e

package testutil

import (
	"context"
	"net"
	"os"
	"os/exec"
	"strings"
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

func TestServerRegistrationExit(t *testing.T) {
	if mode := os.Getenv("RPC_REGISTER_EXIT"); mode != "" {
		ca := NewCertificateAuthority(t)
		StartServer(t, ca.ServerTLS(t), func(*grpc.Server) {
			if mode == "skip" {
				t.Skip("registration skipped")
			}
			t.Fatal("registration failed")
		})
		return
	}
	for _, mode := range []string{"skip", "fatal"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestServerRegistrationExit$", "-test.v")
			cmd.Env = append(os.Environ(), "RPC_REGISTER_EXIT="+mode)
			output, err := cmd.CombinedOutput()
			if ctx.Err() != nil || strings.Contains(string(output), "server failed to stop") || (mode == "skip" && err != nil) || (mode == "fatal" && err == nil) {
				t.Fatalf("registration cleanup: %v: %s", err, output)
			}
		})
	}
}

func TestServerImmediateCleanup(t *testing.T) {
	ca := NewCertificateAuthority(t)
	for i := 0; i < 20; i++ {
		t.Run("immediate", func(t *testing.T) {
			StartServer(t, ca.ServerTLS(t), func(*grpc.Server) {})
		})
	}
}
