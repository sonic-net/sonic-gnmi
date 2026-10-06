package gnmi

import (
	"context"
	"errors"
	"testing"

	debugpb "github.com/openconfig/gnoi/debug"
	"github.com/sonic-net/sonic-gnmi/common_utils"
	debugservice "github.com/sonic-net/sonic-gnmi/pkg/gnoi/debug"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type debugAuthStream struct {
	debugpb.Debug_DebugServer
	ctx context.Context
}

func (s *debugAuthStream) Context() context.Context {
	return s.ctx
}

func TestDebugAccessLevel(t *testing.T) {
	tests := []struct {
		name     string
		auth     common_utils.AuthInfo
		want     debugservice.AccessLevel
		wantCode codes.Code
	}{
		{
			name:     "authentication disabled",
			auth:     common_utils.AuthInfo{User: "local", Roles: []string{"admin"}},
			wantCode: codes.Unauthenticated,
		},
		{
			name:     "missing principal",
			auth:     common_utils.AuthInfo{AuthEnabled: true, Roles: []string{"admin"}},
			wantCode: codes.Unauthenticated,
		},
		{
			name:     "missing role",
			auth:     common_utils.AuthInfo{AuthEnabled: true, User: "operator"},
			wantCode: codes.PermissionDenied,
		},
		{
			name: "unrelated role",
			auth: common_utils.AuthInfo{
				AuthEnabled: true,
				User:        "operator",
				Roles:       []string{"gnmi_readwrite"},
			},
			wantCode: codes.PermissionDenied,
		},
		{
			name: "gnoi read only",
			auth: common_utils.AuthInfo{
				AuthEnabled: true,
				User:        "operator",
				Roles:       []string{"gnoi_readonly"},
			},
			wantCode: codes.PermissionDenied,
		},
		{
			name: "gnoi read write",
			auth: common_utils.AuthInfo{
				AuthEnabled: true,
				User:        "operator",
				Roles:       []string{"gnoi_readwrite"},
			},
			wantCode: codes.PermissionDenied,
		},
		{
			name: "admin",
			auth: common_utils.AuthInfo{
				AuthEnabled: true,
				User:        "admin-user",
				Roles:       []string{"admin"},
			},
			want: debugservice.AccessReadWrite,
		},
		{
			name: "admin role must be exact",
			auth: common_utils.AuthInfo{
				AuthEnabled: true,
				User:        "admin-user",
				Roles:       []string{" admin "},
			},
			wantCode: codes.PermissionDenied,
		},
		{
			name: "admin role is case sensitive",
			auth: common_utils.AuthInfo{
				AuthEnabled: true,
				User:        "admin-user",
				Roles:       []string{"Admin"},
			},
			wantCode: codes.PermissionDenied,
		},
		{
			name: "noaccess dominates later admin",
			auth: common_utils.AuthInfo{
				AuthEnabled: true,
				User:        "admin-user",
				Roles:       []string{"gnoi_noaccess", "admin"},
			},
			wantCode: codes.PermissionDenied,
		},
		{
			name: "noaccess dominates earlier admin",
			auth: common_utils.AuthInfo{
				AuthEnabled: true,
				User:        "admin-user",
				Roles:       []string{"admin", "gnoi_noaccess"},
			},
			wantCode: codes.PermissionDenied,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := debugAccessLevel(&tt.auth)
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

func TestDebugAuthenticatesOnce(t *testing.T) {
	originalAuthenticate := authenticateDebug
	originalHandle := handleDebugRequest
	t.Cleanup(func() {
		authenticateDebug = originalAuthenticate
		handleDebugRequest = originalHandle
	})

	authCalls := 0
	authenticateDebug = func(config *Config, ctx context.Context, target string, writeAccess bool) (context.Context, error) {
		authCalls++
		if target != "gnoi" || writeAccess {
			t.Fatalf("unexpected authorization request: target=%q write=%v", target, writeAccess)
		}
		rc, authenticatedCtx := common_utils.GetContext(ctx)
		rc.Auth = common_utils.AuthInfo{
			AuthEnabled: true,
			User:        "admin-user",
			Roles:       []string{"admin"},
		}
		return authenticatedCtx, nil
	}

	handlerErr := errors.New("handler sentinel")
	handleCalls := 0
	handleDebugRequest = func(req *debugpb.DebugRequest, stream debugpb.Debug_DebugServer, policy *debugservice.Policy, access debugservice.AccessLevel) error {
		handleCalls++
		if access != debugservice.AccessReadWrite {
			t.Fatalf("access = %v, want read-write", access)
		}
		rc, _ := common_utils.GetContext(stream.Context())
		if rc.Auth.User != "admin-user" {
			t.Fatalf("authenticated principal not propagated: %+v", rc.Auth)
		}
		return handlerErr
	}

	srv := &DebugServer{
		Server: &Server{config: &Config{}},
		policy: debugservice.NewUnavailablePolicy(errors.New("not used")),
	}
	err := srv.Debug(
		&debugpb.DebugRequest{Mode: debugpb.DebugRequest_MODE_CLI, Command: []byte("uptime")},
		&debugAuthStream{ctx: context.Background()},
	)
	if !errors.Is(err, handlerErr) {
		t.Fatalf("Debug() error = %v, want handler sentinel", err)
	}
	if authCalls != 1 {
		t.Fatalf("authentication calls = %d, want 1", authCalls)
	}
	if handleCalls != 1 {
		t.Fatalf("handler calls = %d, want 1", handleCalls)
	}
}

func TestDebugDeniesBeforeHandler(t *testing.T) {
	originalAuthenticate := authenticateDebug
	originalHandle := handleDebugRequest
	t.Cleanup(func() {
		authenticateDebug = originalAuthenticate
		handleDebugRequest = originalHandle
	})

	authenticateDebug = func(config *Config, ctx context.Context, target string, writeAccess bool) (context.Context, error) {
		rc, authenticatedCtx := common_utils.GetContext(ctx)
		rc.Auth = common_utils.AuthInfo{
			AuthEnabled: true,
			User:        "operator",
			Roles:       []string{"gnoi_readwrite"},
		}
		return authenticatedCtx, nil
	}
	handleDebugRequest = func(req *debugpb.DebugRequest, stream debugpb.Debug_DebugServer, policy *debugservice.Policy, access debugservice.AccessLevel) error {
		t.Fatal("handler called after authorization denial")
		return nil
	}

	srv := &DebugServer{
		Server: &Server{config: &Config{}},
		policy: debugservice.NewUnavailablePolicy(errors.New("not used")),
	}
	err := srv.Debug(
		&debugpb.DebugRequest{Mode: debugpb.DebugRequest_MODE_CLI, Command: []byte("uptime")},
		&debugAuthStream{ctx: context.Background()},
	)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("status code = %v, want PermissionDenied: %v", status.Code(err), err)
	}
}
