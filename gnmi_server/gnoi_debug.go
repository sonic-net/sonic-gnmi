package gnmi

import (
	"context"
	"strings"

	log "github.com/golang/glog"
	debugpb "github.com/openconfig/gnoi/debug"
	"github.com/sonic-net/sonic-gnmi/common_utils"
	debugservice "github.com/sonic-net/sonic-gnmi/pkg/gnoi/debug"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	authenticateDebug  = authenticate
	handleDebugRequest = debugservice.HandleCommandRequest
)

const debugAdminRole = "admin"

type authenticatedDebugStream struct {
	debugpb.Debug_DebugServer
	ctx context.Context
}

func (s *authenticatedDebugStream) Context() context.Context {
	return s.ctx
}

func (srv *DebugServer) Debug(req *debugpb.DebugRequest, stream debugpb.Debug_DebugServer) error {
	ctx, err := authenticateDebug(srv.config, stream.Context(), "gnoi", false)
	if err != nil {
		log.Errorf("authentication failed in Debug RPC: %v", err)
		return err
	}

	rc, ctx := common_utils.GetContext(ctx)
	access, err := debugAccessLevel(&rc.Auth)
	if err != nil {
		log.Errorf("authorization failed in Debug RPC for user %q", rc.Auth.User)
		return err
	}

	log.Infof("gNOI Debug RPC called by %q", rc.Auth.User)
	return handleDebugRequest(req, &authenticatedDebugStream{
		Debug_DebugServer: stream,
		ctx:               ctx,
	}, srv.policy, access)
}

func debugAccessLevel(auth *common_utils.AuthInfo) (debugservice.AccessLevel, error) {
	if auth == nil || !auth.AuthEnabled || strings.TrimSpace(auth.User) == "" {
		return 0, status.Error(codes.Unauthenticated, "Debug requires an authenticated principal")
	}

	access := resolveTargetRoleAccess(auth, "gnoi")
	if access.noAccess {
		return 0, status.Error(codes.PermissionDenied, "Debug access is denied")
	}
	for _, role := range auth.Roles {
		if role == debugAdminRole {
			return debugservice.AccessReadWrite, nil
		}
	}
	return 0, status.Error(codes.PermissionDenied, "Debug requires the admin role")
}
