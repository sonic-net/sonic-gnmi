// Package gnmidbpath interprets database paths without native database discovery.
package gnmidbpath

import (
	"strings"

	pb "github.com/openconfig/gnmi/proto/gnmi"
	spb "github.com/sonic-net/sonic-gnmi/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ParseTarget separates a direct target and reports its known database name.
// A malformed qualifier does not change whether the database itself is known.
func ParseTarget(target string) (database, qualifier string, known, malformed bool) {
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

// Resolution accumulates the database and instance selected by native paths.
type Resolution struct {
	Database string
	Instance string
}

// Append interprets one path so callers can retain per-path native lookup and
// error ordering. Paths without Elem are skipped. Prefix.Target is not used.
// Prefix elements retain the legacy append/backing-array behavior.
func (r Resolution) Append(prefix, path *pb.Path) (next Resolution, processed bool, err error) {
	if path.GetElem() == nil {
		return r, false, nil
	}
	elems := path.GetElem()
	if prefix != nil {
		elems = append(prefix.GetElem(), elems...)
	}
	if elems == nil {
		return r, false, status.Error(codes.Unimplemented, "No valid elem")
	}
	if len(elems) < 2 {
		return r, false, status.Error(codes.Unimplemented, "Invalid elem length")
	}
	if r.Database == "" {
		r.Database = elems[0].GetName()
	} else if r.Database != elems[0].GetName() {
		return r, false, status.Error(codes.Unimplemented, "Target conflict in path")
	}
	instance := elems[1].GetName()
	if r.Instance == "" {
		r.Instance = instance
	} else if r.Instance != instance {
		return r, false, status.Error(codes.Unimplemented, "Namespace/container conflict in path")
	}
	return r, true, nil
}
