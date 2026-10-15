package gnmi

import (
	"context"
	"errors"
	"net"
	"testing"

	debugpb "github.com/openconfig/gnoi/debug"
	"github.com/sonic-net/sonic-gnmi/common_utils"
	debugservice "github.com/sonic-net/sonic-gnmi/pkg/gnoi/debug"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

type debugTestStream struct {
	debugpb.Debug_DebugServer
	ctx context.Context
}

func (s *debugTestStream) Context() context.Context {
	return s.ctx
}

func debugPeerContext(addr net.Addr) context.Context {
	return peer.NewContext(context.Background(), &peer.Peer{Addr: addr})
}

func TestDebugAccessLevel(t *testing.T) {
	tests := []struct {
		name     string
		ctx      context.Context
		want     debugservice.AccessLevel
		wantCode codes.Code
	}{
		{
			name:     "missing peer",
			ctx:      context.Background(),
			wantCode: codes.PermissionDenied,
		},
		{
			name: "TCP peer",
			ctx: debugPeerContext(&net.TCPAddr{
				IP:   net.ParseIP("127.0.0.1"),
				Port: 8080,
			}),
			wantCode: codes.PermissionDenied,
		},
		{
			name: "Unix peer",
			ctx: debugPeerContext(&net.UnixAddr{
				Name: "/var/run/gnmi/gnmi.sock",
				Net:  "unix",
			}),
			want: debugservice.AccessReadWrite,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := debugAccessLevel(tt.ctx)
			if status.Code(err) != tt.wantCode {
				t.Fatalf("status code = %v, want %v: %v", status.Code(err), tt.wantCode, err)
			}
			if got != tt.want {
				t.Fatalf("access = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCheckRoleAccessNoAccessDominates(t *testing.T) {
	for _, roles := range [][]string{
		{"gnoi_noaccess", "gnoi_readwrite"},
		{"gnoi_readwrite", "gnoi_noaccess"},
	} {
		auth := &common_utils.AuthInfo{User: "operator", Roles: roles}
		if err := checkRoleAccess(auth, "gnoi", false); err == nil {
			t.Fatalf("read access allowed for roles %q", roles)
		}
		if err := checkRoleAccess(auth, "gnoi", true); err == nil {
			t.Fatalf("write access allowed for roles %q", roles)
		}
	}
}

func TestDebugAllowsUnixSocketPeer(t *testing.T) {
	originalHandle := handleDebugRequest
	t.Cleanup(func() {
		handleDebugRequest = originalHandle
	})

	handlerErr := errors.New("handler sentinel")
	handleCalls := 0
	handleDebugRequest = func(req *debugpb.DebugRequest, stream debugpb.Debug_DebugServer, policy *debugservice.Policy, access debugservice.AccessLevel) error {
		handleCalls++
		if access != debugservice.AccessReadWrite {
			t.Fatalf("access = %v, want read-write", access)
		}
		if !isUnixPeer(stream.Context()) {
			t.Fatal("Unix peer context not propagated")
		}
		return handlerErr
	}

	srv := &DebugServer{
		Server: &Server{config: &Config{}},
		policy: debugservice.NewUnavailablePolicy(errors.New("not used")),
	}
	err := srv.Debug(
		&debugpb.DebugRequest{Mode: debugpb.DebugRequest_MODE_CLI, Command: []byte("uptime")},
		&debugTestStream{ctx: debugPeerContext(&net.UnixAddr{
			Name: "/var/run/gnmi/gnmi.sock",
			Net:  "unix",
		})},
	)
	if !errors.Is(err, handlerErr) {
		t.Fatalf("Debug() error = %v, want handler sentinel", err)
	}
	if handleCalls != 1 {
		t.Fatalf("handler calls = %d, want 1", handleCalls)
	}
}

func TestDebugDeniesNetworkPeerBeforeHandler(t *testing.T) {
	originalHandle := handleDebugRequest
	t.Cleanup(func() {
		handleDebugRequest = originalHandle
	})

	handleDebugRequest = func(req *debugpb.DebugRequest, stream debugpb.Debug_DebugServer, policy *debugservice.Policy, access debugservice.AccessLevel) error {
		t.Fatal("handler called for a network peer")
		return nil
	}

	srv := &DebugServer{
		Server: &Server{config: &Config{}},
		policy: debugservice.NewUnavailablePolicy(errors.New("not used")),
	}
	err := srv.Debug(
		&debugpb.DebugRequest{Mode: debugpb.DebugRequest_MODE_CLI, Command: []byte("uptime")},
		&debugTestStream{ctx: debugPeerContext(&net.TCPAddr{
			IP:   net.ParseIP("127.0.0.1"),
			Port: 8080,
		})},
	)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("status code = %v, want PermissionDenied: %v", status.Code(err), err)
	}
}
