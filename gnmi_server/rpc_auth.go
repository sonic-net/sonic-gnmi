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

func centralRPCAuth(config *Config) bool {
	if config.ConfigTableName != "GNMI_CLIENT_CERT" || !config.UserAuth["cert"] {
		return false
	}
	for mode, enabled := range config.UserAuth {
		if enabled && mode != "cert" {
			return false
		}
	}
	return true
}

// Only exact services with an assigned role target may use the RW default.
// gNSI and UDS-only Debug have no central target and remain denied.
func rpcAccess(method string) (target string, write bool) {
	service, name, ok := strings.Cut(strings.TrimPrefix(method, "/"), "/")
	if !strings.HasPrefix(method, "/") || !ok || name == "" || strings.Contains(name, "/") {
		return "", true
	}
	switch service {
	case "gnmi.gNMI", "gnoi.sonic.Debug":
		target = "gnmi"
	case "gnoi.factory_reset.FactoryReset", "gnoi.system.System", "gnoi.file.File", "gnoi.os.OS", "gnoi.containerz.Containerz", "gnoi.healthz.Healthz", "gnoi.sonic_jwt.SonicJwtService", "gnoi.sonic.SonicService", "sonic.gnoi.oras.v1.Oras":
		target = "gnoi"
	default:
		return "", true
	}
	switch method {
	case "/gnoi.sonic_jwt.SonicJwtService/Authenticate", "/gnoi.containerz.Containerz/Remove", "/gnoi.containerz.Containerz/Start", "/gnoi.containerz.Containerz/Stop", "/gnoi.containerz.Containerz/List", "/gnoi.containerz.Containerz/Log", "/gnoi.healthz.Healthz/List", "/gnoi.healthz.Healthz/Check":
		return "", true
	case "/gnmi.gNMI/Capabilities", "/gnmi.gNMI/Get", "/gnmi.gNMI/Subscribe", "/gnoi.system.System/Time", "/gnoi.system.System/RebootStatus", "/gnoi.file.File/Stat", "/gnoi.file.File/Get", "/gnoi.os.OS/Verify", "/gnoi.healthz.Healthz/Get", "/gnoi.healthz.Healthz/Artifact", "/gnoi.sonic.SonicService/ShowTechsupport", "/gnoi.sonic.Debug/GetSubscribePreferences":
		return target, false
	default:
		return target, true
	}
}

func rpcDenied() error { return status.Error(codes.PermissionDenied, "RPC access denied") }

func checkRPCIdentity(ctx context.Context, target string, write bool) error {
	rc, _ := common_utils.GetContext(ctx)
	service, _, _ := strings.Cut(target, "_")
	// Reuse the legacy evaluator for service noaccess and exact strict roles.
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

type rpcTargetKey struct{}
type rpcTarget struct {
	target string
	write  bool
}

func rpcUnaryAuth(config *Config) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, request interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if isUnixPeer(ctx) {
			return handler(ctx, request)
		}
		target, write := rpcAccess(info.FullMethod)
		if target == "" {
			return nil, rpcDenied()
		}
		ctx, err := rpcCredentials(config, ctx)
		if err != nil {
			return nil, err
		}
		target, err = rpcRequestTarget(ctx, info.FullMethod, request, target)
		if err != nil {
			return nil, err
		}
		if err = checkRPCIdentity(ctx, target, write); err != nil {
			return nil, err
		}
		return handler(context.WithValue(ctx, rpcTargetKey{}, rpcTarget{target, write}), request)
	}
}

func rpcStreamAuth(config *Config) grpc.StreamServerInterceptor {
	return func(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if isUnixPeer(stream.Context()) {
			return handler(srv, stream)
		}
		target, write := rpcAccess(info.FullMethod)
		if target == "" {
			return rpcDenied()
		}
		ctx, err := rpcCredentials(config, stream.Context())
		if err != nil {
			return err
		}
		wrapped := &rpcAuthStream{ServerStream: stream, ctx: ctx}
		if info.FullMethod == "/gnmi.gNMI/Subscribe" {
			// A scoped reader may subscribe, but a service denial or no valid role
			// must not wait for an initial message before being rejected.
			rc, _ := common_utils.GetContext(ctx)
			eligible := false
			for _, role := range rc.Auth.Roles {
				candidate := strings.TrimSpace(role)
				for _, suffix := range []string{"_readonly", "_readwrite"} {
					if candidate, ok := strings.CutSuffix(candidate, suffix); ok && candidate == strings.ToLower(candidate) && !strings.Contains(candidate, "__") && !strings.HasSuffix(candidate, "_") && (candidate == "gnmi" || strings.HasPrefix(candidate, "gnmi_")) && checkRPCIdentity(ctx, candidate, false) == nil {
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
			ctx, err = rpcCredentials(config, ctx)
			if err != nil {
				return err
			}
			target, err = rpcRequestTarget(ctx, info.FullMethod, first, target)
			if err != nil {
				return err
			}
			wrapped.first = first
		}
		if err := checkRPCIdentity(ctx, target, write); err != nil {
			return err
		}
		wrapped.ctx = context.WithValue(ctx, rpcTargetKey{}, rpcTarget{target, write})
		if wrapped.first == nil {
			wrapped.refresh = func(ctx context.Context) (context.Context, error) {
				ctx, err := rpcCredentials(config, ctx)
				if err != nil {
					return ctx, err
				}
				return ctx, checkRPCIdentity(ctx, target, write)
			}
		}
		err = handler(srv, wrapped)
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
