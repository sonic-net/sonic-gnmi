package gnmi

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const (
	gnmiClientCertTable = "GNMI_CLIENT_CERT"
	WriteAccessMode     = "readwrite"
	ReadOnlyMode        = "readonly"
	NoAccessMode        = "noaccess"
)

type rpcAuthorization struct {
	target             string
	write              bool
	requestTarget      bool
	bufferFirstRequest bool
}

// gRPC descriptors identify methods and streaming shape, but do not describe
// whether an RPC reads or changes device state. Keep that decision in one
// explicit registry and deny methods that are not classified here.
var rpcAuthorizations = map[string]rpcAuthorization{
	"/gnmi.gNMI/Capabilities": {target: "gnmi"},
	"/gnmi.gNMI/Get":          {target: "gnmi", requestTarget: true},
	"/gnmi.gNMI/Set":          {target: "gnmi", write: true, requestTarget: true},
	"/gnmi.gNMI/Subscribe":    {target: "gnmi", requestTarget: true, bufferFirstRequest: true},

	"/gnoi.sonic.Debug/GetSubscribePreferences":  {target: "gnmi"},
	"/gnoi.sonic.SonicService/ClearNeighbors":    {target: "gnoi", write: true},
	"/gnoi.sonic.SonicService/CopyConfig":        {target: "gnoi", write: true},
	"/gnoi.sonic.SonicService/ImageDefault":      {target: "gnoi", write: true},
	"/gnoi.sonic.SonicService/ImageInstall":      {target: "gnoi", write: true},
	"/gnoi.sonic.SonicService/ImageRemove":       {target: "gnoi", write: true},
	"/gnoi.sonic.SonicService/ShowTechsupport":   {target: "gnoi"},
	"/gnoi.sonic_jwt.SonicJwtService/Refresh":    {target: "gnoi", write: true},
	"/gnoi.containerz.Containerz/Deploy":         {target: "gnoi", write: true},
	"/gnoi.factory_reset.FactoryReset/Start":     {target: "gnoi", write: true},
	"/gnoi.file.File/Get":                        {target: "gnoi"},
	"/gnoi.file.File/Put":                        {target: "gnoi", write: true},
	"/gnoi.file.File/Remove":                     {target: "gnoi", write: true},
	"/gnoi.file.File/Stat":                       {target: "gnoi"},
	"/gnoi.file.File/TransferToRemote":           {target: "gnoi", write: true},
	"/gnoi.healthz.Healthz/Acknowledge":          {target: "gnoi", write: true},
	"/gnoi.healthz.Healthz/Artifact":             {target: "gnoi"},
	"/gnoi.healthz.Healthz/Get":                  {target: "gnoi"},
	"/gnoi.os.OS/Activate":                       {target: "gnoi", write: true},
	"/gnoi.os.OS/Install":                        {target: "gnoi", write: true},
	"/gnoi.os.OS/Verify":                         {target: "gnoi"},
	"/gnoi.system.System/CancelReboot":           {target: "gnoi", write: true},
	"/gnoi.system.System/KillProcess":            {target: "gnoi", write: true},
	"/gnoi.system.System/Ping":                   {target: "gnoi", write: true},
	"/gnoi.system.System/Reboot":                 {target: "gnoi", write: true},
	"/gnoi.system.System/RebootStatus":           {target: "gnoi"},
	"/gnoi.system.System/SetPackage":             {target: "gnoi", write: true},
	"/gnoi.system.System/SwitchControlProcessor": {target: "gnoi", write: true},
	"/gnoi.system.System/Time":                   {target: "gnoi"},
	"/gnoi.system.System/Traceroute":             {target: "gnoi", write: true},
	"/sonic.gnoi.oras.v1.Oras/Pull":              {target: "gnoi", write: true},
}

type rpcAuthorizationInterceptor struct {
	authenticate func(context.Context) (context.Context, *rpcIdentity, error)
	bypass       func(context.Context) bool
}

type rpcIdentity struct {
	user  string
	roles []string
}

type rpcAuthorizationContextKey struct{}

type rpcAuthorizationRoute uint8

const (
	rpcAuthorizationRouteDefault rpcAuthorizationRoute = iota
	rpcAuthorizationRouteSetBypassOnly
)

type rpcAuthorizationTarget struct {
	target string
	route  rpcAuthorizationRoute
}

type rpcAuthorizationDecision struct {
	authorization rpcAuthorization
	target        string
	route         rpcAuthorizationRoute
}

func rpcAuthorizationMode(authModes map[string]bool, configTableName string) (bool, string) {
	if authModes["none"] || !authModes["cert"] {
		return false, ""
	}
	if configTableName != gnmiClientCertTable {
		return false, "central RPC authorization supports cert authentication only with GNMI_CLIENT_CERT"
	}
	for mode, enabled := range authModes {
		if enabled && mode != "cert" {
			return false, "central RPC authorization does not support mixed authentication modes"
		}
	}
	return true, ""
}

func newRPCAuthorizationInterceptor(
	authenticate func(context.Context) (context.Context, *rpcIdentity, error),
	bypass func(context.Context) bool,
) *rpcAuthorizationInterceptor {
	return &rpcAuthorizationInterceptor{
		authenticate: authenticate,
		bypass:       bypass,
	}
}

func (i *rpcAuthorizationInterceptor) authenticateRPC(
	ctx context.Context,
	fullMethod string,
) (context.Context, *rpcIdentity, rpcAuthorization, error) {
	authorization, ok := rpcAuthorizations[fullMethod]
	if !ok {
		return ctx, nil, rpcAuthorization{}, status.Error(codes.PermissionDenied, "RPC access denied")
	}

	authenticatedCtx, auth, err := i.authenticate(ctx)
	if err != nil {
		return authenticatedCtx, nil, rpcAuthorization{}, err
	}
	if auth == nil {
		return authenticatedCtx, nil, rpcAuthorization{}, status.Error(codes.PermissionDenied, "RPC access denied")
	}
	return authenticatedCtx, auth, authorization, nil
}

func authorizeRPCTarget(
	ctx context.Context,
	auth *rpcIdentity,
	authorization rpcAuthorization,
	requestTarget rpcAuthorizationTarget,
) (context.Context, error) {
	target := requestTarget.target
	if target == "" {
		target = authorization.target
	}
	access := resolveAuthorizationTargetRoleAccess(auth.roles, target)
	if checkResolvedRoleAccess(auth.user, target, authorization.write, access, true) != nil {
		return ctx, status.Error(codes.PermissionDenied, "RPC access denied")
	}

	decision := rpcAuthorizationDecision{
		authorization: authorization,
		target:        normalizeAuthorizationTarget(target),
		route:         requestTarget.route,
	}
	return context.WithValue(ctx, rpcAuthorizationContextKey{}, decision), nil
}

func (i *rpcAuthorizationInterceptor) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if i.bypass != nil && i.bypass(ctx) {
			return handler(ctx, req)
		}

		authenticatedCtx, auth, authorization, err := i.authenticateRPC(ctx, info.FullMethod)
		if err != nil {
			return nil, err
		}
		requestTarget := rpcAuthorizationTarget{target: authorization.target}
		if authorization.requestTarget {
			requestTarget, err = rpcAuthorizationRequestTarget(authenticatedCtx, info.FullMethod, req)
			if err != nil {
				return nil, err
			}
		}
		authorizedCtx, err := authorizeRPCTarget(authenticatedCtx, auth, authorization, requestTarget)
		if err != nil {
			return nil, err
		}
		return handler(authorizedCtx, req)
	}
}

func (i *rpcAuthorizationInterceptor) StreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if i.bypass != nil && i.bypass(stream.Context()) {
			return handler(srv, stream)
		}

		authenticatedCtx, auth, authorization, err := i.authenticateRPC(stream.Context(), info.FullMethod)
		if err != nil {
			return err
		}
		requestTarget := rpcAuthorizationTarget{target: authorization.target}
		var bufferedRequest proto.Message
		if authorization.requestTarget {
			if !authorization.bufferFirstRequest {
				return status.Error(codes.Internal, "RPC target resolver is not configured")
			}
			if !authorizationRoleCouldAccessService(auth.roles, authorization.target, authorization.write) {
				return status.Error(codes.PermissionDenied, "RPC access denied")
			}
			bufferedRequest = rpcAuthorizationFirstStreamRequest(info.FullMethod)
			if bufferedRequest == nil {
				return status.Error(codes.Internal, "RPC target resolver is not configured")
			}
			if err := stream.RecvMsg(bufferedRequest); err != nil {
				if err == io.EOF {
					return status.Error(codes.Aborted, "stream EOF received before init")
				}
				return status.Error(status.Code(err), "received error from client")
			}
			requestTarget, err = rpcAuthorizationRequestTarget(authenticatedCtx, info.FullMethod, bufferedRequest)
			if err != nil {
				return err
			}
		}
		authorizedCtx, err := authorizeRPCTarget(authenticatedCtx, auth, authorization, requestTarget)
		if err != nil {
			return err
		}
		return handler(srv, &rpcAuthorizationServerStream{
			ServerStream: stream,
			ctx:          authorizedCtx,
			buffered:     bufferedRequest,
		})
	}
}

type rpcAuthorizationServerStream struct {
	grpc.ServerStream
	ctx      context.Context
	buffered proto.Message
}

func (s *rpcAuthorizationServerStream) Context() context.Context {
	return s.ctx
}

func (s *rpcAuthorizationServerStream) RecvMsg(message interface{}) error {
	if s.buffered == nil {
		return s.ServerStream.RecvMsg(message)
	}

	destination, ok := message.(proto.Message)
	if !ok || reflect.TypeOf(destination) != reflect.TypeOf(s.buffered) {
		return status.Error(codes.Internal, "buffered RPC request type mismatch")
	}
	proto.Reset(destination)
	proto.Merge(destination, s.buffered)
	s.buffered = nil
	return nil
}

func rpcAuthorizationFromContext(ctx context.Context) (rpcAuthorizationDecision, bool) {
	decision, ok := ctx.Value(rpcAuthorizationContextKey{}).(rpcAuthorizationDecision)
	return decision, ok
}

func rpcAuthorizationCompleted(ctx context.Context) bool {
	_, authorized := rpcAuthorizationFromContext(ctx)
	return authorized
}

func rpcAuthorizationSetBypassOnly(ctx context.Context) bool {
	decision, authorized := rpcAuthorizationFromContext(ctx)
	return authorized && decision.route == rpcAuthorizationRouteSetBypassOnly
}

func checkCompletedRPCAuthorization(ctx context.Context, user string, roles []string, target string) error {
	decision, ok := rpcAuthorizationFromContext(ctx)
	if !ok || decision.target != normalizeAuthorizationTarget(target) {
		return status.Error(codes.PermissionDenied, "RPC access denied")
	}

	access := resolveAuthorizationTargetRoleAccess(roles, target)
	if checkResolvedRoleAccess(user, target, decision.authorization.write, access, true) != nil {
		return status.Error(codes.PermissionDenied, "RPC access denied")
	}
	return nil
}

func checkResolvedRoleAccess(user, target string, writeAccess bool, access targetRoleAccess, requireRole bool) error {
	if access.noAccessRole != "" {
		return fmt.Errorf("%s does not have access, target %s, role %s", user, target, access.noAccessRole)
	}
	if writeAccess && !access.readWrite {
		if access.readOnlyRole != "" {
			return fmt.Errorf("%s does not have access, target %s, role %s", user, target, access.readOnlyRole)
		}
		return fmt.Errorf("%s does not have write access, target %s", user, target)
	}
	if requireRole && access.readOnlyRole == "" && !access.readWrite {
		return fmt.Errorf("%s does not have read access, target %s", user, target)
	}
	return nil
}

type targetRoleAccess struct {
	noAccessRole string
	readOnlyRole string
	readWrite    bool
}

func resolveTargetRoleAccess(roles []string, target string) targetRoleAccess {
	var access targetRoleAccess
	prefix := strings.ToLower(target) + "_"
	for _, role := range roles {
		role = strings.TrimSpace(role)
		switch role {
		case prefix + NoAccessMode:
			access.noAccessRole = role
		case prefix + ReadOnlyMode:
			access.readOnlyRole = role
		case prefix + WriteAccessMode:
			access.readWrite = true
		}
	}
	return access
}

func normalizeAuthorizationTarget(target string) string {
	return strings.ToLower(strings.TrimSpace(target))
}

func authorizationServiceTarget(target string) string {
	target = normalizeAuthorizationTarget(target)
	if separator := strings.IndexByte(target, '_'); separator >= 0 {
		return target[:separator]
	}
	return target
}

func resolveAuthorizationTargetRoleAccess(roles []string, target string) targetRoleAccess {
	access := resolveTargetRoleAccess(roles, target)
	serviceTarget := authorizationServiceTarget(target)
	if serviceTarget == normalizeAuthorizationTarget(target) {
		return access
	}

	serviceAccess := resolveTargetRoleAccess(roles, serviceTarget)
	if serviceAccess.noAccessRole != "" {
		access.noAccessRole = serviceAccess.noAccessRole
	}
	return access
}

func authorizationRoleCouldAccessService(roles []string, serviceTarget string, writeAccess bool) bool {
	serviceTarget = normalizeAuthorizationTarget(serviceTarget)
	if resolveTargetRoleAccess(roles, serviceTarget).noAccessRole != "" {
		return false
	}

	accessByTarget := make(map[string]targetRoleAccess)
	for _, role := range roles {
		target, mode, ok := splitAuthorizationRole(role)
		if !ok ||
			!validAuthorizationRoleTarget(target, serviceTarget) ||
			authorizationServiceTarget(target) != serviceTarget {
			continue
		}
		access := accessByTarget[target]
		switch mode {
		case NoAccessMode:
			access.noAccessRole = role
		case WriteAccessMode:
			access.readWrite = true
		case ReadOnlyMode:
			access.readOnlyRole = role
		}
		accessByTarget[target] = access
	}

	for _, access := range accessByTarget {
		if access.noAccessRole != "" {
			continue
		}
		if access.readWrite || (!writeAccess && access.readOnlyRole != "") {
			return true
		}
	}
	return false
}

func validAuthorizationRoleTarget(target, serviceTarget string) bool {
	normalizedTarget := normalizeAuthorizationTarget(target)
	if target != normalizedTarget {
		return false
	}
	target = normalizedTarget
	serviceTarget = normalizeAuthorizationTarget(serviceTarget)
	if target == serviceTarget {
		return true
	}
	scope, ok := strings.CutPrefix(target, serviceTarget+"_")
	if !ok || scope == "" {
		return false
	}
	for _, segment := range strings.Split(scope, "_") {
		if segment == "" {
			return false
		}
	}
	return true
}

func splitAuthorizationRole(role string) (string, string, bool) {
	role = strings.TrimSpace(role)
	for _, mode := range []string{NoAccessMode, ReadOnlyMode, WriteAccessMode} {
		suffix := "_" + mode
		if strings.HasSuffix(role, suffix) && len(role) > len(suffix) {
			return strings.TrimSuffix(role, suffix), mode, true
		}
	}
	return "", "", false
}
