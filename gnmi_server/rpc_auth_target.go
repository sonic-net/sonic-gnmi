package gnmi

import (
	"context"
	"strings"

	pb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/sonic-net/sonic-gnmi/pkg/bypass"
	spb "github.com/sonic-net/sonic-gnmi/proto"
	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
	"github.com/sonic-net/sonic-gnmi/swsscommon"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Resolve only request-dependent gNMI targets; never initialize a backend here.
func rpcRequestTarget(ctx context.Context, method string, request interface{}, fallback string) (string, error) {
	var prefix *pb.Path
	var paths []*pb.Path
	var mode pb.SubscriptionList_Mode
	switch method {
	case "/gnmi.gNMI/Get":
		req, ok := request.(*pb.GetRequest)
		if !ok || req == nil {
			return "", status.Error(codes.InvalidArgument, "Get request required")
		}
		prefix, paths = req.GetPrefix(), req.GetPath()
	case "/gnmi.gNMI/Set":
		req, ok := request.(*pb.SetRequest)
		if !ok || req == nil {
			return "", status.Error(codes.InvalidArgument, "Set request required")
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
			return "", status.Error(codes.InvalidArgument, "first Subscribe request must contain SubscriptionList")
		}
		sub := req.GetSubscribe()
		prefix, mode = sub.GetPrefix(), sub.GetMode()
		if sub.GetSubscription() == nil {
			return "", status.Error(codes.InvalidArgument, "Subscribe request must contain subscriptions")
		}
		for _, s := range sub.GetSubscription() {
			paths = append(paths, s.GetPath())
		}
	default:
		return fallback, nil
	}
	target, origin := prefix.GetTarget(), prefix.GetOrigin()
	if method == "/gnmi.gNMI/Get" {
		switch target {
		case "OPERATIONAL":
			return "gnoi", nil
		case "OTHERS":
			return "gnmi_other", nil
		case "SHOW":
			return "gnmi_show", nil
		}
		if db, known, err := rpcDatabaseTarget(target); known || err != nil {
			return "gnmi_" + db, err
		}
	}
	if origin == "" || method == "/gnmi.gNMI/Subscribe" {
		pathOrigin, err := ParseOrigin(paths)
		if err != nil {
			return "", err
		}
		if origin == "" {
			origin = pathOrigin
		} else if pathOrigin != "" && pathOrigin != origin {
			return "", status.Error(codes.InvalidArgument, "Origin conflict between prefix and paths")
		}
	}
	if origin == "sonic-db" {
		// Table-relative ConfigDB bypass requests are not native DB paths. They
		// must stay on the bypass path; Set rejects an ineligible fallback below.
		if method == "/gnmi.gNMI/Set" && bypass.IsRequested(ctx) && strings.EqualFold(target, "CONFIG_DB") {
			req := request.(*pb.SetRequest)
			updates := append(append([]*pb.Update(nil), req.GetReplace()...), req.GetUpdate()...)
			short := len(paths) > 0
			for _, path := range paths {
				elems := append(append([]*pb.PathElem(nil), prefix.GetElem()...), path.GetElem()...)
				// DB-first native paths must never be treated as table-relative,
				// even when their instance name collides with an allowed table.
				if len(elems) == 0 || !bypass.AllowedTables[elems[0].GetName()] {
					short = false
					break
				}
			}
			if short && bypass.IsAllowedSetRequest(prefix, req.GetDelete(), updates) {
				return "gnmi_CONFIG_DB", nil
			}
		}
		return rpcNativeTarget(prefix, paths)
	}
	if method == "/gnmi.gNMI/Subscribe" {
		switch {
		case origin == "openconfig":
			return "gnmi", nil
		case origin != "":
			return "", status.Error(codes.Unimplemented, "Unsupported origin: "+origin)
		case target == "":
			return "", status.Error(codes.Unimplemented, "Empty target data not supported")
		case target == "OTHERS":
			return "gnmi_others", nil
		case target == "SHOW":
			return "", status.Error(codes.Unimplemented, "SHOW does not support subscribe operations")
		case target == "EVENTS" && mode == pb.SubscriptionList_STREAM:
			return "gnmi_events", nil
		}
		if db, known, err := rpcDatabaseTarget(target); known || err != nil {
			return "gnmi_" + db, err
		}
	}
	return "gnmi", nil // Existing non-native/OpenConfig routing.
}

func rpcDatabaseTarget(target string) (string, bool, error) {
	parts := strings.Split(target, "/")
	_, known := spb.Target_value[parts[0]]
	if known && len(parts) > 2 {
		return "", true, status.Error(codes.InvalidArgument, "invalid database target")
	}
	return parts[0], known, nil
}

func rpcNativeTarget(prefix *pb.Path, paths []*pb.Path) (string, error) {
	database, instance := "", ""
	for _, path := range paths {
		if path.GetElem() == nil {
			continue
		}
		elems := append(append([]*pb.PathElem(nil), prefix.GetElem()...), path.GetElem()...)
		if len(elems) < 2 {
			return "", status.Error(codes.Unimplemented, "Invalid elem length")
		}
		db, inst := elems[0].GetName(), elems[1].GetName()
		if database != "" && database != db {
			return "", status.Error(codes.Unimplemented, "Target conflict in path")
		}
		if instance != "" && instance != inst {
			return "", status.Error(codes.Unimplemented, "Namespace/container conflict in path")
		}
		database, instance = db, inst
	}
	if target := prefix.GetTarget(); target != "" {
		parts := strings.Split(target, "/")
		if len(parts) > 2 || parts[0] != database || len(parts) == 2 && parts[1] != "" && parts[1] != instance {
			return "", status.Error(codes.InvalidArgument, "native target conflicts with path")
		}
	}
	// Native databases come from configuration, unlike direct-target enum routing.
	// Membership across instances authorizes a DB scope, not an instance route.
	keys, err := sdcfg.GetDbAllInstances()
	defer func() {
		for _, key := range keys {
			swsscommon.DeleteSonicDBKey(key)
		}
	}()
	if err != nil {
		return "", status.Error(codes.Unavailable, "database configuration unavailable")
	}
	known := false
	for _, key := range keys {
		dbs, err := sdcfg.GetDbListByDBKey(key)
		// Any unreadable instance makes the configuration snapshot incomplete;
		// fail closed even if another instance contained the requested name.
		if err != nil {
			return "", status.Error(codes.Unavailable, "database configuration unavailable")
		}
		for _, db := range dbs {
			if db == database {
				known = true
			}
		}
	}
	if !known {
		return "", rpcDenied()
	}
	return "gnmi_" + database, nil
}
