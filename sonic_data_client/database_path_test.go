package client

import (
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	pb "github.com/openconfig/gnmi/proto/gnmi"
	dbconfig "github.com/sonic-net/sonic-gnmi/sonic_db_config"
	"github.com/sonic-net/sonic-gnmi/swsscommon"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func nativePath(names ...string) *pb.Path {
	elems := make([]*pb.PathElem, 0, len(names))
	for _, name := range names {
		elems = append(elems, &pb.PathElem{Name: name})
	}
	return &pb.Path{Elem: elems}
}

func TestDatabasePathCompatibility(t *testing.T) {
	tests := []struct {
		name              string
		prefix            *pb.Path
		paths             []*pb.Path
		code              codes.Code
		message, database string
	}{
		{name: "empty list", code: codes.Unimplemented, message: "No valid path"},
		{name: "nil path", paths: []*pb.Path{nil}, code: codes.Unimplemented, message: "No target specified in path"},
		{name: "prefix forwarded", prefix: nativePath("TEST_DB"), paths: []*pb.Path{nativePath("localhost", "PORT")}, database: "TEST_DB"},
		{name: "skip mixed nil", paths: []*pb.Path{nil, nativePath("TEST_DB", "localhost")}, database: "TEST_DB"},
		{name: "empty database sentinel", paths: []*pb.Path{nativePath("", "localhost"), nativePath("TEST_DB", "localhost")}, database: "TEST_DB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := MixedDbClient{namespace_cnt: 1, container_cnt: 1}
			database, key, err := c.ParseDatabase(tt.prefix, tt.paths)
			if key != nil {
				defer swsscommon.DeleteSonicDBKey(key)
			}
			if status.Code(err) != tt.code || status.Convert(err).Message() != tt.message || database != tt.database {
				t.Fatalf("database=%q err=%v, want %q %v %q", database, err, tt.database, tt.code, tt.message)
			}
		})
	}
}

func TestDatabasePathLookupOrder(t *testing.T) {
	var calls [][2]string
	patch := gomonkey.ApplyFunc(dbconfig.GetDbInstanceFromTarget, func(ns, container string) (swsscommon.SonicDBKey, bool) {
		calls = append(calls, [2]string{ns, container})
		if ns == "asic0" || container == "dpu0" {
			return swsscommon.NewSonicDBKey(), true
		}
		return nil, false
	})
	defer patch.Reset()
	c := MixedDbClient{namespace_cnt: 2, container_cnt: 2}
	_, _, err := c.ParseDatabase(nil, []*pb.Path{nativePath("CONFIG_DB", "bad"), nativePath("STATE_DB", "localhost")})
	if err == nil || err.Error() != "Unsupported namespace/container bad" || status.Code(err) != codes.Unknown {
		t.Fatalf("failure precedence changed: %v", err)
	}
	if !reflect.DeepEqual(calls, [][2]string{{"bad", ""}, {"", "bad"}}) {
		t.Fatalf("lookup order=%v", calls)
	}
	for _, instance := range []string{"asic0", "dpu0"} {
		calls = nil
		_, key, err := c.ParseDatabase(nil, []*pb.Path{nativePath("CONFIG_DB", instance), nativePath("CONFIG_DB", instance)})
		if err != nil {
			t.Fatal(err)
		}
		defer swsscommon.DeleteSonicDBKey(key)
		want := [][2]string{{"asic0", ""}, {"asic0", ""}}
		if instance == "dpu0" {
			want = [][2]string{{"dpu0", ""}, {"", "dpu0"}, {"dpu0", ""}, {"", "dpu0"}}
			if key.GetContainerName() != "dpu0" {
				t.Fatal("wrong container")
			}
		}
		if instance == "asic0" && key.GetNetns() != "asic0" {
			t.Fatal("wrong namespace")
		}
		if !reflect.DeepEqual(calls, want) {
			t.Fatalf("%s lookups=%v", instance, calls)
		}
	}
}

func TestDatabasePathInstanceSelection(t *testing.T) {
	for _, counts := range [][2]int{{1, 1}, {2, 1}, {1, 2}} {
		for _, instance := range []string{"localhost", "asic0", "dpu0", ""} {
			c := MixedDbClient{namespace_cnt: counts[0], container_cnt: counts[1]}
			_, key, err := c.ParseDatabase(nil, []*pb.Path{nativePath("TEST_DB", instance)})
			if err != nil {
				t.Fatal(err)
			}
			wantNamespace, wantContainer := "", ""
			if instance != "localhost" {
				if counts[0] > 1 {
					wantNamespace = instance
				}
				if counts[1] > 1 {
					wantContainer = instance
				}
			}
			if key.GetNetns() != wantNamespace || key.GetContainerName() != wantContainer {
				t.Errorf("counts=%v instance=%q namespace=%q container=%q", counts, instance, key.GetNetns(), key.GetContainerName())
			}
			swsscommon.DeleteSonicDBKey(key)
		}
	}
	// An empty first instance is a sentinel, not a conflict with the next path.
	c := MixedDbClient{namespace_cnt: 2, container_cnt: 1}
	_, key, err := c.ParseDatabase(nil, []*pb.Path{nativePath("TEST_DB", ""), nativePath("TEST_DB", "asic0")})
	if err != nil {
		t.Fatal(err)
	}
	defer swsscommon.DeleteSonicDBKey(key)
	if key.GetNetns() != "asic0" {
		t.Fatalf("empty-instance transition selected %q", key.GetNetns())
	}
}

func TestResolvedDatabaseBindingPreservesLookupOrder(t *testing.T) {
	var calls [][2]string
	patch := gomonkey.ApplyFunc(dbconfig.GetDbInstanceFromTarget, func(ns, container string) (swsscommon.SonicDBKey, bool) {
		calls = append(calls, [2]string{ns, container})
		if ns == "asic0" || container == "dpu0" {
			return swsscommon.NewSonicDBKey(), true
		}
		return nil, false
	})
	defer patch.Reset()
	c := MixedDbClient{namespace_cnt: 2, container_cnt: 2}
	key, err := c.materializeDatabaseInstances([]string{"dpu0", "dpu0"})
	if err != nil {
		t.Fatal(err)
	}
	defer swsscommon.DeleteSonicDBKey(key)
	if key.GetNetns() != "" || key.GetContainerName() != "dpu0" {
		t.Fatalf("binding namespace=%q container=%q", key.GetNetns(), key.GetContainerName())
	}
	if !reflect.DeepEqual(calls, [][2]string{{"dpu0", ""}, {"", "dpu0"}, {"dpu0", ""}, {"", "dpu0"}}) {
		t.Fatalf("lookup order=%v", calls)
	}
}

func TestResolvedDatabaseConstructorUsesBinding(t *testing.T) {
	// Changes in discovery after materialization cannot select a different
	// namespace/container. No read/write is issued by this constructor test.
	patch := gomonkey.ApplyFunc(dbconfig.GetDbAllInstances, func() ([]swsscommon.SonicDBKey, error) {
		key := swsscommon.NewSonicDBKey()
		key.SetNetns("different")
		return []swsscommon.SonicDBKey{key}, nil
	})
	defer patch.Reset()
	patch.ApplyFunc(IsTargetDbByDBKey, func(_ string, key swsscommon.SonicDBKey) bool {
		if key.GetNetns() != "bound" || key.GetContainerName() != "" {
			t.Fatalf("constructor changed binding: %s/%s", key.GetNetns(), key.GetContainerName())
		}
		return true
	})
	paths := []*pb.Path{nativePath("STATE_DB", "different", "TABLE")}
	client, err := NewResolvedMixedDbClient(paths, &pb.Path{Origin: "sonic-db"}, "sonic-db", pb.Encoding_JSON_IETF, "", "",
		NativeDatabaseBinding{Database: "STATE_DB", Namespace: "bound"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	actual := client.(*MixedDbClient)
	if actual.target != "STATE_DB" || actual.dbkey.GetNetns() != "bound" || actual.mapkey != "bound:" {
		t.Fatalf("constructor did not retain binding: %s %s", actual.target, actual.mapkey)
	}
}

func TestTargetDatabaseCompatibility(t *testing.T) {
	tests := []struct {
		target, database, qualifier string
		known, qualified            bool
	}{
		{"CONFIG_DB/asic0", "CONFIG_DB", "asic0", true, true},
		{"CONFIG_DB/", "CONFIG_DB", "", true, true},
		{"CONFIG_DB/asic0/extra", "CONFIG_DB", "", false, false},
		{"UNKNOWN/asic0", "UNKNOWN", "asic0", false, true},
	}
	for _, multi := range []bool{false, true} {
		patch := gomonkey.ApplyFunc(dbconfig.CheckDbMultiNamespace, func() (bool, error) { return multi, nil })
		defer patch.Reset()
		for _, tt := range tests {
			database, known, ns, qualified := IsTargetDb(tt.target)
			wantNs := ""
			if multi {
				wantNs = tt.qualifier
			}
			if database != tt.database || known != tt.known || ns != wantNs || qualified != (multi && tt.qualified) {
				t.Fatalf("multi=%v target=%q got=(%s,%v,%s,%v)", multi, tt.target, database, known, ns, qualified)
			}
		}
		patch.Reset()
	}
}
