package gnmi

import (
	"context"
	"io"
	"reflect"

	"github.com/sonic-net/sonic-gnmi/internal/rpcauthz"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const (
	gnmiClientCertTable = "GNMI_CLIENT_CERT"
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
	if rpcauthz.CheckAuthorizationTargetAccess(auth.user, auth.roles, target, authorization.write, true) != nil {
		return ctx, status.Error(codes.PermissionDenied, "RPC access denied")
	}

	decision := rpcAuthorizationDecision{
		authorization: authorization,
		target:        rpcauthz.NormalizeTarget(target),
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
			if !rpcauthz.CanAccessService(auth.roles, authorization.target, authorization.write) {
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
	if !ok || decision.target != rpcauthz.NormalizeTarget(target) {
		return status.Error(codes.PermissionDenied, "RPC access denied")
	}

	if rpcauthz.CheckAuthorizationTargetAccess(user, roles, target, decision.authorization.write, true) != nil {
		return status.Error(codes.PermissionDenied, "RPC access denied")
	}
	return nil
}
