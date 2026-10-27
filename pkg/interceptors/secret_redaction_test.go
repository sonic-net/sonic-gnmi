package interceptors

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	pb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
)

const syntheticSecret = "synthetic-only-credential"

type secretFixtureServer struct {
	pb.UnimplementedGNMIServer
	response *pb.GetResponse
}

func (s *secretFixtureServer) Get(_ context.Context, req *pb.GetRequest) (*pb.GetResponse, error) {
	if s.response != nil {
		return proto.Clone(s.response).(*pb.GetResponse), nil
	}
	// Synthetic backend retrieval; no SONiC configuration is read.
	data := map[string]interface{}{
		"TACPLUS_SERVER": map[string]interface{}{
			"192.0.2.1": map[string]interface{}{"passkey": syntheticSecret, "priority": "7"},
		},
	}
	value, _ := json.Marshal(data)
	return &pb.GetResponse{Notification: []*pb.Notification{{
		Prefix: req.GetPrefix(),
		Update: []*pb.Update{{Val: &pb.TypedValue{Value: &pb.TypedValue_JsonIetfVal{JsonIetfVal: value}}}},
	}}}, nil
}

func (s *secretFixtureServer) Subscribe(stream pb.GNMI_SubscribeServer) error {
	req, err := stream.Recv()
	if err != nil {
		return err
	}
	resp, _ := s.Get(stream.Context(), &pb.GetRequest{Prefix: req.GetSubscribe().GetPrefix()})
	return stream.Send(&pb.SubscribeResponse{Response: &pb.SubscribeResponse_Update{Update: resp.Notification[0]}})
}

func TestSecretRedactionWire(t *testing.T) {
	chain, err := NewServerChain()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = chain.Close() })
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer(chain.GetServerOptions()...)
	backend := &secretFixtureServer{}
	pb.RegisterGNMIServer(server, backend)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	t.Cleanup(func() { _ = listener.Close() })
	conn, err := grpc.NewClient("passthrough:///redaction",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := pb.NewGNMIClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	prefix := &pb.Path{Origin: "sonic-db", Elem: []*pb.PathElem{{Name: "CONFIG_DB"}, {Name: "localhost"}}}
	got, err := client.Get(ctx, &pb.GetRequest{Prefix: prefix})
	if err != nil {
		t.Fatal(err)
	}
	assertRedactedWire(t, got)
	for _, mode := range []pb.SubscriptionList_Mode{pb.SubscriptionList_ONCE, pb.SubscriptionList_POLL, pb.SubscriptionList_STREAM} {
		t.Run(mode.String(), func(t *testing.T) {
			stream, err := client.Subscribe(ctx)
			if err != nil {
				t.Fatal(err)
			}
			err = stream.Send(&pb.SubscribeRequest{Request: &pb.SubscribeRequest_Subscribe{
				Subscribe: &pb.SubscriptionList{Prefix: prefix, Mode: mode},
			}})
			if err != nil {
				t.Fatal(err)
			}
			resp, err := stream.Recv()
			if err != nil {
				t.Fatal(err)
			}
			assertRedactedWire(t, resp)
			if _, err := stream.Recv(); err != io.EOF {
				t.Fatal("expected end of synthetic stream")
			}
		})
	}
	for _, readPrefix := range []*pb.Path{prefix, {Target: "CONFIG_DB"}} {
		for _, pattern := range []string{"NTP_KE?", "NTP_KE[Z-A]", "NTP_K[E-]x]Y", "SNMP_COMMUNITY|prefix",
			"SNMP_COMMUNITY|*", "SNMP_COMMUNITY?*", "NTP_KEY?*"} {
			stream, err := client.Subscribe(ctx)
			if err != nil {
				t.Fatal(err)
			}
			err = stream.Send(&pb.SubscribeRequest{Request: &pb.SubscribeRequest_Subscribe{
				Subscribe: &pb.SubscriptionList{
					Prefix:       readPrefix,
					Subscription: []*pb.Subscription{{Path: testPath(pattern)}},
				},
			}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := stream.Recv(); status.Code(err) != codes.InvalidArgument {
				t.Error("ambiguous table glob reached streaming retrieval")
			}
		}
	}
	for _, read := range []struct{ prefix, path *pb.Path }{
		{&pb.Path{Target: "CONFIG_DB", Element: []string{"PORT"}}, testPath("NTP_KE?")},
		{&pb.Path{Target: "CONFIG_DB", Origin: "sonic-db"}, testPath("CONFIG_DB", "localhost", "NTP_KE?")},
		{&pb.Path{Target: "CONFIG_DB"}, &pb.Path{Origin: "sonic-db", Elem: testPath("CONFIG_DB", "localhost", "NTP_KE?").Elem}},
		{&pb.Path{Target: "CONFIG_DB"}, testPath("*")},
	} {
		for _, mode := range []pb.SubscriptionList_Mode{pb.SubscriptionList_ONCE, pb.SubscriptionList_POLL, pb.SubscriptionList_STREAM} {
			stream, err := client.Subscribe(ctx)
			if err != nil {
				t.Fatal(err)
			}
			err = stream.Send(&pb.SubscribeRequest{Request: &pb.SubscribeRequest_Subscribe{
				Subscribe: &pb.SubscriptionList{
					Prefix: read.prefix, Mode: mode, Subscription: []*pb.Subscription{{Path: read.path}},
				},
			}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := stream.Recv(); status.Code(err) != codes.InvalidArgument {
				t.Error("subscription routing ambiguity reached retrieval")
			}
		}
	}
	for _, fixture := range []struct {
		path   *pb.Path
		value  *pb.TypedValue
		public string
	}{
		{testPath("ASIC_DB", "localhost", "ASIC_STATE", "SAI_OBJECT_TYPE_MACSEC_SA:fixture"),
			&pb.TypedValue{Value: &pb.TypedValue_StringVal{StringVal: syntheticSecret}}, redacted},
		{testPath("CONFIG_DB", "localhost", "SNMP_COMMUNITY"),
			&pb.TypedValue{Value: &pb.TypedValue_JsonIetfVal{JsonIetfVal: []byte(`{"TYPE":"RO"}`)}}, `"TYPE":"RO"`},
	} {
		key := syntheticSecret + "|suffix-password"
		if fixture.public == redacted {
			key = "SAI_MACSEC_SA_ATTR_SAK"
		}
		fixture.path.Elem[len(fixture.path.Elem)-1].Key = map[string]string{"key": key}
		for _, target := range []string{"", "UNRECOGNIZED_DB", "CONFIG_DB/ns/extra"} {
			readPrefix := &pb.Path{Origin: "sonic-db", Target: target}
			backend.response = &pb.GetResponse{Notification: []*pb.Notification{{
				Prefix: readPrefix, Update: []*pb.Update{{Path: fixture.path, Val: fixture.value}},
			}}}
			assertValue := func(response proto.Message) {
				t.Helper()
				wire, err := proto.Marshal(response)
				if err != nil || strings.Contains(string(wire), syntheticSecret) || !strings.Contains(string(wire), fixture.public) {
					t.Error("native key component leaked a credential or lost public data")
				}
			}
			response, err := client.Get(ctx, &pb.GetRequest{Prefix: readPrefix, Path: []*pb.Path{fixture.path}})
			if err != nil {
				t.Fatal(err)
			}
			assertValue(response)
			stream, err := client.Subscribe(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := stream.Send(&pb.SubscribeRequest{Request: &pb.SubscribeRequest_Subscribe{
				Subscribe: &pb.SubscriptionList{Prefix: readPrefix, Subscription: []*pb.Subscription{{Path: fixture.path}}},
			}}); err != nil {
				t.Fatal(err)
			}
			notification, err := stream.Recv()
			if err != nil {
				t.Fatal(err)
			}
			assertValue(notification)
			if _, err := stream.Recv(); err != io.EOF {
				t.Fatal("expected end of synthetic stream")
			}
		}
	}
}

func assertRedactedWire(t *testing.T, msg proto.Message) {
	t.Helper()
	wire, err := proto.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(wire), syntheticSecret) {
		t.Fatal("serialized response contains the synthetic credential")
	}
	if !strings.Contains(string(wire), `"priority":"7"`) {
		t.Fatal("serialized response lost non-secret configuration")
	}
}

func testPath(names ...string) *pb.Path {
	path := &pb.Path{}
	for _, name := range names {
		path.Elem = append(path.Elem, &pb.PathElem{Name: name})
	}
	return path
}

func redactTestGet(response *pb.GetResponse) (*pb.GetResponse, error) {
	result, err := (&secretRedactor{}).UnaryInterceptor()(context.Background(), &pb.GetRequest{},
		&grpc.UnaryServerInfo{FullMethod: gnmiGetMethod},
		func(context.Context, interface{}) (interface{}, error) { return response, nil })
	if err != nil {
		return nil, err
	}
	return result.(*pb.GetResponse), nil
}

func TestSecretRedactionInventory(t *testing.T) {
	for _, entry := range []struct{ table, field string }{
		{"TACPLUS", "passkey"}, {"TACPLUS_SERVER", "passkey"},
		{"RADIUS", "passkey"}, {"RADIUS_SERVER", "passkey"}, {"LDAP", "bind_password"},
		{"BGP_NEIGHBOR", "auth_password"}, {"BGP_PEER_GROUP", "auth_password"},
		{"SNMP_TRAP_CONFIG", "Community"}, {"SNMP_USER", "SNMP_USER_AUTH_PASSWORD"},
		{"SNMP_USER", "SNMP_USER_ENCRYPTION_PASSWORD"},
		{"MACSEC_PROFILE", "primary_cak"}, {"MACSEC_PROFILE", "fallback_cak"},
		{"MACSEC_EGRESS_SA_TABLE", "sak"}, {"MACSEC_INGRESS_SA_TABLE", "auth_key"},
		{"NTP_KEY", "value"}, {"ntp-keys", "key-value"},
		{"config", "secret-key"}, {"state", "secret-key-hashed"},
		{"config", "admin-password"}, {"state", "admin-password-hashed"},
		{"config", "password"}, {"state", "password-hashed"},
		{"SAI_OBJECT_TYPE_MACSEC_SA", "SAI_MACSEC_SA_ATTR_SAK"},
		{"SAI_OBJECT_TYPE_MACSEC_SA", "SAI_MACSEC_SA_ATTR_AUTH_KEY"},
		{"vendor", "future-password"},
	} {
		t.Run(entry.table+"/"+entry.field, func(t *testing.T) {
			data, _ := json.Marshal(map[string]interface{}{
				entry.table: map[string]interface{}{
					"fixture": map[string]interface{}{entry.field: syntheticSecret, "priority": "7"},
				},
			})
			for _, ietf := range []bool{false, true} {
				val := &pb.TypedValue{Value: &pb.TypedValue_JsonVal{JsonVal: data}}
				if ietf {
					val.Value = &pb.TypedValue_JsonIetfVal{JsonIetfVal: data}
				}
				input := &pb.GetResponse{Notification: []*pb.Notification{{Update: []*pb.Update{{Val: val}}}}}
				out, err := redactTestGet(input)
				if err != nil {
					t.Fatal(err)
				}
				assertRedactedWire(t, out)
				before, _ := proto.Marshal(input)
				if !strings.Contains(string(before), syntheticSecret) {
					t.Fatal("redaction modified backend-owned data")
				}
			}
		})
	}
}

func TestSecretRedactionPathsAndScalars(t *testing.T) {
	for _, path := range []*pb.Path{
		testPath("CONFIG_DB", "localhost", "TACPLUS_SERVER", "192.0.2.1", "passkey"),
		testPath("TACPLUS_SERVER", "192.0.2.1", "passkey"),
		{Element: []string{"RADIUS_SERVER", "192.0.2.1", "passkey"}},
		testPath("system", "aaa", "server-groups", "server-group", "servers", "server", "tacacs", "state", "secret-key-hashed"),
		testPath("CONFIG_DB", "localhost", "NTP_KEY", "1", "value", "0"),
		testPath("CONFIG_DB", "localhost", "SNMP_COMMUNITY", syntheticSecret, "name"),
		{Elem: []*pb.PathElem{{Name: "sonic-snmp:sonic-snmp"}, {Name: "SNMP_COMMUNITY"},
			{Name: "SNMP_COMMUNITY_LIST", Key: map[string]string{"name": syntheticSecret}}, {Name: "name"}}},
	} {
		input := &pb.GetResponse{Notification: []*pb.Notification{{
			Update: []*pb.Update{{Path: path, Val: &pb.TypedValue{Value: &pb.TypedValue_StringVal{StringVal: syntheticSecret}}}},
			Delete: []*pb.Path{proto.Clone(path).(*pb.Path)},
		}}}
		out, err := redactTestGet(input)
		if err != nil {
			t.Fatal(err)
		}
		wire, _ := proto.Marshal(out)
		if strings.Contains(string(wire), syntheticSecret) {
			t.Fatal("secret in scalar or response path")
		}
	}
}

func TestSecretRedactionPreservesNonSecrets(t *testing.T) {
	data := []byte(`{"BGP_NEIGHBOR":{"192.0.2.1":{"community":"65000:7"}},"password-policy":{"min-length":12},` +
		`"password-version":"5","server_key":"/etc/sonic/key.pem","ssh-key":"synthetic-public-key",` +
		`"primary_ckn":"public-key-name","secret-key-type":"CLEAR","counter":18446744073709551615}`)
	in := &pb.GetResponse{Notification: []*pb.Notification{{Update: []*pb.Update{{
		Val: &pb.TypedValue{Value: &pb.TypedValue_JsonIetfVal{JsonIetfVal: data}},
	}}}}}
	out, err := redactTestGet(in)
	if err != nil {
		t.Fatal(err)
	}
	var before, after interface{}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	_ = decoder.Decode(&before)
	decoder = json.NewDecoder(bytes.NewReader(out.Notification[0].Update[0].Val.GetJsonIetfVal()))
	decoder.UseNumber()
	_ = decoder.Decode(&after)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("non-secret fields changed")
	}
	for _, path := range []*pb.Path{
		testPath("TACPLUS_SERVER", "192.0.2.1", "priority"),
		testPath("CONFIG_DB", "localhost", "TACPLUS_SERVER", "192.0.2.1", "priority"),
		testPath("system", "aaa", "config", "timestamp"),
	} {
		for _, val := range []*pb.TypedValue{
			{Value: &pb.TypedValue_StringVal{StringVal: "7"}},
			{Value: &pb.TypedValue_AsciiVal{AsciiVal: "7"}},
			{Value: &pb.TypedValue_UintVal{UintVal: 7}},
			{Value: &pb.TypedValue_BytesVal{BytesVal: []byte("7")}},
		} {
			in.Notification[0].Update[0].Path = path
			in.Notification[0].Update[0].Val = val
			out, err := redactTestGet(in)
			if err != nil || !proto.Equal(out.Notification[0].Update[0].Val, val) {
				t.Fatal("non-secret scalar field changed or rejected")
			}
		}
	}
	in.Notification[0].Update[0].Path = &pb.Path{Elem: []*pb.PathElem{
		{Name: "system"}, {Name: "aaa"}, {Name: "server-group", Key: map[string]string{"key": "future-password"}},
	}}
	value := &pb.TypedValue{Value: &pb.TypedValue_JsonIetfVal{JsonIetfVal: []byte(`{"priority":"7"}`)}}
	in.Notification[0].Update[0].Val = value
	out, err = redactTestGet(in)
	if err != nil || !proto.Equal(out.Notification[0].Update[0].Val, value) {
		t.Fatal("translated list key was interpreted as a native schema component")
	}
}

func TestSecretRedactionFailClosed(t *testing.T) {
	for _, val := range []*pb.TypedValue{
		{Value: &pb.TypedValue_JsonVal{JsonVal: []byte(`{"passkey":"synthetic-only-credential"`)}},
		{Value: &pb.TypedValue_JsonIetfVal{JsonIetfVal: []byte(`{} {"passkey":"synthetic-only-credential"}`)}},
		{Value: &pb.TypedValue_ProtoBytes{ProtoBytes: []byte(syntheticSecret)}},
		{Value: &pb.TypedValue_BytesVal{BytesVal: []byte(syntheticSecret)}},
	} {
		response := &pb.GetResponse{Notification: []*pb.Notification{{
			Prefix: testPath("CONFIG_DB", "localhost"), Update: []*pb.Update{{Val: val}},
		}}}
		out, err := redactTestGet(response)
		if out != nil || status.Code(err) != codes.DataLoss || strings.Contains(err.Error(), syntheticSecret) {
			t.Fatal("unsafe representation did not fail closed")
		}
	}
}

func TestSecretRedactionUnsafeNotifications(t *testing.T) {
	mixed := testPath("password")
	mixed.Element = []string{"PORT"}
	for _, notification := range []*pb.Notification{
		{Prefix: mixed},
		{Update: []*pb.Update{{Path: mixed}}},
		{Delete: []*pb.Path{mixed}},
		{Update: []*pb.Update{{Path: testPath("password"),
			Val: &pb.TypedValue{Value: &pb.TypedValue_UintVal{UintVal: 1}}}}},
		{Update: []*pb.Update{{Path: testPath("password"),
			Val: &pb.TypedValue{Value: &pb.TypedValue_LeaflistVal{LeaflistVal: &pb.ScalarArray{
				Element: []*pb.TypedValue{{Value: &pb.TypedValue_UintVal{UintVal: 1}}},
			}}}}}},
		{Update: []*pb.Update{{Path: testPath("TACPLUS_SERVER"),
			Val: &pb.TypedValue{Value: &pb.TypedValue_StringVal{StringVal: syntheticSecret}}}}},
		{Update: []*pb.Update{{Path: testPath("CONFIG_DB"),
			Val: &pb.TypedValue{Value: &pb.TypedValue_AsciiVal{AsciiVal: syntheticSecret}}}}},
		{Update: []*pb.Update{{Path: testPath("CONFIG_DB"),
			Val: &pb.TypedValue{Value: &pb.TypedValue_JsonVal{JsonVal: []byte(`"synthetic-only-credential"`)}}}}},
		{Update: []*pb.Update{{Path: testPath("password"),
			Value: &pb.Value{Type: pb.Encoding_JSON, Value: []byte(`{"password":`)}}}},
		{Update: []*pb.Update{{Path: testPath("password"),
			Value: &pb.Value{Type: pb.Encoding_BYTES, Value: []byte(syntheticSecret)}}}},
	} {
		out, err := redactTestGet(&pb.GetResponse{Notification: []*pb.Notification{notification}})
		if out != nil || status.Code(err) != codes.DataLoss || strings.Contains(err.Error(), syntheticSecret) {
			t.Fatal("unsafe notification did not fail closed")
		}
	}
}
