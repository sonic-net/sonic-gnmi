package gnmi

import (
	"context"

	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/sonic-net/sonic-gnmi/pkg/bypass"
	sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type requestBackend uint8

const (
	backendTranslib requestBackend = iota
	backendDatabase
	backendNative
	backendOperational
	backendOthers
	backendShow
	backendEvents
)

// ownedRequest is independent of the protobuf handed to downstream middleware.
// Changed requests are rejected, not silently replaced by the saved request.
type authorizedRequestRoute struct {
	method          string
	ownedRequest    proto.Message
	backend         requestBackend
	origin          string
	database        string
	qualifier       string
	qualified       bool
	native          sdc.NativeDatabaseRoute
	nativeBinding   *sdc.NativeDatabaseBinding
	bypassRequested bool
}

func authorizedRoute(ctx context.Context, config *Config, method string, request proto.Message) (*authorizedRequestRoute, error) {
	if isUnixPeer(ctx) {
		return nil, nil
	}
	decision, completed := rpcAuthorizationFromContext(ctx)
	enabled, _ := rpcAuthorizationMode(map[string]bool(config.UserAuth), config.ConfigTableName)
	if !completed && !enabled {
		return nil, nil
	}
	route := decision.execution
	if !completed || route == nil || route.method != method ||
		decision.authorization.write != (method == "/gnmi.gNMI/Set") ||
		route.bypassRequested != bypass.IsRequested(ctx) ||
		!proto.Equal(route.ownedRequest, request) {
		return nil, status.Error(codes.PermissionDenied, "authorized request does not match execution")
	}
	copy := *route
	return &copy, nil
}

func (r *authorizedRequestRoute) usesDefaultInstance() bool {
	return r.nativeBinding != nil && r.nativeBinding.Namespace == "" && r.nativeBinding.Container == ""
}

func (r *authorizedRequestRoute) bindNative() error {
	if r.nativeBinding != nil {
		return nil
	}
	binding, err := sdc.ResolveNativeDatabaseRoute(r.native)
	if err != nil {
		return err
	}
	r.nativeBinding = &binding
	return nil
}

// newClient consumes the approved backend/target, leaving table/key conversion
// and native resource discovery to the data client after authorization.
func (r *authorizedRequestRoute) newClient(ctx context.Context, config *Config, prefix *gnmipb.Path, paths []*gnmipb.Path, encoding gnmipb.Encoding, logLevel int) (sdc.Client, error) {
	switch r.backend {
	case backendNative:
		if err := r.bindNative(); err != nil {
			return nil, err
		}
		zmq, vrf := config.ZmqPort, config.Vrf
		if r.method == "/gnmi.gNMI/Subscribe" {
			zmq, vrf = "", ""
		}
		return sdc.NewResolvedMixedDbClient(paths, prefix, r.origin, encoding, zmq, vrf, *r.nativeBinding)
	case backendDatabase:
		// The legacy empty-path client performs no namespace lookup or access.
		if len(paths) == 0 {
			return sdc.NewResolvedDbClient(paths, prefix, r.method == "/gnmi.gNMI/Get", sdc.DatabaseRoute{})
		}
		binding, err := sdc.ResolveDatabaseRoute(r.database, r.qualifier, r.qualified)
		if err != nil {
			return nil, err
		}
		client, err := sdc.NewResolvedDbClient(paths, prefix, r.method == "/gnmi.gNMI/Get", binding)
		if err == nil && r.method == "/gnmi.gNMI/Get" {
			err = client.(*sdc.DbClient).ValidatePaths()
			if err != nil {
				client.Close()
			}
		}
		return client, err
	case backendOthers:
		return sdc.NewNonDbClient(paths, prefix)
	case backendShow:
		return sdc.NewShowClient(paths, prefix)
	case backendEvents:
		return sdc.NewEventClient(paths, prefix, logLevel)
	case backendTranslib:
		switch request := r.ownedRequest.(type) {
		case *gnmipb.GetRequest:
			return sdc.NewTranslClient(prefix, paths, ctx, request.GetExtension())
		case *gnmipb.SetRequest:
			return sdc.NewTranslClient(prefix, nil, ctx, request.GetExtension())
		case *gnmipb.SubscribeRequest:
			return sdc.NewTranslClient(prefix, paths, ctx, request.GetExtension(), sdc.TranslWildcardOption{})
		}
	}
	return nil, status.Error(codes.Internal, "unsupported authorized backend")
}
