package interceptors

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	pb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

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
		err := redactNotification(response.Notification[0], route)
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
					if err := redactNotification(n, routeLegacy); err != nil {
						t.Fatal(err)
					}
					wire, _ := proto.Marshal(n)
					if strings.Contains(string(wire), syntheticSecret) {
						t.Fatal("composite community component survived serialization")
					}
					outNames := rawPathNames(false, n.Prefix, update.Path)
					for i, name := range outNames {
						if i > 0 && !(field && i == len(outNames)-1) && name != redacted {
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
		if err := redactNotification(n, route); err != nil {
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
		if status.Code(redactNotification(n, routeLegacy)) != codes.DataLoss {
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
