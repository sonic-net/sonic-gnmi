package gnmi

import (
	"context"
	"strings"

	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/sonic-net/sonic-gnmi/pkg/bypass"
	"github.com/sonic-net/sonic-gnmi/sonic_data_client/dbpath"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func rpcAuthorizationRequestTarget(
	ctx context.Context,
	fullMethod string,
	request interface{},
) (rpcAuthorizationTarget, error) {
	switch fullMethod {
	case "/gnmi.gNMI/Get":
		req, ok := request.(*gnmipb.GetRequest)
		if !ok {
			return rpcAuthorizationTarget{}, status.Error(codes.Internal, "unexpected gNMI Get request type")
		}
		target, err := gnmiGetAuthorizationTarget(req)
		return rpcAuthorizationTarget{target: target}, err
	case "/gnmi.gNMI/Set":
		req, ok := request.(*gnmipb.SetRequest)
		if !ok {
			return rpcAuthorizationTarget{}, status.Error(codes.Internal, "unexpected gNMI Set request type")
		}
		return gnmiSetAuthorizationTarget(ctx, req)
	case "/gnmi.gNMI/Subscribe":
		req, ok := request.(*gnmipb.SubscribeRequest)
		if !ok {
			return rpcAuthorizationTarget{}, status.Error(codes.Internal, "unexpected gNMI Subscribe request type")
		}
		target, err := gnmiSubscribeAuthorizationTarget(req)
		return rpcAuthorizationTarget{target: target}, err
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

func gnmiGetAuthorizationTarget(req *gnmipb.GetRequest) (string, error) {
	if req == nil {
		return "", status.Error(codes.InvalidArgument, "gNMI Get request is required")
	}
	prefix := req.GetPrefix()
	target := prefix.GetTarget()
	switch target {
	case "OPERATIONAL":
		return "gnoi", nil
	case "OTHERS":
		return "gnmi_other", nil
	case "SHOW":
		return "gnmi_show", nil
	}
	if database, ok, err := authorizationDatabaseTarget(target); err != nil {
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
	if origin == "sonic-db" {
		database, err := authorizationNativeDatabase(prefix, req.GetPath())
		if err != nil {
			return "", err
		}
		return "gnmi_" + database, nil
	}
	return "gnmi", nil
}

func gnmiSetAuthorizationTarget(ctx context.Context, req *gnmipb.SetRequest) (rpcAuthorizationTarget, error) {
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
	if origin != "sonic-db" {
		return rpcAuthorizationTarget{target: "gnmi"}, nil
	}

	database, err := authorizationNativeDatabase(prefix, paths)
	if err == nil {
		return rpcAuthorizationTarget{target: "gnmi_" + database}, nil
	}
	updates := append(append([]*gnmipb.Update(nil), req.GetReplace()...), req.GetUpdate()...)
	if bypass.IsRequested(ctx) &&
		strings.EqualFold(prefix.GetTarget(), "CONFIG_DB") &&
		bypass.IsAllowedSetRequest(prefix, req.GetDelete(), updates) {
		return rpcAuthorizationTarget{
			target: "gnmi_CONFIG_DB",
			route:  rpcAuthorizationRouteSetBypassOnly,
		}, nil
	}
	return rpcAuthorizationTarget{}, err
}

func gnmiSubscribeAuthorizationTarget(req *gnmipb.SubscribeRequest) (string, error) {
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
	switch {
	case origin == "openconfig":
		return "gnmi", nil
	case origin == "sonic-db":
		database, err := authorizationNativeDatabase(prefix, paths)
		if err != nil {
			return "", err
		}
		return "gnmi_" + database, nil
	case origin != "":
		return "", status.Errorf(codes.Unimplemented, "Unsupported origin: %s", origin)
	case target == "":
		return "", status.Error(codes.Unimplemented, "Empty target data not supported")
	case target == "OTHERS":
		return "gnmi_others", nil
	case target == "SHOW":
		return "", status.Error(codes.Unimplemented, "SHOW does not support subscribe operations")
	case target == "EVENTS" && subscription.GetMode() == gnmipb.SubscriptionList_STREAM:
		return "gnmi_events", nil
	}
	if database, ok, err := authorizationDatabaseTarget(target); err != nil {
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

func authorizationNativeTarget(prefix *gnmipb.Path, paths []*gnmipb.Path) string {
	if target := prefix.GetTarget(); target != "" {
		return target
	}
	resolution, err := dbpath.Resolve(prefix, paths)
	if err != nil {
		return ""
	}
	return resolution.Database
}

func authorizationNativeDatabase(prefix *gnmipb.Path, paths []*gnmipb.Path) (string, error) {
	resolution, err := dbpath.Resolve(prefix, paths)
	if err != nil {
		return "", err
	}
	if err := validateAuthorizationNativePrefixTarget(prefix.GetTarget(), resolution); err != nil {
		return "", err
	}
	return resolution.Database, nil
}

func validateAuthorizationNativePrefixTarget(target string, resolution dbpath.Resolution) error {
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

func authorizationDatabaseTarget(target string) (string, bool, error) {
	database, _, known, malformed := dbpath.ParseTarget(target)
	if !known {
		return "", false, nil
	}
	if malformed {
		return "", false, status.Error(codes.InvalidArgument, "invalid gNMI database target format")
	}
	return database, true, nil
}
