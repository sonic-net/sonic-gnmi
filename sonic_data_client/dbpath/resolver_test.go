package dbpath

import (
	"testing"

	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestResolve(t *testing.T) {
	tests := []struct {
		name         string
		prefix       *gnmipb.Path
		paths        []*gnmipb.Path
		wantDatabase string
		wantInstance string
		wantCode     codes.Code
	}{
		{
			name: "database and instance from path",
			paths: []*gnmipb.Path{{Elem: []*gnmipb.PathElem{
				{Name: "CONFIG_DB"},
				{Name: "localhost"},
				{Name: "VNET"},
			}}},
			wantDatabase: "CONFIG_DB",
			wantInstance: "localhost",
		},
		{
			name:   "prefix elements participate",
			prefix: &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "STATE_DB"}}},
			paths: []*gnmipb.Path{{Elem: []*gnmipb.PathElem{
				{Name: "localhost"},
				{Name: "PORT_TABLE"},
			}}},
			wantDatabase: "STATE_DB",
			wantInstance: "localhost",
		},
		{
			name: "database conflict",
			paths: []*gnmipb.Path{
				{Elem: []*gnmipb.PathElem{{Name: "CONFIG_DB"}, {Name: "localhost"}}},
				{Elem: []*gnmipb.PathElem{{Name: "APPL_DB"}, {Name: "localhost"}}},
			},
			wantCode: codes.Unimplemented,
		},
		{
			name: "instance conflict",
			paths: []*gnmipb.Path{
				{Elem: []*gnmipb.PathElem{{Name: "CONFIG_DB"}, {Name: "localhost"}}},
				{Elem: []*gnmipb.PathElem{{Name: "CONFIG_DB"}, {Name: "asic0"}}},
			},
			wantCode: codes.Unimplemented,
		},
		{
			name:     "short path",
			paths:    []*gnmipb.Path{{Elem: []*gnmipb.PathElem{{Name: "CONFIG_DB"}}}},
			wantCode: codes.Unimplemented,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Resolve(test.prefix, test.paths)
			if code := status.Code(err); code != test.wantCode {
				t.Fatalf("status = %v, want %v; err=%v", code, test.wantCode, err)
			}
			if got.Database != test.wantDatabase || got.Instance != test.wantInstance {
				t.Fatalf("resolution = %+v, want database=%q instance=%q",
					got, test.wantDatabase, test.wantInstance)
			}
		})
	}
}

func TestParseTarget(t *testing.T) {
	tests := []struct {
		name          string
		target        string
		wantDatabase  string
		wantQualifier string
		wantKnown     bool
		wantMalformed bool
	}{
		{
			name:         "database",
			target:       "CONFIG_DB",
			wantDatabase: "CONFIG_DB",
			wantKnown:    true,
		},
		{
			name:          "database with qualifier",
			target:        "CONFIG_DB/asic0",
			wantDatabase:  "CONFIG_DB",
			wantQualifier: "asic0",
			wantKnown:     true,
		},
		{
			name:          "malformed database target",
			target:        "CONFIG_DB/asic0/extra",
			wantDatabase:  "CONFIG_DB",
			wantKnown:     true,
			wantMalformed: true,
		},
		{
			name:          "generic target",
			target:        "YANG/device/one",
			wantDatabase:  "YANG",
			wantMalformed: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database, qualifier, known, malformed := ParseTarget(test.target)
			if database != test.wantDatabase || qualifier != test.wantQualifier ||
				known != test.wantKnown || malformed != test.wantMalformed {
				t.Fatalf(
					"ParseTarget() = (%q, %q, %t, %t), want (%q, %q, %t, %t)",
					database, qualifier, known, malformed,
					test.wantDatabase, test.wantQualifier, test.wantKnown, test.wantMalformed,
				)
			}
		})
	}
}
