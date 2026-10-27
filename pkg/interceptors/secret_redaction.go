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
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const redacted = "REDACTED"

type secretRedactor struct{}

func (*secretRedactor) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if info.FullMethod == gnmiGetMethod {
			if request, ok := req.(*pb.GetRequest); ok {
				if err := validateReadPaths(request.GetPrefix(), request.GetPath()); err != nil {
					return nil, err
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
			if err := redactNotification(notification); err != nil {
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
		return validateReadPaths(sub.Prefix, paths)
	}
	return nil
}

func (s *redactingStream) SendMsg(msg interface{}) error {
	value, ok := msg.(*pb.SubscribeResponse)
	if !ok || value == nil {
		return invalidSecretResponse()
	}
	out := proto.Clone(value).(*pb.SubscribeResponse)
	if err := redactNotification(out.GetUpdate()); err != nil {
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
	names := rawPathNames(paths...)
	for i := range names {
		names[i] = localName(names[i])
	}
	return names
}

func rawPathNames(paths ...*pb.Path) []string {
	var names []string
	for _, path := range paths {
		for _, name := range path.GetElement() {
			names = append(names, name)
		}
		for _, elem := range path.GetElem() {
			names = append(names, elem.GetName())
		}
	}
	return names
}

func validateReadPaths(prefix *pb.Path, paths []*pb.Path) error {
	for _, readPath := range paths {
		if mixedPath(prefix) || mixedPath(readPath) {
			return status.Error(codes.InvalidArgument, "ambiguous gNMI path encoding")
		}
		names := rawPathNames(prefix, readPath)
		db := strings.SplitN(prefix.GetTarget(), "/", 2)[0]
		table := 0
		legacy := strings.HasSuffix(db, "_DB")
		if !legacy {
			if len(names) < 3 || !strings.HasSuffix(names[0], "_DB") {
				continue
			}
			db, table = names[0], 2
		}
		if table >= len(names) {
			continue
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
		if legacy && db == "CONFIG_DB" && pattern == "*" && len(names) == 1 {
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

// Redis ranges can consume an apparent closing bracket as a range endpoint.
// Widen from the first class onward instead of parsing incompatible syntax.
func conservativeTablePattern(pattern string) string {
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '\\':
			if i+1 == len(pattern) {
				return "*"
			}
			i++
		case '[':
			return pattern[:i] + "*"
		}
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

func redactNotification(n *pb.Notification) error {
	if n == nil {
		return nil
	}
	if mixedPath(n.Prefix) {
		return invalidSecretResponse()
	}
	for _, update := range n.Update {
		if update == nil {
			continue
		}
		if mixedPath(update.Path) {
			return invalidSecretResponse()
		}
		names := pathNames(n.Prefix, update.Path)
		if err := redactValue(update.Val, names); err != nil {
			return err
		}
		if update.Value != nil {
			switch update.Value.Type {
			case pb.Encoding_JSON, pb.Encoding_JSON_IETF:
				data, err := redactJSON(update.Value.Value, names)
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
		redactPath(update.Path, pathNames(n.Prefix))
	}
	for _, path := range n.Delete {
		if mixedPath(path) {
			return invalidSecretResponse()
		}
		redactPath(path, pathNames(n.Prefix))
	}
	redactPath(n.Prefix, nil)
	return nil
}

func redactValue(value *pb.TypedValue, names []string) error {
	if value == nil {
		return nil
	}
	switch v := value.Value.(type) {
	case *pb.TypedValue_JsonVal:
		data, err := redactJSON(v.JsonVal, names)
		if err != nil {
			return err
		}
		v.JsonVal = data
	case *pb.TypedValue_JsonIetfVal:
		data, err := redactJSON(v.JsonIetfVal, names)
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
			if err := redactValue(element, names); err != nil {
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

func redactJSON(data []byte, names []string) ([]byte, error) {
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
	value = redactObject(value, names)
	out, err := json.Marshal(value)
	if err != nil {
		return nil, invalidSecretResponse()
	}
	return out, nil
}

func redactObject(value interface{}, names []string) interface{} {
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
			// Native SNMP communities are dictionary keys, not field values.
			_, list := data[key].([]interface{})
			if len(names) > 0 && names[len(names)-1] == "snmp-community" &&
				!(localName(key) == "snmp-community-list" && list) {
				outKey = redacted + "_" + strconv.Itoa(i)
				childNames[len(childNames)-1] = "@entry"
			}
			out[outKey] = redactObject(data[key], childNames)
		}
		return out
	case []interface{}:
		for i, item := range data {
			childNames := names
			if len(names) > 0 && names[len(names)-1] == "snmp-community" {
				childNames = append(append([]string(nil), names...), "@list-entry")
			}
			data[i] = redactObject(item, childNames)
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

var bgpPassword = regexp.MustCompile(`(?mi)(^|[ \t])(password|secret)([ \t]+)[^\r\n]*`)

func redactBGP(text string) string {
	return bgpPassword.ReplaceAllString(text, "${1}${2} "+redacted)
}

func redactPath(path *pb.Path, prefix []string) {
	if path == nil {
		return
	}
	names := append([]string(nil), prefix...)
	redactName := func(name string, schemaList bool) string {
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
		elem.Name = redactName(name, localName(name) == "snmp-community-list" &&
			(len(elem.Key) > 0 || hasName(names, "sonic-snmp")))
		names = append(names, localName(name))
		for key := range elem.Key {
			if secretField(append(append([]string(nil), names...), localName(key))) {
				elem.Key[key] = redacted
			}
		}
	}
}
