package gnmi

import (
	"context"
	"strings"

	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/sonic-net/sonic-gnmi/internal/gnmidbpath"
	"github.com/sonic-net/sonic-gnmi/pkg/bypass"
	sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func rpcAuthorizationRequestTarget(
	ctx context.Context,
	fullMethod string,
	request interface{},
) (rpcAuthorizationTarget, error) {
	route := &authorizedRequestRoute{method: fullMethod, bypassRequested: bypass.IsRequested(ctx)}
	switch fullMethod {
	case "/gnmi.gNMI/Get":
		req, ok := request.(*gnmipb.GetRequest)
		if !ok {
			return rpcAuthorizationTarget{}, status.Error(codes.Internal, "unexpected gNMI Get request type")
		}
		route.ownedRequest = proto.Clone(req)
		target, err := gnmiGetAuthorizationTarget(req, route)
		return rpcAuthorizationTarget{target: target, execution: route}, err
	case "/gnmi.gNMI/Set":
		req, ok := request.(*gnmipb.SetRequest)
		if !ok {
			return rpcAuthorizationTarget{}, status.Error(codes.Internal, "unexpected gNMI Set request type")
		}
		route.ownedRequest = proto.Clone(req)
		result, err := gnmiSetAuthorizationTarget(ctx, req, route)
		result.execution = route
		return result, err
	case "/gnmi.gNMI/Subscribe":
		req, ok := request.(*gnmipb.SubscribeRequest)
		if !ok {
			return rpcAuthorizationTarget{}, status.Error(codes.Internal, "unexpected gNMI Subscribe request type")
		}
		route.ownedRequest = proto.Clone(req)
		target, err := gnmiSubscribeAuthorizationTarget(req, route)
		return rpcAuthorizationTarget{target: target, execution: route}, err
	default:
		return rpcAuthorizationTarget{}, status.Error(codes.Internal, "RPC target resolver is not configured")
	}
}

func rpcAuthorizationFirstStreamRequest(fullMethod string) proto.Message {
	if fullMethod == "/gnmi.gNMI/Subscribe" {
		return &gnmipb.SubscribeRequest{}
	}
	return nil
}

func gnmiGetAuthorizationTarget(req *gnmipb.GetRequest, route *authorizedRequestRoute) (string, error) {
	if req == nil {
		return "", status.Error(codes.InvalidArgument, "gNMI Get request is required")
	}
	prefix := req.GetPrefix()
	target := prefix.GetTarget()
	switch target {
	case "OPERATIONAL":
		route.backend = backendOperational
		return "gnoi", nil
	case "OTHERS":
		route.backend = backendOthers
		return "gnmi_other", nil
	case "SHOW":
		route.backend = backendShow
		return "gnmi_show", nil
	}
	if database, ok, err := authorizationDatabaseTarget(target, route); err != nil {
		return "", err
	} else if ok {
		return "gnmi_" + database, nil
	}

	origin := prefix.GetOrigin()
	if origin == "" {
		var err error
		origin, err = authorizationPathOrigin(req.GetPath())
		if err != nil {
			return "", err
		}
	}
	route.origin = origin
	if origin == "sonic-db" {
		route.backend = backendNative
		database, err := authorizationNativeDatabase(prefix, req.GetPath(), route)
		if err != nil {
			return "", err
		}
		return "gnmi_" + database, nil
	}
	return "gnmi", nil
}

func gnmiSetAuthorizationTarget(ctx context.Context, req *gnmipb.SetRequest, route *authorizedRequestRoute) (rpcAuthorizationTarget, error) {
	if req == nil {
		return rpcAuthorizationTarget{}, status.Error(codes.InvalidArgument, "gNMI Set request is required")
	}
	paths := make([]*gnmipb.Path, 0, len(req.GetDelete())+len(req.GetReplace())+len(req.GetUpdate()))
	paths = append(paths, req.GetDelete()...)
	for _, update := range req.GetReplace() {
		paths = append(paths, update.GetPath())
	}
	for _, update := range req.GetUpdate() {
		paths = append(paths, update.GetPath())
	}

	prefix := req.GetPrefix()
	origin := prefix.GetOrigin()
	if origin == "" {
		var err error
		origin, err = authorizationPathOrigin(paths)
		if err != nil {
			return rpcAuthorizationTarget{}, err
		}
	}
	route.origin = origin
	if origin != "sonic-db" {
		return rpcAuthorizationTarget{target: "gnmi"}, nil
	}

	route.backend = backendNative
	database, err := authorizationNativeDatabase(prefix, paths, route)
	if err == nil {
		return rpcAuthorizationTarget{target: "gnmi_" + database}, nil
	}
	updates := append(append([]*gnmipb.Update(nil), req.GetReplace()...), req.GetUpdate()...)
	if bypass.IsRequested(ctx) &&
		strings.EqualFold(prefix.GetTarget(), "CONFIG_DB") &&
		shortDefaultBypassPaths(prefix, paths) &&
		bypass.IsAllowedSetRequest(prefix, req.GetDelete(), updates) {
		route.native = sdc.NativeDatabaseRoute{Database: "CONFIG_DB", Instances: []string{"localhost"}}
		return rpcAuthorizationTarget{
			target: "gnmi_CONFIG_DB",
			route:  rpcAuthorizationRouteSetBypassOnly,
		}, nil
	}
	return rpcAuthorizationTarget{}, err
}

// Short bypass syntax starts with a table, never a native database/instance.
// Native parse failures must not be reinterpreted as default-database writes.
func shortDefaultBypassPaths(prefix *gnmipb.Path, paths []*gnmipb.Path) bool {
	if len(paths) == 0 {
		return false
	}
	for _, path := range paths {
		elems := append(append([]*gnmipb.PathElem(nil), prefix.GetElem()...), path.GetElem()...)
		if len(elems) == 0 || !bypass.AllowedTables[elems[0].GetName()] {
			return false
		}
	}
	return true
}

func gnmiSubscribeAuthorizationTarget(req *gnmipb.SubscribeRequest, route *authorizedRequestRoute) (string, error) {
	if req == nil || req.GetSubscribe() == nil {
		return "", status.Error(codes.InvalidArgument, "first Subscribe request must contain a SubscriptionList")
	}
	subscription := req.GetSubscribe()
	if subscription.GetSubscription() == nil {
		return "", status.Error(codes.InvalidArgument, "Subscribe request must contain subscriptions")
	}

	paths := make([]*gnmipb.Path, 0, len(subscription.GetSubscription()))
	for _, item := range subscription.GetSubscription() {
		paths = append(paths, item.GetPath())
	}
	prefix := subscription.GetPrefix()
	origin := prefix.GetOrigin()
	pathOrigin, err := authorizationPathOrigin(paths)
	if err != nil {
		return "", err
	}
	if origin == "" {
		origin = pathOrigin
	} else if pathOrigin != "" && pathOrigin != origin {
		return "", status.Error(codes.InvalidArgument, "Origin conflict between prefix and paths")
	}

	target := prefix.GetTarget()
	route.origin = origin
	switch {
	case origin == "openconfig":
		return "gnmi", nil
	case origin == "sonic-db":
		route.backend = backendNative
		database, err := authorizationNativeDatabase(prefix, paths, route)
		if err != nil {
			return "", err
		}
		return "gnmi_" + database, nil
	case origin != "":
		return "", status.Errorf(codes.Unimplemented, "Unsupported origin: %s", origin)
	case target == "":
		return "", status.Error(codes.Unimplemented, "Empty target data not supported")
	case target == "OTHERS":
		route.backend = backendOthers
		return "gnmi_others", nil
	case target == "SHOW":
		return "", status.Error(codes.Unimplemented, "SHOW does not support subscribe operations")
	case target == "EVENTS" && subscription.GetMode() == gnmipb.SubscriptionList_STREAM:
		route.backend = backendEvents
		return "gnmi_events", nil
	}
	if database, ok, err := authorizationDatabaseTarget(target, route); err != nil {
		return "", err
	} else if ok {
		return "gnmi_" + database, nil
	}
	return "gnmi", nil
}

func authorizationPathOrigin(paths []*gnmipb.Path) (string, error) {
	origin := ""
	for index, path := range paths {
		if index == 0 {
			origin = path.GetOrigin()
			continue
		}
		if origin != path.GetOrigin() {
			return "", status.Error(codes.Unimplemented, "Origin conflict in path")
		}
	}
	return origin, nil
}

func authorizationNativeDatabase(prefix *gnmipb.Path, paths []*gnmipb.Path, route *authorizedRequestRoute) (string, error) {
	resolution, err := resolveAuthorizationNativePaths(prefix, paths, route)
	if err != nil {
		return "", err
	}
	if err := validateAuthorizationNativePrefixTarget(prefix.GetTarget(), resolution); err != nil {
		return "", err
	}
	return resolution.Database, nil
}

// Resolve the whole request before authorization without performing backend
// discovery or changing the request's prefix backing array. Backend callers
// retain their incremental Append/query ordering and legacy aliasing behavior.
func resolveAuthorizationNativePaths(prefix *gnmipb.Path, paths []*gnmipb.Path, route *authorizedRequestRoute) (gnmidbpath.Resolution, error) {
	if len(paths) == 0 {
		return gnmidbpath.Resolution{}, status.Error(codes.Unimplemented, "No valid path")
	}
	var resolution gnmidbpath.Resolution
	route.native = sdc.NativeDatabaseRoute{}
	for _, path := range paths {
		isolatedPrefix := &gnmipb.Path{Elem: append([]*gnmipb.PathElem(nil), prefix.GetElem()...)}
		next, processed, err := resolution.Append(isolatedPrefix, path)
		if err != nil {
			return gnmidbpath.Resolution{}, err
		}
		resolution = next
		if processed {
			route.native.Instances = append(route.native.Instances, resolution.Instance)
		}
	}
	if resolution.Database == "" {
		return gnmidbpath.Resolution{}, status.Error(codes.Unimplemented, "No target specified in path")
	}
	route.native.Database = resolution.Database
	return resolution, nil
}

func validateAuthorizationNativePrefixTarget(target string, resolution gnmidbpath.Resolution) error {
	if target == "" {
		return nil
	}
	parts := strings.Split(target, "/")
	if len(parts) > 2 || parts[0] == "" {
		return status.Error(codes.InvalidArgument, "invalid native gNMI target format")
	}
	if parts[0] != resolution.Database {
		return status.Error(codes.InvalidArgument, "native gNMI target conflicts with path")
	}
	if len(parts) == 2 && parts[1] != "" && parts[1] != resolution.Instance {
		return status.Error(codes.InvalidArgument, "native gNMI target instance conflicts with path")
	}
	return nil
}

func authorizationDatabaseTarget(target string, route *authorizedRequestRoute) (string, bool, error) {
	database, qualifier, known, malformed := gnmidbpath.ParseTarget(target)
	if !known {
		return "", false, nil
	}
	if malformed {
		return "", false, status.Error(codes.InvalidArgument, "invalid gNMI database target format")
	}
	route.backend = backendDatabase
	route.database, route.qualifier, route.qualified = database, qualifier, strings.Contains(target, "/")
	return database, true, nil
}
