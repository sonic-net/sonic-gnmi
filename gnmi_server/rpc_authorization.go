package gnmi

import (
	"context"
	"io"
	"reflect"
	"sync"

	"github.com/sonic-net/sonic-gnmi/common_utils"
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
	deny               bool
	target             string
	write              bool
	requestTarget      bool
	bufferFirstRequest bool
}

type rpcAuthorizationInterceptor struct {
	methods      map[string]rpcAuthorization
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
		methods:      serviceAuthorizations(),
		authenticate: authenticate,
		bypass:       bypass,
	}
}

func (i *rpcAuthorizationInterceptor) authenticateRPC(
	ctx context.Context,
	fullMethod string,
) (context.Context, *rpcIdentity, rpcAuthorization, error) {
	authorization, ok := i.methods[fullMethod]
	if !ok || authorization.deny {
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
			authenticatedCtx, auth, _, err = i.authenticateRPC(common_utils.WithFreshAuth(authenticatedCtx), info.FullMethod)
			if err != nil {
				return err
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
		wrapped := &rpcAuthorizationServerStream{
			ServerStream: stream,
			ctx:          authorizedCtx,
			buffered:     bufferedRequest,
		}
		if bufferedRequest == nil {
			// Generated server-streaming handlers receive their request after the
			// interceptor starts. Refresh before that first message reaches them.
			wrapped.refresh = func(ctx context.Context) (context.Context, error) {
				ctx, identity, _, err := i.authenticateRPC(common_utils.WithFreshAuth(ctx), info.FullMethod)
				if err != nil {
					return ctx, err
				}
				return authorizeRPCTarget(ctx, identity, authorization, requestTarget)
			}
		}
		err = handler(srv, wrapped)
		if wrapped.refreshErr != nil {
			return wrapped.refreshErr
		}
		return err
	}
}

type rpcAuthorizationServerStream struct {
	grpc.ServerStream
	ctx        context.Context
	buffered   proto.Message
	mu         sync.RWMutex
	refresh    func(context.Context) (context.Context, error)
	refreshErr error
}

func (s *rpcAuthorizationServerStream) Context() context.Context {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ctx
}

func (s *rpcAuthorizationServerStream) RecvMsg(message interface{}) error {
	if s.buffered == nil {
		if s.refreshErr != nil {
			return s.refreshErr
		}
		if err := s.ServerStream.RecvMsg(message); err != nil {
			return err
		}
		if s.refresh != nil {
			ctx, err := s.refresh(s.Context())
			s.refresh = nil
			s.refreshErr = err
			if err != nil {
				return err
			}
			s.mu.Lock()
			s.ctx = ctx
			s.mu.Unlock()
		}
		return nil
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
