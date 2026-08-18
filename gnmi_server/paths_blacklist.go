package gnmi

// Adapter between the pure pkg/pathblacklist policy and the gNMI server:
// normalizes protobuf request paths into pathblacklist.Path values and
// translates a policy match into a gRPC PermissionDenied error. The matched
// entry is logged server-side only; clients receive a generic denial so the
// policy contents cannot be enumerated.

import (
	log "github.com/golang/glog"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/sonic-net/sonic-gnmi/pkg/pathblacklist"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// checkPathsBlacklist returns a PermissionDenied error if any of the given
// request paths, combined with the request prefix, is covered by the policy.
// A nil policy allows everything.
func checkPathsBlacklist(policy *pathblacklist.Policy, prefix *gnmipb.Path, paths []*gnmipb.Path) error {
	if policy.Len() == 0 {
		return nil
	}
	for _, path := range paths {
		req := normalizeRequestPath(prefix, path)
		if entry, ok := policy.Match(req); ok {
			log.V(2).Infof("Rejecting request: target %q path %v matched paths blacklist entry %q",
				req.Target, req.Elems, entry)
			return status.Error(codes.PermissionDenied, "request path is blocked by policy")
		}
	}
	return nil
}

// normalizeRequestPath flattens prefix + path into a pathblacklist.Path,
// mapping both wire forms of a DB request to the same normalized value:
//
//	prefix.target=CONFIG_DB, path=/PORT           -> {CONFIG_DB, [PORT]}
//	origin=sonic-db, /CONFIG_DB/localhost/PORT    -> {CONFIG_DB, [PORT]}
//
// In the native sonic-db form the first element is the database and the
// second is the namespace/container instance (see MixedDbClient.ParseDatabase).
func normalizeRequestPath(prefix, path *gnmipb.Path) pathblacklist.Path {
	elems := append(elemNames(prefix), elemNames(path)...)
	target := prefix.GetTarget()
	origin := prefix.GetOrigin()
	if origin == "" {
		origin = path.GetOrigin()
	}
	if IsNativeOrigin(origin) && len(elems) > 0 {
		target = elems[0]
		if len(elems) > 2 {
			elems = elems[2:]
		} else {
			elems = nil
		}
	}
	return pathblacklist.Path{Target: target, Elems: elems}
}

// elemNames returns the element names of a single path part, supporting the
// deprecated Element field for backward compatibility. Handling each part
// independently keeps requests that mix encodings between prefix and path
// from dropping elements. Key qualifiers on elements are ignored; matching
// is by element name only.
func elemNames(p *gnmipb.Path) []string {
	if len(p.GetElem()) > 0 {
		names := make([]string, 0, len(p.GetElem()))
		for _, pe := range p.GetElem() {
			names = append(names, pe.GetName())
		}
		return names
	}
	if len(p.GetElement()) > 0 {
		return append([]string(nil), p.GetElement()...)
	}
	return nil
}
