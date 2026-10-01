package client

import (
	"strings"
	"testing"
	"time"

	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
)

func recordsPath(elems ...interface{}) *gnmipb.Path {
	p := &gnmipb.Path{}
	for _, e := range elems {
		switch v := e.(type) {
		case string:
			p.Elem = append(p.Elem, &gnmipb.PathElem{Name: v})
		case *gnmipb.PathElem:
			p.Elem = append(p.Elem, v)
		default:
			panic("bad elem")
		}
	}
	return p
}

func withRecordsNamespaces(t *testing.T, ns []string) {
	t.Helper()
	prev := recordsGetNamespaces
	recordsGetNamespaces = func() ([]string, error) { return ns, nil }
	t.Cleanup(func() { recordsGetNamespaces = prev })
}

func TestParseRecordsPathAPPLKeyRejoin(t *testing.T) {
	sub, err := parseRecordsPath(recordsPath(
		"RECORDS", "localhost", "APPL_DB", "ROUTE_TABLE", "10.1.0.0", "24",
	))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if sub.namespace != "localhost" || sub.db != "APPL_DB" || sub.table != "ROUTE_TABLE" {
		t.Fatalf("got ns/db/table = %s/%s/%s", sub.namespace, sub.db, sub.table)
	}
	if sub.key != "10.1.0.0/24" {
		t.Fatalf("key = %q, want 10.1.0.0/24", sub.key)
	}
	if !sub.from.IsZero() || len(sub.ops) != 0 {
		t.Fatalf("expected live-only/all-ops, from=%v ops=%v", sub.from, sub.ops)
	}
}

func TestParseRecordsPathWithoutRECORDSElem(t *testing.T) {
	// Target already in prefix; path elems start at namespace.
	sub, err := parseRecordsPath(recordsPath("localhost", "APPL_DB", "NEIGH_TABLE"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if sub.table != "NEIGH_TABLE" || sub.key != "" {
		t.Fatalf("got table=%q key=%q", sub.table, sub.key)
	}
}

func TestParseRecordsPathAPPLDatabaseWideFrom(t *testing.T) {
	fixed := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	prev := recordsNow
	recordsNow = func() time.Time { return fixed }
	defer func() { recordsNow = prev }()

	sub, err := parseRecordsPath(recordsPath(
		"RECORDS",
		"localhost",
		&gnmipb.PathElem{Name: "APPL_DB", Key: map[string]string{"from": "-30m"}},
	))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if sub.db != "APPL_DB" || sub.table != "" || sub.key != "" {
		t.Fatalf("got db=%q table=%q key=%q", sub.db, sub.table, sub.key)
	}
	if want := fixed.Add(-30 * time.Minute); !sub.from.Equal(want) {
		t.Fatalf("from = %v, want %v", sub.from, want)
	}
}

func TestParseRecordsPathASICWithOps(t *testing.T) {
	sub, err := parseRecordsPath(recordsPath(
		"RECORDS", "localhost", "ASIC_DB", "ASIC_STATE",
		&gnmipb.PathElem{
			Name: "SAI_OBJECT_TYPE_NEXT_HOP_GROUP:oid:0x5000000000a3c",
			Key:  map[string]string{"ops": "E"},
		},
	))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if sub.db != "ASIC_DB" || sub.table != "ASIC_STATE" {
		t.Fatalf("db/table = %s/%s", sub.db, sub.table)
	}
	if sub.key != "SAI_OBJECT_TYPE_NEXT_HOP_GROUP:oid:0x5000000000a3c" {
		t.Fatalf("key = %q", sub.key)
	}
	if len(sub.ops) != 1 || sub.ops[0] != "E" {
		t.Fatalf("ops = %v, want [E]", sub.ops)
	}
}

func TestParseRecordsPathFromRelative(t *testing.T) {
	fixed := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	prev := recordsNow
	recordsNow = func() time.Time { return fixed }
	defer func() { recordsNow = prev }()

	sub, err := parseRecordsPath(recordsPath(
		&gnmipb.PathElem{
			Name: "RECORDS",
			Key:  map[string]string{"from": "-30m"},
		},
		"localhost", "APPL_DB", "ROUTE_TABLE", "10.1.0.0", "24",
	))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := fixed.Add(-30 * time.Minute)
	if !sub.from.Equal(want) {
		t.Fatalf("from = %v, want %v", sub.from, want)
	}
}

func TestParseRecordsPathFromDayAndRFC3339(t *testing.T) {
	fixed := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	prev := recordsNow
	recordsNow = func() time.Time { return fixed }
	defer func() { recordsNow = prev }()

	sub, err := parseRecordsPath(recordsPath(
		&gnmipb.PathElem{Name: "localhost", Key: map[string]string{"from": "-1d"}},
		"APPL_DB", "ROUTE_TABLE",
	))
	if err != nil {
		t.Fatalf("parse -1d: %v", err)
	}
	if !sub.from.Equal(fixed.Add(-24 * time.Hour)) {
		t.Fatalf("from -1d = %v", sub.from)
	}

	sub, err = parseRecordsPath(recordsPath(
		&gnmipb.PathElem{Name: "asic1", Key: map[string]string{"from": "2026-09-26T10:00:00Z"}},
		"ASIC_DB", "ASIC_STATE", "SAI_OBJECT_TYPE_ROUTE_ENTRY",
	))
	if err != nil {
		t.Fatalf("parse RFC3339: %v", err)
	}
	if sub.namespace != "asic1" || sub.key != "SAI_OBJECT_TYPE_ROUTE_ENTRY" {
		t.Fatalf("got ns=%s key=%q", sub.namespace, sub.key)
	}
	if sub.from.UTC().Format(time.RFC3339) != "2026-09-26T10:00:00Z" {
		t.Fatalf("from = %v", sub.from)
	}
}

func TestParseRecordsPathErrors(t *testing.T) {
	cases := []struct {
		name string
		path *gnmipb.Path
		want string
	}{
		{"short", recordsPath("RECORDS", "localhost"), "path must be"},
		{"badDB", recordsPath("localhost", "CONFIG_DB", "ROUTE_TABLE"), "DB must be"},
		{"missingAsicTable", recordsPath("RECORDS", "localhost", "ASIC_DB"), "ASIC_STATE"},
		{"badAsicTable", recordsPath("localhost", "ASIC_DB", "ROUTE_TABLE"), "ASIC_STATE"},
		{"badFrom", recordsPath(&gnmipb.PathElem{Name: "localhost", Key: map[string]string{"from": "yesterday"}}, "APPL_DB", "T"), "invalid from"},
		{"badOps", recordsPath(&gnmipb.PathElem{Name: "localhost", Key: map[string]string{"ops": "SET,"}}, "APPL_DB", "T"), "invalid ops"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseRecordsPath(tc.path)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestValidateRecordsNamespace(t *testing.T) {
	withRecordsNamespaces(t, []string{"", "asic0", "asic1"})

	if err := validateRecordsNamespace("localhost"); err != nil {
		t.Fatalf("localhost: %v", err)
	}
	if err := validateRecordsNamespace("asic0"); err != nil {
		t.Fatalf("asic0: %v", err)
	}
	if err := validateRecordsNamespace("asic9"); err == nil {
		t.Fatal("expected unknown namespace error")
	}
}

func TestNewRecordsClientParsesAndValidates(t *testing.T) {
	withRecordsNamespaces(t, []string{""})

	dc, err := NewRecordsClient([]*gnmipb.Path{recordsTestPath()}, recordsTestPrefix(), 0)
	if err != nil {
		t.Fatalf("NewRecordsClient: %v", err)
	}
	rc := dc.(*RecordsClient)
	if len(rc.subs) != 1 {
		t.Fatalf("subs = %d", len(rc.subs))
	}
	if rc.subs[0].key != "10.1.0.0/24" {
		t.Fatalf("key = %q", rc.subs[0].key)
	}

	_, err = NewRecordsClient([]*gnmipb.Path{
		recordsPath("RECORDS", "asic9", "APPL_DB", "ROUTE_TABLE"),
	}, recordsTestPrefix(), 0)
	if err == nil || !strings.Contains(err.Error(), "unknown namespace") {
		t.Fatalf("err = %v, want unknown namespace", err)
	}
}
