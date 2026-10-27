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
}

func (*secretFixtureServer) Get(_ context.Context, req *pb.GetRequest) (*pb.GetResponse, error) {
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
	pb.RegisterGNMIServer(server, &secretFixtureServer{})
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
		for _, pattern := range []string{"NTP_KE?", "NTP_KE[Z-A]", "NTP_K[E-]x]Y", "SNMP_COMMUNITY|prefix", "SNMP_COMMUNITY|*"} {
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
				t.Fatal("ambiguous table glob reached streaming retrieval")
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
	for _, val := range []*pb.TypedValue{
		{Value: &pb.TypedValue_StringVal{StringVal: "7"}},
		{Value: &pb.TypedValue_UintVal{UintVal: 7}},
	} {
		in.Notification[0].Update[0].Path = testPath("TACPLUS_SERVER", "192.0.2.1", "priority")
		in.Notification[0].Update[0].Val = val
		if _, err := redactTestGet(in); err != nil {
			t.Fatal("non-secret scalar field rejected")
		}
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
