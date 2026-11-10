package gnmi

import (
	"context"
	"strings"

	pb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/sonic-net/sonic-gnmi/pkg/bypass"
	spb "github.com/sonic-net/sonic-gnmi/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Resolve gNMI authorization scope without constructing a data client or
// changing the request. Native instance/database availability stays with backend.
func rpcRequestTarget(ctx context.Context, method string, request interface{}, target string) (rpcDecision, error) {
	result := rpcDecision{target: target}
	var prefix *pb.Path
	var paths []*pb.Path
	var mode pb.SubscriptionList_Mode
	switch method {
	case "/gnmi.gNMI/Get":
		req, ok := request.(*pb.GetRequest)
		if !ok || req == nil {
			return result, status.Error(codes.InvalidArgument, "Get request required")
		}
		prefix, paths = req.GetPrefix(), req.GetPath()
	case "/gnmi.gNMI/Set":
		req, ok := request.(*pb.SetRequest)
		if !ok || req == nil {
			return result, status.Error(codes.InvalidArgument, "Set request required")
		}
		prefix = req.GetPrefix()
		paths = append(paths, req.GetDelete()...)
		for _, u := range req.GetReplace() {
			paths = append(paths, u.GetPath())
		}
		for _, u := range req.GetUpdate() {
			paths = append(paths, u.GetPath())
		}
	case "/gnmi.gNMI/Subscribe":
		req, ok := request.(*pb.SubscribeRequest)
		if !ok || req.GetSubscribe() == nil {
			return result, status.Error(codes.InvalidArgument, "first Subscribe request must contain SubscriptionList")
		}
		sub := req.GetSubscribe()
		prefix, mode = sub.GetPrefix(), sub.GetMode()
		if sub.GetSubscription() == nil {
			return result, status.Error(codes.InvalidArgument, "Subscribe request must contain subscriptions")
		}
		for _, s := range sub.GetSubscription() {
			paths = append(paths, s.GetPath())
		}
	default:
		return result, nil
	}
	target, origin := prefix.GetTarget(), prefix.GetOrigin()
	if method == "/gnmi.gNMI/Get" {
		switch target {
		case "OPERATIONAL":
			result.target = "gnoi"
			return result, nil
		case "OTHERS":
			result.target = "gnmi_other"
			return result, nil
		case "SHOW":
			result.target = "gnmi_show"
			return result, nil
		}
		if db, known, err := rpcDatabaseTarget(target); known || err != nil {
			result.target = "gnmi_" + db
			return result, err
		}
	}
	if origin == "" || method == "/gnmi.gNMI/Subscribe" {
		o, err := ParseOrigin(paths)
		if err != nil {
			return result, err
		}
		if origin == "" {
			origin = o
		} else if o != "" && o != origin {
			return result, status.Error(codes.InvalidArgument, "Origin conflict between prefix and paths")
		}
	}
	if origin == "sonic-db" {
		db, err := rpcNativeDatabase(prefix, paths)
		if err == nil {
			result.target = "gnmi_" + db
			return result, nil
		}
		if method == "/gnmi.gNMI/Set" && bypass.IsRequested(ctx) && strings.EqualFold(target, "CONFIG_DB") {
			short := len(paths) > 0
			for _, p := range paths {
				elems := append(append([]*pb.PathElem(nil), prefix.GetElem()...), p.GetElem()...)
				if len(elems) == 0 || !bypass.AllowedTables[elems[0].GetName()] {
					short = false
					break
				}
			}
			if short {
				result.target = "gnmi_CONFIG_DB"
				result.bypassOnly = true
				return result, nil
			}
		}
		return result, err
	}
	if method == "/gnmi.gNMI/Subscribe" {
		switch {
		case origin == "openconfig":
			return result, nil
		case origin != "":
			return result, status.Error(codes.Unimplemented, "Unsupported origin: "+origin)
		case target == "":
			return result, status.Error(codes.Unimplemented, "Empty target data not supported")
		case target == "OTHERS":
			result.target = "gnmi_others"
			return result, nil
		case target == "SHOW":
			return result, status.Error(codes.Unimplemented, "SHOW does not support subscribe operations")
		case target == "EVENTS" && mode == pb.SubscriptionList_STREAM:
			result.target = "gnmi_events"
			return result, nil
		}
		if db, known, err := rpcDatabaseTarget(target); known || err != nil {
			result.target = "gnmi_" + db
			return result, err
		}
	}
	return result, nil
}

func rpcDatabaseTarget(target string) (string, bool, error) {
	parts := strings.Split(target, "/")
	_, known := spb.Target_value[parts[0]]
	if known && len(parts) > 2 {
		return "", true, status.Error(codes.InvalidArgument, "invalid database target")
	}
	return parts[0], known, nil
}

func rpcNativeDatabase(prefix *pb.Path, paths []*pb.Path) (string, error) {
	if len(paths) == 0 {
		return "", status.Error(codes.Unimplemented, "No valid path")
	}
	database, instance := "", ""
	for _, p := range paths {
		if p.GetElem() == nil {
			continue
		}
		elems := append(append([]*pb.PathElem(nil), prefix.GetElem()...), p.GetElem()...)
		if len(elems) < 2 {
			return "", status.Error(codes.Unimplemented, "Invalid elem length")
		}
		db, inst := elems[0].GetName(), elems[1].GetName()
		if database == "" {
			database = db
		} else if database != db {
			return "", status.Error(codes.Unimplemented, "Target conflict in path")
		}
		if instance == "" {
			instance = inst
		} else if instance != inst {
			return "", status.Error(codes.Unimplemented, "Namespace/container conflict in path")
		}
	}
	if database == "" {
		return "", status.Error(codes.Unimplemented, "No target specified in path")
	}
	if target := prefix.GetTarget(); target != "" {
		parts := strings.Split(target, "/")
		if len(parts) > 2 || parts[0] != database || len(parts) == 2 && parts[1] != "" && parts[1] != instance {
			return "", status.Error(codes.InvalidArgument, "native target conflicts with path")
		}
	}
	return database, nil
}
