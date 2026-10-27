package interceptors

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	pb "github.com/openconfig/gnmi/proto/gnmi"
	spb "github.com/sonic-net/sonic-gnmi/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const redacted = "REDACTED"

type secretRedactor struct{}

type readRoute int

const (
	routeTranslated readRoute = iota
	routeLegacy
	routeNative
)

func (*secretRedactor) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		route := routeTranslated
		if info.FullMethod == gnmiGetMethod {
			if request, ok := req.(*pb.GetRequest); ok {
				route = responseRoute(request.GetPrefix(), request.GetPath(), false)
				if err := validateReadPaths(request.GetPrefix(), request.GetPath(), false); err != nil {
					return nil, err
				}
				if route == routeNative {
					request = proto.Clone(request).(*pb.GetRequest)
					normalizeNativeRead(request.Prefix, request.Path)
					req = request
				}
			}
		}
		resp, err := handler(ctx, req)
		if info.FullMethod != gnmiGetMethod {
			return resp, err
		}
		if err != nil {
			return nil, readError(err)
		}
		value, ok := resp.(*pb.GetResponse)
		if !ok || value == nil {
			return nil, invalidSecretResponse()
		}
		out := proto.Clone(value).(*pb.GetResponse)
		for _, notification := range out.Notification {
			if err := redactNotification(notification, route); err != nil {
				return nil, err
			}
		}
		return out, nil
	}
}

func (*secretRedactor) StreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if info.FullMethod != "/gnmi.gNMI/Subscribe" {
			return handler(srv, stream)
		}
		return readError(handler(srv, &redactingStream{ServerStream: stream}))
	}
}

type redactingStream struct {
	grpc.ServerStream
	route      readRoute
	subscribed bool
}

func (s *redactingStream) RecvMsg(msg interface{}) error {
	if err := s.ServerStream.RecvMsg(msg); err != nil {
		return err
	}
	if request, ok := msg.(*pb.SubscribeRequest); ok && request.GetSubscribe() != nil {
		sub := request.GetSubscribe()
		paths := make([]*pb.Path, 0, len(sub.Subscription))
		for _, entry := range sub.Subscription {
			paths = append(paths, entry.GetPath())
		}
		route := responseRoute(sub.Prefix, paths, true)
		if s.subscribed && route != s.route {
			return status.Error(codes.InvalidArgument, "subscription route changed")
		}
		if !s.subscribed {
			s.route, s.subscribed = route, true
		}
		if err := validateReadPaths(sub.Prefix, paths, true); err != nil {
			return err
		}
		if route == routeNative {
			sub = proto.Clone(sub).(*pb.SubscriptionList)
			for i, entry := range sub.Subscription {
				paths[i] = entry.GetPath()
			}
			normalizeNativeRead(sub.Prefix, paths)
			for i, entry := range sub.Subscription {
				if entry == nil {
					return status.Error(codes.InvalidArgument, "missing subscription")
				}
				entry.Path = paths[i]
			}
			request.Request = &pb.SubscribeRequest_Subscribe{Subscribe: sub}
		}
	}
	return nil
}

func (s *redactingStream) SendMsg(msg interface{}) error {
	value, ok := msg.(*pb.SubscribeResponse)
	if !ok || value == nil {
		return invalidSecretResponse()
	}
	out := proto.Clone(value).(*pb.SubscribeResponse)
	if err := redactNotification(out.GetUpdate(), s.route); err != nil {
		return err
	}
	// Deprecated in-band errors can contain backend values, too.
	if out.GetError() != nil {
		out.GetError().Message = "gNMI read failed"
		out.GetError().Data = nil
	}
	return s.ServerStream.SendMsg(out)
}

func readError(err error) error {
	if err == nil {
		return nil
	}
	return status.Error(status.Code(err), "gNMI read failed")
}

func invalidSecretResponse() error {
	return status.Error(codes.DataLoss, "gNMI response cannot be safely redacted")
}

func localName(name string) string {
	if i := strings.IndexByte(name, ':'); i >= 0 {
		name = name[i+1:]
	}
	return strings.ToLower(strings.ReplaceAll(strings.TrimSuffix(name, "@"), "_", "-"))
}

func pathNames(paths ...*pb.Path) []string {
	return readPathNames(routeTranslated, paths...)
}

func readPathNames(route readRoute, paths ...*pb.Path) []string {
	names := rawPathNames(route == routeNative, paths...)
	for i := range names {
		names[i] = localName(names[i])
	}
	table := 0
	if route == routeNative {
		table = 2
	}
	if route != routeTranslated && len(names) > table+1 && nativeSecretTable(names[table]) {
		names[table+1] = "@entry"
	}
	return names
}

func rawPathNames(native bool, paths ...*pb.Path) []string {
	var names []string
	for _, path := range paths {
		for _, name := range path.GetElement() {
			names = append(names, name)
		}
		for _, elem := range path.GetElem() {
			names = append(names, elem.GetName())
			// Native Redis expands "key" after the DB and instance elements.
			if native && len(names) > 2 {
				if key, ok := elem.GetKey()["key"]; ok {
					names = append(names, key)
				}
			}
		}
	}
	return names
}

func readOrigin(prefix *pb.Path, paths []*pb.Path) string {
	if origin := prefix.GetOrigin(); origin != "" {
		return origin
	}
	if len(paths) > 0 {
		return paths[0].GetOrigin()
	}
	return ""
}

func responseRoute(prefix *pb.Path, paths []*pb.Path, subscribe bool) readRoute {
	origin := readOrigin(prefix, paths)
	if legacyReadTarget(prefix.GetTarget()) && (!subscribe || origin == "") {
		return routeLegacy
	}
	if origin == "sonic-db" {
		return routeNative
	}
	return routeTranslated
}

func legacyReadTarget(target string) bool {
	parts := strings.Split(target, "/")
	_, valid := spb.Target_value[parts[0]]
	return target != "OTHERS" && len(parts) <= 2 && valid
}

// Empty Path.Elem makes the native backend ignore the prefix. Materialize
// the same full scope for local handlers and forwarded read requests.
func normalizeNativeRead(prefix *pb.Path, paths []*pb.Path) {
	needed := false
	for _, p := range paths {
		needed = needed || len(p.GetElem()) == 0 || len(p.GetElement()) > 0
	}
	if !needed {
		return
	}
	elems := func(p *pb.Path) []*pb.PathElem {
		out := append([]*pb.PathElem(nil), p.GetElem()...)
		for _, name := range p.GetElement() {
			out = append(out, &pb.PathElem{Name: name})
		}
		return out
	}
	base := elems(prefix)
	for i, p := range paths {
		if p == nil {
			p = &pb.Path{}
			paths[i] = p
		}
		p.Elem = append(append([]*pb.PathElem(nil), base...), elems(p)...)
		p.Element = nil
	}
	if prefix != nil {
		prefix.Elem, prefix.Element = nil, nil
	}
}

func validateReadPaths(prefix *pb.Path, paths []*pb.Path, subscribe bool) error {
	for _, readPath := range paths {
		if mixedPath(prefix) || mixedPath(readPath) ||
			(len(prefix.GetElement()) > 0 && len(readPath.GetElem()) > 0) ||
			(len(prefix.GetElem()) > 0 && len(readPath.GetElement()) > 0) {
			return status.Error(codes.InvalidArgument, "ambiguous gNMI path encoding")
		}
		origin := readOrigin(prefix, []*pb.Path{readPath})
		db := strings.SplitN(prefix.GetTarget(), "/", 2)[0]
		table := 0
		legacy := legacyReadTarget(prefix.GetTarget()) && (!subscribe || origin == "")
		if !legacy && origin != "sonic-db" {
			continue
		}
		names := rawPathNames(!legacy, prefix, readPath)
		if !legacy {
			if len(names) < 3 || !strings.HasSuffix(names[0], "_DB") {
				continue
			}
			db, table = names[0], 2
		}
		if table >= len(names) {
			continue
		}
		for _, name := range names[table:] {
			if name == "" {
				return status.Error(codes.InvalidArgument, "empty database path component")
			}
		}
		pattern := names[table]
		// CONFIG_DB table names cannot contain the Redis key separator.
		// Otherwise a table query can select partial community keys while
		// hiding the table identity from response classification.
		if db == "CONFIG_DB" && strings.Contains(pattern, "|") {
			return status.Error(codes.InvalidArgument, "ambiguous CONFIG_DB table name")
		}
		if !strings.ContainsAny(pattern, "*?[\\") {
			continue
		}
		// Legacy CONFIG_DB/* retains table names in its root-shaped result.
		// Other table globs flatten them, losing the provenance needed to
		// distinguish credential keys and contextual "value"/"Community" leaves.
		if !subscribe && legacy && db == "CONFIG_DB" && pattern == "*" && len(names) == 1 {
			continue
		}
		pattern = conservativeTablePattern(pattern)
		for _, sensitive := range []string{"SNMP_COMMUNITY", "SNMP_TRAP_CONFIG", "NTP_KEY"} {
			match, err := path.Match(pattern, sensitive)
			if err != nil || match {
				return status.Error(codes.InvalidArgument, "ambiguous secret-bearing table wildcard")
			}
		}
	}
	return nil
}

// Redis globs can consume key separators and unusual range endpoints.
// Widen from the first metacharacter instead of parsing incompatible syntax.
func conservativeTablePattern(pattern string) string {
	if i := strings.IndexAny(pattern, "*?[\\"); i >= 0 {
		return pattern[:i] + "*"
	}
	return pattern
}

func mixedPath(path *pb.Path) bool {
	return len(path.GetElem()) > 0 && len(path.GetElement()) > 0
}

func hasName(names []string, name string) bool {
	for _, part := range names {
		if part == name || part == name+"-list" {
			return true
		}
	}
	return false
}

func secretField(names []string) bool {
	if len(names) == 0 {
		return false
	}
	field := names[len(names)-1]
	if _, err := strconv.Atoi(field); err == nil && len(names) > 1 {
		return secretField(names[:len(names)-1])
	}
	switch field {
	case "passkey", "bind-password", "auth-password",
		"snmp-user-auth-password", "snmp-user-encryption-password",
		"primary-cak", "fallback-cak", "sak", "auth-key",
		"sai-macsec-sa-attr-sak", "sai-macsec-sa-attr-auth-key",
		"secret-key", "secret-key-hashed", "admin-password",
		"admin-password-hashed", "password", "password-hashed":
		return true
	case "value", "key-value":
		return hasName(names, "ntp-key") || hasName(names, "ntp-keys")
	case "community":
		return hasName(names, "snmp-trap-config")
	case "name":
		return hasName(names, "snmp-community")
	}
	// Future credential leaves follow these spellings; policy/metadata leaves
	// such as password-policy, password-version and secret-key-type do not.
	return strings.HasSuffix(field, "-password") || strings.HasSuffix(field, "-secret") ||
		strings.HasSuffix(field, "-secret-key")
}

func secretScope(names []string) bool {
	for _, table := range []string{"tacplus", "tacplus-server", "radius", "radius-server",
		"ldap", "bgp-neighbor", "bgp-peer-group", "snmp-community", "snmp-user",
		"snmp-trap-config", "macsec-profile", "macsec-egress-sa-table",
		"macsec-ingress-sa-table", "ntp-key", "aaa", "ntp-keys"} {
		if hasName(names, table) {
			return true
		}
	}
	return false
}

func nativeSecretTable(name string) bool {
	switch name {
	case "tacplus", "tacplus-server", "radius", "radius-server", "ldap",
		"bgp-neighbor", "bgp-peer-group", "snmp-community", "snmp-user",
		"snmp-trap-config", "macsec-profile", "macsec-egress-sa-table",
		"macsec-ingress-sa-table", "ntp-key":
		return true
	}
	return false
}

func nativeJSONTable(name string) bool {
	if i := strings.IndexByte(name, ':'); i >= 0 {
		name = name[i+1:]
	}
	return name == strings.ToUpper(name) && nativeSecretTable(localName(name))
}

func redactNotification(n *pb.Notification, route readRoute) error {
	if n == nil {
		return nil
	}
	if mixedPath(n.Prefix) {
		return invalidSecretResponse()
	}
	prefixNames := readPathNames(route, n.Prefix)
	prefixEnd := 0
	for _, update := range n.Update {
		if update == nil {
			continue
		}
		if mixedPath(update.Path) {
			return invalidSecretResponse()
		}
		names := readPathNames(route, n.Prefix, update.Path)
		communityEnd := 0
		if route == routeLegacy && len(names) >= 2 && names[0] == "snmp-community" {
			communityEnd = len(names)
			// TYPE is the public RO/RW enum. Other positional suffixes may
			// be part of a delimiter-containing community, not field names.
			if len(names) > 2 && names[len(names)-1] == "type" &&
				publicCommunityType(update.Val, update.Value) {
				communityEnd--
			}
			names = append([]string{"snmp-community", "@entry"}, names[communityEnd:]...)
		}
		if communityEnd > prefixEnd {
			prefixEnd = communityEnd
		}
		table := ((route == routeLegacy && len(names) == 1) || (route == routeNative && len(names) == 3)) &&
			nativeSecretTable(names[len(names)-1])
		if err := redactValue(update.Val, names, route, table); err != nil {
			return err
		}
		if update.Value != nil {
			switch update.Value.Type {
			case pb.Encoding_JSON, pb.Encoding_JSON_IETF:
				data, err := redactJSON(update.Value.Value, names, route, table)
				if err != nil {
					return err
				}
				update.Value.Value = data
			default:
				if secretField(names) || secretScope(names) || databaseRoot(names) || bgpConfig(names) {
					return invalidSecretResponse()
				}
			}
		}
		redactPath(update.Path, prefixNames, route, communityEnd)
	}
	for _, path := range n.Delete {
		if mixedPath(path) {
			return invalidSecretResponse()
		}
		end := len(readPathNames(route, n.Prefix, path))
		if end > prefixEnd {
			prefixEnd = end
		}
		redactPath(path, prefixNames, route, end)
	}
	if len(n.Update) == 0 && len(n.Delete) == 0 {
		prefixEnd = len(prefixNames)
	}
	redactPath(n.Prefix, nil, route, prefixEnd)
	return nil
}

func publicCommunityType(value *pb.TypedValue, deprecated *pb.Value) bool {
	enum := func(s string) bool { return s == "RO" || s == "RW" }
	jsonEnum := func(data []byte) bool {
		var s string
		return json.Unmarshal(data, &s) == nil && enum(s)
	}
	if value == nil && deprecated == nil {
		return false
	}
	if value != nil {
		switch v := value.Value.(type) {
		case *pb.TypedValue_StringVal:
			if !enum(v.StringVal) {
				return false
			}
		case *pb.TypedValue_AsciiVal:
			if !enum(v.AsciiVal) {
				return false
			}
		case *pb.TypedValue_JsonVal:
			if !jsonEnum(v.JsonVal) {
				return false
			}
		case *pb.TypedValue_JsonIetfVal:
			if !jsonEnum(v.JsonIetfVal) {
				return false
			}
		default:
			return false
		}
	}
	return deprecated == nil || ((deprecated.Type == pb.Encoding_JSON ||
		deprecated.Type == pb.Encoding_JSON_IETF) && jsonEnum(deprecated.Value))
}

func redactValue(value *pb.TypedValue, names []string, route readRoute, table bool) error {
	if value == nil {
		return nil
	}
	switch v := value.Value.(type) {
	case *pb.TypedValue_JsonVal:
		data, err := redactJSON(v.JsonVal, names, route, table)
		if err != nil {
			return err
		}
		v.JsonVal = data
	case *pb.TypedValue_JsonIetfVal:
		data, err := redactJSON(v.JsonIetfVal, names, route, table)
		if err != nil {
			return err
		}
		v.JsonIetfVal = data
	case *pb.TypedValue_StringVal:
		if secretField(names) {
			v.StringVal = redacted
		} else if bgpConfig(names) {
			v.StringVal = redactBGP(v.StringVal)
		} else if (secretScope(names) && !knownFieldPath(names)) || databaseRoot(names) {
			return invalidSecretResponse()
		}
	case *pb.TypedValue_AsciiVal:
		if secretField(names) {
			v.AsciiVal = redacted
		} else if bgpConfig(names) {
			v.AsciiVal = redactBGP(v.AsciiVal)
		} else if (secretScope(names) && !knownFieldPath(names)) || databaseRoot(names) {
			return invalidSecretResponse()
		}
	case *pb.TypedValue_LeaflistVal:
		for _, element := range v.LeaflistVal.GetElement() {
			if err := redactValue(element, names, route, table); err != nil {
				return err
			}
		}
	case *pb.TypedValue_BytesVal, *pb.TypedValue_ProtoBytes, *pb.TypedValue_AnyVal:
		if secretField(names) || (secretScope(names) && !knownFieldPath(names)) || databaseRoot(names) || bgpConfig(names) {
			return invalidSecretResponse()
		}
	default:
		if secretField(names) {
			return invalidSecretResponse()
		}
	}
	return nil
}

func knownFieldPath(names []string) bool {
	// Native scalar reads have DB/instance/table/key/field. Translated scalar
	// reads have a config/state container and a leaf.
	for i, name := range names {
		if strings.HasSuffix(name, "-db") && len(names)-i >= 5 {
			return true
		}
		if (name == "config" || name == "state") && i < len(names)-1 {
			return true
		}
		if secretScope([]string{name}) && name != "aaa" && len(names)-i >= 3 {
			return true
		}
	}
	return false
}

func databaseRoot(names []string) bool {
	if len(names) == 0 {
		return true
	}
	for i, name := range names {
		if (name == "config-db" || name == "appl-db" || name == "asic-db") && len(names)-i <= 2 {
			return true
		}
	}
	return false
}

func redactJSON(data []byte, names []string, route readRoute, table bool) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value interface{}
	if decoder.Decode(&value) != nil {
		return nil, invalidSecretResponse()
	}
	var extra interface{}
	if decoder.Decode(&extra) != io.EOF {
		return nil, invalidSecretResponse()
	}
	if _, scalar := value.(string); scalar && !secretField(names) && !bgpConfig(names) &&
		(databaseRoot(names) || (secretScope(names) && !knownFieldPath(names))) {
		return nil, invalidSecretResponse()
	}
	value = redactObject(value, names, route, table)
	out, err := json.Marshal(value)
	if err != nil {
		return nil, invalidSecretResponse()
	}
	return out, nil
}

func redactObject(value interface{}, names []string, route readRoute, table bool) interface{} {
	if secretField(names) {
		return redacted
	}
	switch data := value.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(data))
		keys := make([]string, 0, len(data))
		for key := range data {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for i, key := range keys {
			childNames := append(append([]string(nil), names...), localName(key))
			outKey := key
			childTable := route != routeTranslated && !hasName(names, "@entry") && nativeJSONTable(key)
			// Native table identifiers are data, not credential leaf names.
			if table {
				if names[len(names)-1] == "snmp-community" {
					outKey = redacted + "_" + strconv.Itoa(i)
				}
				childNames[len(childNames)-1] = "@entry"
				childTable = false
				if _, entry := data[key].(map[string]interface{}); !entry {
					out[outKey] = redacted
					continue
				}
			}
			out[outKey] = redactObject(data[key], childNames, route, childTable)
		}
		return out
	case []interface{}:
		for i, item := range data {
			childNames := names
			if table {
				childNames = append(append([]string(nil), names...), "@entry")
			}
			data[i] = redactObject(item, childNames, route, false)
		}
	case string:
		if bgpConfig(names) {
			return redactBGP(data)
		}
	}
	return value
}

func bgpConfig(names []string) bool {
	return hasName(names, "bgp") && hasName(names, "running-config")
}

var bgpPassword = regexp.MustCompile(`(?mi)^([ \t]*(?:no[ \t]+)?(?:(?:neighbor|username)[ \t]+\S+[ \t]+|enable[ \t]+)?)(password|secret)[ \t]+[^\r\n]*`)

func redactBGP(text string) string {
	return bgpPassword.ReplaceAllString(text, "${1}${2} "+redacted)
}

func redactPath(path *pb.Path, prefix []string, route readRoute, communityEnd int) {
	if path == nil {
		return
	}
	names := append([]string(nil), prefix...)
	redactName := func(name string, schemaList bool) string {
		if route == routeLegacy && hasName(names, "snmp-community") && len(names) < communityEnd {
			return redacted
		}
		if route == routeLegacy && communityEnd > 0 && len(names) > 0 && names[0] == "snmp-community" {
			return name
		}
		if route == routeNative {
			if len(names) == 3 && names[2] == "snmp-community" {
				return redacted
			}
			return name
		}
		if len(names) > 0 && names[len(names)-1] == "snmp-community" && !schemaList {
			return redacted
		}
		return name
	}
	for i, name := range path.Element {
		path.Element[i] = redactName(name, false)
		names = append(names, localName(name))
	}
	for _, elem := range path.Elem {
		if elem == nil {
			continue
		}
		name := elem.Name
		elem.Name = redactName(name, route == routeTranslated &&
			localName(name) == "snmp-community-list" && hasName(names, "sonic-snmp"))
		names = append(names, localName(name))
		if route == routeNative && len(names) > 2 {
			if key, ok := elem.Key["key"]; ok {
				if len(names) == 3 && names[2] == "snmp-community" {
					elem.Key["key"] = redacted
				}
				names = append(names, localName(key))
			}
		}
		for key := range elem.Key {
			if secretField(append(append([]string(nil), names...), localName(key))) {
				elem.Key[key] = redacted
			}
		}
	}
}
