package gnmi

import (
	"context"
	"io"
	"strings"
	"sync"

	pb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/sonic-net/sonic-gnmi/common_utils"
	"github.com/sonic-net/sonic-gnmi/internal/authentication"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type rpcAuth struct{ config *Config }

func configuredRPCAuth(config *Config) *rpcAuth {
	if config.ConfigTableName != "GNMI_CLIENT_CERT" || !config.UserAuth["cert"] {
		return nil
	}
	for mode, enabled := range config.UserAuth {
		if enabled && mode != "cert" {
			return nil
		}
	}
	return &rpcAuth{config: config}
}

func rpcDenied() error { return status.Error(codes.PermissionDenied, "RPC access denied") }

func checkRPCIdentity(ctx context.Context, target string, write bool) error {
	rc, _ := common_utils.GetContext(ctx)
	service, _, _ := strings.Cut(target, "_")
	if target == "" || authentication.CheckRoleAccess(&rc.Auth, service, false, false) != nil || authentication.CheckRoleAccess(&rc.Auth, target, write, true) != nil {
		return rpcDenied()
	}
	return nil
}

func rpcCredentials(config *Config, ctx context.Context) (context.Context, error) {
	ctx = common_utils.WithFreshAuth(ctx)
	rc, ctx := common_utils.GetContext(ctx)
	rc.Auth.AuthEnabled = true
	return ClientCertAuthenAndAuthor(ctx, config.ConfigTableName, config.EnableCrl)
}

type rpcDecisionKey struct{}
type rpcDecision struct {
	target     string
	write      bool
	bypassOnly bool
}

func checkRPCDecision(ctx context.Context, target string, write bool) error {
	decision, ok := ctx.Value(rpcDecisionKey{}).(rpcDecision)
	if !ok || !strings.EqualFold(decision.target, target) {
		return rpcDenied()
	}
	return checkRPCIdentity(ctx, target, write || decision.write)
}

func (a *rpcAuth) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, request interface{}, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (interface{}, error) {
		if isUnixPeer(ctx) {
			return next(ctx, request)
		}
		target, write := rpcAccess(info.FullMethod)
		if target == "" {
			return nil, rpcDenied()
		}
		ctx, err := rpcCredentials(a.config, ctx)
		if err != nil {
			return nil, err
		}
		decision, err := rpcRequestTarget(ctx, info.FullMethod, request, target)
		if err != nil {
			return nil, err
		}
		decision.write = write
		if err := checkRPCIdentity(ctx, decision.target, write); err != nil {
			return nil, err
		}
		return next(context.WithValue(ctx, rpcDecisionKey{}, decision), request)
	}
}

func (a *rpcAuth) StreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, next grpc.StreamHandler) error {
		if isUnixPeer(stream.Context()) {
			return next(srv, stream)
		}
		target, write := rpcAccess(info.FullMethod)
		if target == "" {
			return rpcDenied()
		}
		ctx, err := rpcCredentials(a.config, stream.Context())
		if err != nil {
			return err
		}
		decision := rpcDecision{target: target, write: write}
		wrapped := &rpcAuthStream{ServerStream: stream}
		if info.FullMethod == "/gnmi.gNMI/Subscribe" {
			// Eligibility only permits waiting for initialization, not its target.
			rc, _ := common_utils.GetContext(ctx)
			eligible := false
			for _, role := range rc.Auth.Roles {
				candidate := strings.TrimSpace(role)
				for _, suffix := range []string{"_readonly", "_readwrite"} {
					if scope, ok := strings.CutSuffix(candidate, suffix); ok && scope == strings.ToLower(scope) && !strings.Contains(scope, "__") && !strings.HasSuffix(scope, "_") && (scope == "gnmi" || strings.HasPrefix(scope, "gnmi_")) && checkRPCIdentity(ctx, scope, false) == nil {
						eligible = true
					}
				}
			}
			if !eligible {
				return rpcDenied()
			}
			first := new(pb.SubscribeRequest)
			if err := stream.RecvMsg(first); err != nil {
				if err == io.EOF {
					return status.Error(codes.Aborted, "stream EOF received before init")
				}
				return status.Error(status.Code(err), "received error from client")
			}
			ctx, err = rpcCredentials(a.config, ctx)
			if err != nil {
				return err
			}
			decision, err = rpcRequestTarget(ctx, info.FullMethod, first, target)
			if err != nil {
				return err
			}
			decision.write = write
			wrapped.first = first
		}
		if err := checkRPCIdentity(ctx, decision.target, write); err != nil {
			return err
		}
		wrapped.ctx = context.WithValue(ctx, rpcDecisionKey{}, decision)
		if wrapped.first == nil {
			wrapped.refresh = func(ctx context.Context) (context.Context, error) {
				ctx, err := rpcCredentials(a.config, ctx)
				if err != nil {
					return ctx, err
				}
				return ctx, checkRPCDecision(ctx, decision.target, write)
			}
		}
		err = next(srv, wrapped)
		if wrapped.refreshErr != nil {
			return wrapped.refreshErr
		}
		return err
	}
}

type rpcAuthStream struct {
	grpc.ServerStream
	mu         sync.RWMutex
	ctx        context.Context
	first      *pb.SubscribeRequest
	refresh    func(context.Context) (context.Context, error)
	refreshErr error
}

func (s *rpcAuthStream) Context() context.Context { s.mu.RLock(); defer s.mu.RUnlock(); return s.ctx }
func (s *rpcAuthStream) RecvMsg(message interface{}) error {
	if s.first != nil {
		request, ok := message.(*pb.SubscribeRequest)
		if !ok {
			return status.Error(codes.Internal, "unexpected Subscribe request type")
		}
		proto.Reset(request)
		proto.Merge(request, s.first)
		s.first = nil
		return nil
	}
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
