package dbpath

import (
	"strings"

	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	spb "github.com/sonic-net/sonic-gnmi/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Resolution struct {
	Database string
	Instance string
}

// ParseTarget separates a direct gNMI target without consulting device state.
func ParseTarget(target string) (database string, qualifier string, known bool, malformed bool) {
	parts := strings.Split(target, "/")
	database = parts[0]
	_, known = spb.Target_value[database]
	if len(parts) > 2 {
		return database, "", known, true
	}
	if len(parts) == 2 {
		qualifier = parts[1]
	}
	return database, qualifier, known, false
}

// Resolve returns the database and instance selected by a native sonic-db path.
// Prefix.Target is intentionally excluded because MixedDbClient routes native
// requests from path elements.
func Resolve(prefix *gnmipb.Path, paths []*gnmipb.Path) (Resolution, error) {
	if len(paths) == 0 {
		return Resolution{}, status.Error(codes.Unimplemented, "No valid path")
	}

	var resolution Resolution
	prefixElements := prefix.GetElem()
	for _, path := range paths {
		if path.GetElem() == nil {
			continue
		}
		elements := append(append([]*gnmipb.PathElem(nil), prefixElements...), path.GetElem()...)
		if elements == nil {
			return Resolution{}, status.Error(codes.Unimplemented, "No valid elem")
		}
		if len(elements) < 2 {
			return Resolution{}, status.Error(codes.Unimplemented, "Invalid elem length")
		}

		database := elements[0].GetName()
		instance := elements[1].GetName()
		if resolution.Database == "" {
			resolution.Database = database
		} else if resolution.Database != database {
			return Resolution{}, status.Error(codes.Unimplemented, "Target conflict in path")
		}
		if resolution.Instance == "" {
			resolution.Instance = instance
		} else if resolution.Instance != instance {
			return Resolution{}, status.Error(codes.Unimplemented, "Namespace/container conflict in path")
		}
	}

	if resolution.Database == "" {
		return Resolution{}, status.Error(codes.Unimplemented, "No target specified in path")
	}
	return resolution, nil
}
