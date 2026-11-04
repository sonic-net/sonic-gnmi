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
		paths             []*pb.Path
		code              codes.Code
		message, database string
	}{
		{name: "empty list", code: codes.Unimplemented, message: "No valid path"},
		{name: "nil path", paths: []*pb.Path{nil}, code: codes.Unimplemented, message: "No target specified in path"},
		{name: "deprecated representation skipped", paths: []*pb.Path{{Element: []string{"CONFIG_DB", "localhost"}}}, code: codes.Unimplemented, message: "No target specified in path"},
		{name: "empty elements", paths: []*pb.Path{nativePath()}, code: codes.Unimplemented, message: "Invalid elem length"},
		{name: "short path", paths: []*pb.Path{nativePath("CONFIG_DB")}, code: codes.Unimplemented, message: "Invalid elem length"},
		{name: "target conflict", paths: []*pb.Path{nativePath("CONFIG_DB", "localhost"), nativePath("STATE_DB", "localhost")}, code: codes.Unimplemented, message: "Target conflict in path"},
		{name: "instance conflict", paths: []*pb.Path{nativePath("CONFIG_DB", "asic0"), nativePath("CONFIG_DB", "asic1")}, code: codes.Unimplemented, message: "Namespace/container conflict in path"},
		{name: "skip mixed nil", paths: []*pb.Path{nil, nativePath("TEST_DB", "localhost")}, database: "TEST_DB"},
		{name: "empty database sentinel", paths: []*pb.Path{nativePath("", "localhost"), nativePath("TEST_DB", "localhost")}, database: "TEST_DB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := MixedDbClient{namespace_cnt: 1, container_cnt: 1}
			database, key, err := c.ParseDatabase(nil, tt.paths)
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

func TestDatabasePathPrefixAliasing(t *testing.T) {
	backing := []*pb.PathElem{{Name: "CONFIG_DB"}, {Name: "sentinel"}, {Name: "unused"}}
	prefix := &pb.Path{Target: "IGNORED", Elem: backing[:1]}
	path := nativePath("localhost", "PORT")
	c := MixedDbClient{namespace_cnt: 1, container_cnt: 1}
	database, key, err := c.ParseDatabase(prefix, []*pb.Path{path})
	if key != nil {
		defer swsscommon.DeleteSonicDBKey(key)
	}
	if err != nil || database != "CONFIG_DB" {
		t.Fatalf("database=%s err=%v", database, err)
	}
	if len(prefix.Elem) != 1 || prefix.Elem[0].Name != "CONFIG_DB" || backing[1] != path.Elem[0] || backing[2] != path.Elem[1] {
		t.Fatal("legacy prefix backing-array behavior changed")
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

func TestTargetDatabaseCompatibility(t *testing.T) {
	for _, multi := range []bool{false, true} {
		patch := gomonkey.ApplyFunc(dbconfig.CheckDbMultiNamespace, func() (bool, error) { return multi, nil })
		for _, target := range []string{"CONFIG_DB", "CONFIG_DB/asic0", "CONFIG_DB/", "CONFIG_DB/asic0/extra", "UNKNOWN/asic0", ""} {
			database, known, ns, qualified := IsTargetDb(target)
			wantDb, wantKnown, wantNs, wantQualified := target, false, "", false
			switch target {
			case "CONFIG_DB":
				wantKnown = true
			case "CONFIG_DB/asic0":
				wantDb, wantKnown = "CONFIG_DB", true
				if multi {
					wantNs, wantQualified = "asic0", true
				}
			case "CONFIG_DB/":
				wantDb, wantKnown = "CONFIG_DB", true
				wantQualified = multi
			case "CONFIG_DB/asic0/extra":
				wantDb = "CONFIG_DB"
			case "UNKNOWN/asic0":
				wantDb = "UNKNOWN"
				if multi {
					wantNs, wantQualified = "asic0", true
				}
			}
			if database != wantDb || known != wantKnown || ns != wantNs || qualified != wantQualified {
				t.Fatalf("multi=%v target=%q got=(%s,%v,%s,%v)", multi, target, database, known, ns, qualified)
			}
		}
		patch.Reset()
	}
}
