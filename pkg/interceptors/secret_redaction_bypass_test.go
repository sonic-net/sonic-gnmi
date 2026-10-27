package interceptors

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	pb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func testCommunityIdentityWire(t *testing.T, client pb.GNMIClient, backend *secretFixtureServer) {
	identifiers := []string{"fixture-alpha", "fixture-zeta", "Fixture-alpha", "fixture:alpha",
		"fixture-alpha||fixture-zeta", redacted + "_" + strings.Repeat("0", 64),
		"fixture_alpha", "another:alpha", "fixture-zeta|TYPE"}
	for _, route := range []readRoute{routeNative, routeLegacy, routeTranslated} {
		t.Run([]string{"translated", "legacy", "native"}[route], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			prefix := &pb.Path{}
			table := testPath("SNMP_COMMUNITY")
			switch route {
			case routeNative:
				prefix.Origin = "sonic-db"
				table = testPath("CONFIG_DB", "localhost", "SNMP_COMMUNITY")
			case routeLegacy:
				prefix.Target = "CONFIG_DB"
			case routeTranslated:
				table = testPath("sonic-snmp:sonic-snmp", "SNMP_COMMUNITY", "SNMP_COMMUNITY_LIST")
			}
			payload := func(entries []string, snapshot bool) *pb.TypedValue {
				dictionary := make(map[string]interface{})
				var list []interface{}
				for _, identifier := range entries {
					enum := "RO"
					if snapshot && identifier == identifiers[1] {
						enum = "RW"
					}
					dictionary[identifier] = map[string]string{"TYPE": enum}
					list = append(list, map[string]string{"name": identifier, "TYPE": enum})
				}
				var value interface{} = dictionary
				if route == routeTranslated {
					value = list
				}
				data, _ := json.Marshal(value)
				return &pb.TypedValue{Value: &pb.TypedValue_JsonIetfVal{JsonIetfVal: data}}
			}
			notifications := []*pb.Notification{
				{Prefix: proto.Clone(prefix).(*pb.Path), Update: []*pb.Update{{
					Path: proto.Clone(table).(*pb.Path), Val: payload(identifiers, true),
				}}},
				{Prefix: proto.Clone(prefix).(*pb.Path), Update: []*pb.Update{{
					Path: proto.Clone(table).(*pb.Path), Val: payload(identifiers[1:2], false),
				}}},
			}
			for i, identifier := range identifiers {
				path := proto.Clone(table).(*pb.Path)
				switch route {
				case routeNative:
					if i%2 == 0 {
						path.Elem[len(path.Elem)-1].Key = map[string]string{"key": identifier}
					} else {
						path.Elem = append(path.Elem, &pb.PathElem{Name: identifier})
					}
				case routeLegacy:
					for _, component := range strings.Split(identifier, "|") {
						path.Elem = append(path.Elem, &pb.PathElem{Name: component})
					}
				case routeTranslated:
					path.Elem[len(path.Elem)-1].Key = map[string]string{"name": identifier}
				}
				if i%2 != 0 && route != routeTranslated {
					path.Element, path.Elem = rawPathNames(false, path), nil
				}
				relativePrefix := proto.Clone(prefix).(*pb.Path)
				entry := &pb.Update{Path: path, Val: &pb.TypedValue{
					Value: &pb.TypedValue_JsonVal{JsonVal: []byte(`{"TYPE":"RO"}`)},
				}}
				if i%2 != 0 {
					entry.Val = nil
					entry.Value = &pb.Value{Type: pb.Encoding_JSON, Value: []byte(`{"TYPE":"RO"}`)}
					relativePrefix.Elem, relativePrefix.Element = path.Elem, path.Element
					entry.Path = &pb.Path{}
				}
				updates := []*pb.Update{entry}
				if route == routeTranslated {
					namePath := proto.Clone(entry.Path).(*pb.Path)
					namePath.Elem = append(namePath.Elem, &pb.PathElem{Name: "name"})
					scalar := &pb.Update{Path: namePath, Val: &pb.TypedValue{
						Value: &pb.TypedValue_StringVal{StringVal: identifier},
					}}
					encoded, _ := json.Marshal(identifier)
					switch i % 4 {
					case 1:
						scalar.Val.Value = &pb.TypedValue_AsciiVal{AsciiVal: identifier}
					case 2:
						scalar.Val.Value = &pb.TypedValue_JsonVal{JsonVal: encoded}
					case 3:
						scalar.Val = nil
						scalar.Value = &pb.Value{Type: pb.Encoding_JSON_IETF, Value: encoded}
					}
					updates = append(updates, scalar)
				}
				notifications = append(notifications, &pb.Notification{Prefix: relativePrefix, Update: updates},
					&pb.Notification{Prefix: proto.Clone(prefix).(*pb.Path), Delete: []*pb.Path{proto.Clone(path).(*pb.Path)}})
			}
			backend.get = nil
			backend.response = &pb.GetResponse{Notification: notifications}
			check := func(response []*pb.Notification) string {
				t.Helper()
				if len(response) != len(notifications) {
					t.Fatal("missing snapshot or delta notifications")
				}
				for _, notification := range response {
					wire, err := proto.Marshal(notification)
					if err != nil {
						t.Fatal(err)
					}
					for _, identifier := range identifiers {
						if strings.Contains(string(wire), identifier) {
							t.Fatal("serialized notification contains a synthetic community")
						}
					}
				}
				decode := func(update *pb.Update) map[string]string {
					t.Helper()
					data := update.GetVal().GetJsonIetfVal()
					if route == routeTranslated {
						var list []map[string]string
						if err := json.Unmarshal(data, &list); err != nil {
							t.Fatal(err)
						}
						out := make(map[string]string)
						for _, entry := range list {
							out[entry["name"]] = entry["TYPE"]
						}
						return out
					}
					var dictionary map[string]map[string]string
					if err := json.Unmarshal(data, &dictionary); err != nil {
						t.Fatal(err)
					}
					out := make(map[string]string)
					for identifier, fields := range dictionary {
						out[identifier] = fields["TYPE"]
					}
					return out
				}
				snapshot := decode(response[0].Update[0])
				if len(snapshot) != len(identifiers) {
					t.Fatal("distinct communities collapsed in the snapshot")
				}
				target := ""
				for identifier, enum := range snapshot {
					if enum == "RW" {
						target = identifier
					}
				}
				delta := decode(response[1].Update[0])
				if target == "" || len(delta) != 1 || delta[target] != "RO" {
					t.Fatal("single-entry delta changed community identity")
				}
				pathIdentity := func(prefix, path *pb.Path) string {
					if route == routeTranslated {
						for _, part := range []*pb.Path{prefix, path} {
							for _, elem := range part.GetElem() {
								if value, ok := elem.Key["name"]; ok {
									return value
								}
							}
						}
						return ""
					}
					names := rawPathNames(route == routeNative, prefix, path)
					start := 1
					if route == routeNative {
						start = 3
					}
					return strings.Join(names[start:], "|")
				}
				seen := make(map[string]bool)
				for i := range identifiers {
					entry, deleted := response[2+2*i], response[3+2*i]
					identity := pathIdentity(entry.Prefix, entry.Update[0].Path)
					if _, ok := snapshot[identity]; !ok || seen[identity] {
						t.Fatal("entry path did not preserve a distinct snapshot identity")
					}
					seen[identity] = true
					if pathIdentity(deleted.Prefix, deleted.Delete[0]) != identity {
						t.Fatal("delete did not match the entry identity")
					}
					if route == routeTranslated {
						scalar := entry.Update[1]
						name := scalar.GetVal().GetStringVal()
						if i%4 == 1 {
							name = scalar.GetVal().GetAsciiVal()
						} else if i%4 >= 2 {
							data := scalar.GetVal().GetJsonVal()
							if scalar.Value != nil {
								data = scalar.Value.Value
							}
							if err := json.Unmarshal(data, &name); err != nil {
								t.Fatal(err)
							}
						}
						if name != identity {
							t.Fatal("scalar name did not match the list and path identity")
						}
					}
				}
				return target
			}
			previous := ""
			for i := 0; i < 2; i++ {
				result, err := client.Get(ctx, &pb.GetRequest{Prefix: prefix, Path: []*pb.Path{table}})
				if err != nil {
					t.Fatal(err)
				}
				identity := check(result.Notification)
				if identity == previous {
					t.Fatal("community identity was shared across Get RPCs")
				}
				previous = identity
			}
			for _, mode := range []pb.SubscriptionList_Mode{pb.SubscriptionList_STREAM, pb.SubscriptionList_POLL} {
				stream, err := client.Subscribe(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if err := stream.Send(&pb.SubscribeRequest{Request: &pb.SubscribeRequest_Subscribe{
					Subscribe: &pb.SubscriptionList{Prefix: prefix, Mode: mode,
						Subscription: []*pb.Subscription{{Path: table}}},
				}}); err != nil {
					t.Fatal(err)
				}
				var received []*pb.Notification
				for {
					message, err := stream.Recv()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Fatal(err)
					}
					received = append(received, message.GetUpdate())
				}
				identity := check(received)
				if identity == previous {
					t.Fatal("community identity was shared across read scopes")
				}
				previous = identity
			}
		})
	}
}

func TestSecretRedactionEntropyFailure(t *testing.T) {
	reader := rand.Reader
	rand.Reader = strings.NewReader("")
	defer func() { rand.Reader = reader }()
	called := false
	value, err := (&secretRedactor{}).UnaryInterceptor()(context.Background(), &pb.GetRequest{},
		&grpc.UnaryServerInfo{FullMethod: gnmiGetMethod},
		func(context.Context, interface{}) (interface{}, error) {
			called = true
			return &pb.GetResponse{}, nil
		})
	if called || value != nil || status.Code(err) != codes.DataLoss {
		t.Fatal("entropy failure did not stop Get before backend dispatch")
	}
	err = (&secretRedactor{}).StreamInterceptor()(nil, nil,
		&grpc.StreamServerInfo{FullMethod: "/gnmi.gNMI/Subscribe"},
		func(interface{}, grpc.ServerStream) error { called = true; return nil })
	if called || status.Code(err) != codes.DataLoss {
		t.Fatal("entropy failure did not stop Subscribe before backend dispatch")
	}
	value, err = (&secretRedactor{}).UnaryInterceptor()(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: "/gnoi.jwt.Jwt/Authenticate"},
		func(context.Context, interface{}) (interface{}, error) { return syntheticSecret, nil })
	if err != nil || value != syntheticSecret {
		t.Fatal("entropy failure changed authentication RPCs")
	}
}

func TestSecretRedactionSNMPKeys(t *testing.T) {
	for i, data := range []string{
		`{"SNMP_COMMUNITY":{"synthetic-only-credential":{"TYPE":"RO"},"SNMP_COMMUNITY_LIST":{"TYPE":"RO"},"synthetic-only-credential:SNMP_COMMUNITY_LIST":[{"TYPE":"RO"}]}}`,
		`{"sonic-snmp:sonic-snmp":{"SNMP_COMMUNITY":{"SNMP_COMMUNITY_LIST":[{"name":"synthetic-only-credential","TYPE":"RO"}]}}}`,
		`{"SNMP_COMMUNITY":[{"name":"synthetic-only-credential","TYPE":"RO"}]}`,
	} {
		response := &pb.GetResponse{Notification: []*pb.Notification{{Update: []*pb.Update{{
			Val: &pb.TypedValue{Value: &pb.TypedValue_JsonIetfVal{JsonIetfVal: []byte(data)}},
		}}}}}
		route := routeLegacy
		if i > 0 {
			route = routeTranslated
		}
		err := redactTestNotification(response.Notification[0], route)
		if err != nil {
			t.Fatal(err)
		}
		wire, _ := proto.Marshal(response)
		if strings.Contains(string(wire), syntheticSecret) || !strings.Contains(string(wire), `"TYPE":"RO"`) {
			t.Fatal("SNMP credential or non-secret entry metadata handled incorrectly")
		}
	}
	prefix := testPath("CONFIG_DB", "localhost", "SNMP_COMMUNITY")
	response := &pb.GetResponse{Notification: []*pb.Notification{{
		Prefix: prefix,
		Update: []*pb.Update{{
			Path: testPath(syntheticSecret, "TYPE"),
			Val:  &pb.TypedValue{Value: &pb.TypedValue_StringVal{StringVal: "RO"}},
		}},
	}}}
	out, err := redactTestGet(response)
	if err != nil {
		t.Fatal(err)
	}
	wire, _ := proto.Marshal(out)
	if strings.Contains(string(wire), syntheticSecret) || !strings.Contains(string(wire), "RO") {
		t.Fatal("SNMP prefix-relative response leaked its key or lost the public field")
	}
	logPaths := requestPaths(&pb.GetRequest{Prefix: prefix, Path: []*pb.Path{testPath(syntheticSecret, "TYPE")}})
	if strings.Contains(strings.Join(logPaths, " "), syntheticSecret) {
		t.Fatal("completion logging leaked a native SNMP credential key")
	}
}

func TestSecretRedactionBGPAndDeprecatedValue(t *testing.T) {
	text := "router bgp 65000\n neighbor 192.0.2.1 password 8 " + syntheticSecret +
		"\n neighbor 192.0.2.1 remote-as 65001\n"
	encoded, _ := json.Marshal(text)
	for _, value := range []*pb.TypedValue{
		{Value: &pb.TypedValue_JsonIetfVal{JsonIetfVal: encoded}},
		{Value: &pb.TypedValue_StringVal{StringVal: text}},
		{Value: &pb.TypedValue_AsciiVal{AsciiVal: text}},
	} {
		response := &pb.GetResponse{Notification: []*pb.Notification{{
			Prefix: testPath("bgp"), Update: []*pb.Update{{Path: testPath("running-config"), Val: value}},
		}}}
		out, err := redactTestGet(response)
		if err != nil {
			t.Fatal(err)
		}
		wire, _ := proto.Marshal(out)
		if strings.Contains(string(wire), syntheticSecret) || !strings.Contains(string(wire), "remote-as 65001") {
			t.Fatal("BGP password leaked or non-secret configuration disappeared")
		}
	}
	response := &pb.GetResponse{Notification: []*pb.Notification{{Update: []*pb.Update{{
		Path: testPath("MACSEC_EGRESS_SA_TABLE", "fixture", "sak"),
		Val: &pb.TypedValue{Value: &pb.TypedValue_LeaflistVal{LeaflistVal: &pb.ScalarArray{
			Element: []*pb.TypedValue{{Value: &pb.TypedValue_StringVal{StringVal: syntheticSecret}}},
		}}},
		Value: &pb.Value{Type: pb.Encoding_JSON, Value: []byte(`"synthetic-only-credential"`)},
	}}}}}
	out, err := redactTestGet(response)
	if err != nil {
		t.Fatal(err)
	}
	wire, _ := proto.Marshal(out)
	if strings.Contains(string(wire), syntheticSecret) {
		t.Fatal("leaf-list or deprecated Value leaked the synthetic credential")
	}
}

func TestSecretRedactionCommunityComponents(t *testing.T) {
	for _, deprecated := range []bool{false, true} {
		for _, split := range []int{0, 1, 2, 3} {
			for _, field := range []bool{false, true} {
				for _, oldPath := range []bool{false, true} {
					names := []string{"SNMP_COMMUNITY", "fixture", syntheticSecret}
					value := &pb.TypedValue{Value: &pb.TypedValue_JsonIetfVal{JsonIetfVal: []byte(`{"TYPE":"RO"}`)}}
					if field {
						names = append(names, "TYPE")
						value = &pb.TypedValue{Value: &pb.TypedValue_StringVal{StringVal: "RO"}}
					}
					prefix, path := testPath(names[:split]...), testPath(names[split:]...)
					prefix.Target = "CONFIG_DB"
					if oldPath {
						prefix.Elem, path.Elem = nil, nil
						prefix.Element, path.Element = names[:split], names[split:]
					}
					update := &pb.Update{Path: path, Val: value}
					if deprecated {
						data := []byte(`{"TYPE":"RO"}`)
						if field {
							data = []byte(`"RO"`)
						}
						update.Val = nil
						update.Value = &pb.Value{Type: pb.Encoding_JSON, Value: data}
					}
					n := &pb.Notification{Prefix: prefix, Update: []*pb.Update{update}}
					if err := redactTestNotification(n, routeLegacy); err != nil {
						t.Fatal(err)
					}
					wire, _ := proto.Marshal(n)
					if strings.Contains(string(wire), syntheticSecret) {
						t.Fatal("composite community component survived serialization")
					}
					outNames := rawPathNames(false, n.Prefix, update.Path)
					for i, name := range outNames {
						if i > 0 && !(field && i == len(outNames)-1) && !strings.HasPrefix(name, redacted+"_") {
							t.Fatal("community component was not masked")
						}
					}
					if field && outNames[len(outNames)-1] != "TYPE" {
						t.Fatal("public TYPE field path was hidden")
					}
					logged := requestPaths(&pb.GetRequest{Prefix: testPath(names[:split]...), Path: []*pb.Path{testPath(names[split:]...)}})
					if strings.Contains(strings.Join(logged, " "), syntheticSecret) {
						t.Fatal("completion log retained a composite credential component")
					}
				}
			}
		}
	}
	for _, route := range []readRoute{routeLegacy, routeNative} {
		prefix := &pb.Path{Target: "CONFIG_DB"}
		path := testPath("SNMP_COMMUNITY", syntheticSecret+":SNMP_COMMUNITY_LIST")
		value := &pb.TypedValue{Value: &pb.TypedValue_JsonIetfVal{JsonIetfVal: []byte(`{"TYPE":"RO"}`)}}
		if route == routeNative {
			prefix = &pb.Path{Origin: "sonic-db"}
			path = testPath("CONFIG_DB", "localhost", "SNMP_COMMUNITY", syntheticSecret+":SNMP_COMMUNITY_LIST")
			value = &pb.TypedValue{Value: &pb.TypedValue_StringVal{StringVal: "RO"}}
		}
		path.Elem[len(path.Elem)-1].Key = map[string]string{"key": "TYPE"}
		n := &pb.Notification{Prefix: prefix, Update: []*pb.Update{{Path: path, Val: value}},
			Delete: []*pb.Path{proto.Clone(path).(*pb.Path)}}
		if err := redactTestNotification(n, route); err != nil {
			t.Fatal(err)
		}
		wire, _ := proto.Marshal(n)
		if strings.Contains(string(wire), syntheticSecret) {
			t.Fatal("schema-like community identifier bypassed route-aware masking")
		}
	}
}

func TestSecretRedactionCommunityUnknownRepresentation(t *testing.T) {
	for _, value := range []*pb.TypedValue{
		{Value: &pb.TypedValue_StringVal{StringVal: syntheticSecret}},
		{Value: &pb.TypedValue_BytesVal{BytesVal: []byte(syntheticSecret)}},
		{Value: &pb.TypedValue_JsonIetfVal{JsonIetfVal: []byte(`"synthetic-only-credential"`)}},
	} {
		n := &pb.Notification{Update: []*pb.Update{{
			Path: testPath("SNMP_COMMUNITY", "fixture", "TYPE"), Val: value,
		}}}
		if status.Code(redactTestNotification(n, routeLegacy)) != codes.DataLoss {
			t.Fatal("unproven public TYPE representation was accepted")
		}
	}
}

func TestSecretRedactionBGPCommandPositions(t *testing.T) {
	public := " neighbor 192.0.2.1 description secret backup link\n" +
		" neighbor password peer-group\n neighbor password remote-as 65001\n" +
		" neighbor secret description password backup\n"
	credentials := " neighbor 192.0.2.1 password 8 " + syntheticSecret + "\n" +
		" enable secret " + syntheticSecret + "\n" +
		" password " + syntheticSecret + "\n" +
		" username fixture password " + syntheticSecret + "\n"
	out := redactBGP(public + credentials)
	if !strings.HasPrefix(out, public) || strings.Contains(out, syntheticSecret) {
		t.Fatal("BGP command redaction leaked a credential or hid public text")
	}
}

type shortCircuitRead struct {
	secretRedactor
}

func (*shortCircuitRead) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(context.Context, interface{}, *grpc.UnaryServerInfo, grpc.UnaryHandler) (interface{}, error) {
		return (&secretFixtureServer{}).Get(context.Background(), &pb.GetRequest{})
	}
}

func TestSecretRedactionShortCircuitAndAuth(t *testing.T) {
	chain := NewChain(&secretRedactor{}, &shortCircuitRead{})
	result, err := chain.UnaryInterceptor()(context.Background(), &pb.GetRequest{},
		&grpc.UnaryServerInfo{FullMethod: gnmiGetMethod},
		func(context.Context, interface{}) (interface{}, error) {
			t.Fatal("forwarded response should short-circuit the local handler")
			return nil, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	assertRedactedWire(t, result.(*pb.GetResponse))
	for _, method := range []string{gnmiGetMethod, "/gnoi.jwt.Jwt/Authenticate"} {
		result, err = (&secretRedactor{}).UnaryInterceptor()(context.Background(), nil,
			&grpc.UnaryServerInfo{FullMethod: method},
			func(context.Context, interface{}) (interface{}, error) {
				if method == gnmiGetMethod {
					return nil, status.Error(codes.NotFound, syntheticSecret)
				}
				return syntheticSecret, nil
			})
		if method == gnmiGetMethod {
			if status.Code(err) != codes.NotFound || strings.Contains(err.Error(), syntheticSecret) {
				t.Fatal("read error leaked a backend value or lost its status code")
			}
		} else if err != nil || result != syntheticSecret {
			t.Fatal("intentional authentication response was changed")
		}
	}
}
