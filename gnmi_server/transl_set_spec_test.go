package gnmi

import (
	"context"
	"crypto/tls"
	"testing"
	"time"

	pb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/openconfig/ygot/ygot"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
)

// TestGnmiSetSpecCompliance checks that translib Set takes the value of the
// node at the update path, as defined by the gNMI specification.
func TestGnmiSetSpecCompliance(t *testing.T) {
	if !ENABLE_TRANSLIB_WRITE {
		t.Skip("skipping test in read-only mode.")
	}
	s := createServer(t, 8091)
	go runServer(t, s)
	defer s.Stop()

	prepareDbTranslib(t)

	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}
	conn, err := grpc.Dial("127.0.0.1:8091", opts...)
	if err != nil {
		t.Fatalf("Dialing to 127.0.0.1:8091 failed: %v", err)
	}
	defer conn.Close()
	gClient := pb.NewGNMIClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	const aclSets = "/openconfig-acl:acl/acl-sets"
	const acl = aclSets + "/acl-set[name=S001][type=ACL_IPV4]"
	prefix := &pb.Path{Target: "OC_YANG"}

	set := func(t *testing.T, want codes.Code, replace, update []*pb.Update) {
		t.Helper()
		runTestSetRaw(t, ctx, gClient, &pb.SetRequest{Prefix: prefix, Replace: replace, Update: update}, want)
	}
	upd := func(path string, val *pb.TypedValue) []*pb.Update {
		p, err := ygot.StringToStructuredPath(path)
		if err != nil {
			t.Fatalf("bad path %q: %v", path, err)
		}
		return []*pb.Update{{Path: p, Val: val}}
	}
	ietf := func(s string) *pb.TypedValue {
		return &pb.TypedValue{Value: &pb.TypedValue_JsonIetfVal{JsonIetfVal: []byte(s)}}
	}
	jsonv := func(s string) *pb.TypedValue {
		return &pb.TypedValue{Value: &pb.TypedValue_JsonVal{JsonVal: []byte(s)}}
	}
	verifyDesc := func(t *testing.T, want string) {
		t.Helper()
		runTestGet(t, ctx, gClient, "OC_YANG", pathToPb(acl+"/config/description"), codes.OK,
			`{"openconfig-acl:description": "`+want+`"}`, true)
	}
	cleanup := func() {
		p, _ := ygot.StringToStructuredPath(aclSets)
		gClient.Set(ctx, &pb.SetRequest{Prefix: prefix, Delete: []*pb.Path{p}})
	}
	cleanup()
	defer cleanup()

	t.Run("update list entry without keys in the value", func(t *testing.T) {
		set(t, codes.OK, nil, upd(acl, ietf(`{"config": {"name": "S001", "type": "ACL_IPV4", "description": "entry"}}`)))
		verifyDesc(t, "entry")
	})
	t.Run("update container with its content", func(t *testing.T) {
		set(t, codes.OK, nil, upd(acl+"/config", ietf(`{"description": "container"}`)))
		verifyDesc(t, "container")
	})
	t.Run("update leaf with JSON scalar", func(t *testing.T) {
		set(t, codes.OK, nil, upd(acl+"/config/description", ietf(`"json"`)))
		verifyDesc(t, "json")
	})
	t.Run("replace leaf with string_val", func(t *testing.T) {
		set(t, codes.OK, upd(acl+"/config/description", &pb.TypedValue{Value: &pb.TypedValue_StringVal{StringVal: "typed"}}), nil)
		verifyDesc(t, "typed")
	})
	t.Run("update leaf with uint_val", func(t *testing.T) {
		set(t, codes.OK, nil, upd("/openconfig-interfaces:interfaces/interface[name=Ethernet0]/config/mtu",
			&pb.TypedValue{Value: &pb.TypedValue_UintVal{UintVal: 9100}}))
	})
	t.Run("update whole list with array", func(t *testing.T) {
		set(t, codes.OK, nil, upd(aclSets+"/acl-set",
			ietf(`[{"name": "S002", "type": "ACL_IPV4", "config": {"name": "S002", "type": "ACL_IPV4"}}]`)))
		runTestGet(t, ctx, gClient, "OC_YANG", pathToPb(aclSets+"/acl-set[name=S002][type=ACL_IPV4]/config/name"),
			codes.OK, `{"openconfig-acl:name": "S002"}`, true)
	})

	t.Run("update container with json_val", func(t *testing.T) {
		set(t, codes.OK, nil, upd(acl+"/config", jsonv(`{"description": "jsonval container"}`)))
		verifyDesc(t, "jsonval container")
	})
	t.Run("replace leaf with json_val", func(t *testing.T) {
		set(t, codes.OK, upd(acl+"/config/description", jsonv(`"jsonval leaf"`)), nil)
		verifyDesc(t, "jsonval leaf")
	})
	t.Run("update list entry with json_val", func(t *testing.T) {
		set(t, codes.OK, nil, upd(acl, jsonv(`{"config": {"name": "S001", "type": "ACL_IPV4", "description": "jsonval entry"}}`)))
		verifyDesc(t, "jsonval entry")
	})
	t.Run("update leaf with json_val number", func(t *testing.T) {
		set(t, codes.OK, nil, upd("/openconfig-interfaces:interfaces/interface[name=Ethernet0]/config/mtu", jsonv(`9100`)))
	})
	t.Run("bulk update with json_val", func(t *testing.T) {
		update := append(
			upd(aclSets+"/acl-set[name=S004][type=ACL_IPV4]", jsonv(`{"config": {"name": "S004", "type": "ACL_IPV4"}}`)),
			upd(acl+"/config/description", jsonv(`"jsonval bulk"`))...)
		set(t, codes.OK, nil, update)
		verifyDesc(t, "jsonval bulk")
		runTestGet(t, ctx, gClient, "OC_YANG", pathToPb(aclSets+"/acl-set[name=S004][type=ACL_IPV4]/config/name"),
			codes.OK, `{"openconfig-acl:name": "S004"}`, true)
	})

	t.Run("list key in value differs from path", func(t *testing.T) {
		set(t, codes.InvalidArgument, nil, upd(acl, ietf(`{"name": "OTHER"}`)))
	})
	t.Run("replace list entry with empty value", func(t *testing.T) {
		set(t, codes.InvalidArgument, upd(acl, ietf(`{}`)), nil)
	})
	t.Run("array value for list entry", func(t *testing.T) {
		set(t, codes.InvalidArgument, nil, upd(acl, ietf(`[{"name": "S001", "type": "ACL_IPV4"}]`)))
	})
	t.Run("scalar value for container", func(t *testing.T) {
		set(t, codes.InvalidArgument, nil, upd(acl+"/config", &pb.TypedValue{Value: &pb.TypedValue_UintVal{UintVal: 1}}))
	})
	t.Run("invalid update fails the whole request", func(t *testing.T) {
		update := append(
			upd(aclSets+"/acl-set[name=S003][type=ACL_IPV4]", ietf(`{"config": {"name": "S003", "type": "ACL_IPV4"}}`)),
			upd(acl, ietf(`{"name": "OTHER"}`))...)
		set(t, codes.InvalidArgument, nil, update)
		runTestGet(t, ctx, gClient, "OC_YANG", pathToPb(aclSets+"/acl-set[name=S003][type=ACL_IPV4]"),
			codes.NotFound, nil, false)
	})
}
