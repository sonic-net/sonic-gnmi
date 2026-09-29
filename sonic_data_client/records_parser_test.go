//go:build records

package client

import (
	"testing"
	"time"
)

var testLoc = time.FixedZone("UTC", 0)

func TestParseSwssRouteSet(t *testing.T) {
	p := NewRecordsParser(testLoc)
	line := RawLine{
		Source: "swss",
		Seq:    "swss:1234:5678",
		Line:   "2026-09-26.10:15:32.101234|ROUTE_TABLE:10.1.0.0/24|SET|nexthop:10.0.0.1|ifname:Ethernet0",
	}
	r, ok := p.Parse(line)
	if !ok {
		t.Fatal("expected parse to succeed")
	}
	if r.Source != "swss" {
		t.Errorf("Source = %q, want swss", r.Source)
	}
	if r.DB != "APPL_DB" {
		t.Errorf("DB = %q, want APPL_DB", r.DB)
	}
	if r.Table != "ROUTE_TABLE" {
		t.Errorf("Table = %q, want ROUTE_TABLE", r.Table)
	}
	if r.Key != "10.1.0.0/24" {
		t.Errorf("Key = %q, want 10.1.0.0/24", r.Key)
	}
	if r.Op != "SET" {
		t.Errorf("Op = %q, want SET", r.Op)
	}
	if r.Fields["nexthop"] != "10.0.0.1" {
		t.Errorf("nexthop = %q, want 10.0.0.1", r.Fields["nexthop"])
	}
	if r.Fields["ifname"] != "Ethernet0" {
		t.Errorf("ifname = %q, want Ethernet0", r.Fields["ifname"])
	}
	if r.Status != "" {
		t.Errorf("Status = %q, want empty", r.Status)
	}
	if r.Seq != "swss:1234:5678" {
		t.Errorf("Seq = %q, want swss:1234:5678", r.Seq)
	}
}

func TestParseSwssRouteDel(t *testing.T) {
	p := NewRecordsParser(testLoc)
	line := RawLine{
		Source: "swss",
		Seq:    "swss:1234:9000",
		Line:   "2026-09-26.10:20:00.000000|ROUTE_TABLE:10.1.0.0/24|DEL",
	}
	r, ok := p.Parse(line)
	if !ok {
		t.Fatal("expected parse to succeed")
	}
	if r.Op != "DEL" {
		t.Errorf("Op = %q, want DEL", r.Op)
	}
	if len(r.Fields) != 0 {
		t.Errorf("Fields = %v, want empty", r.Fields)
	}
}

func TestParseSwssNeigh(t *testing.T) {
	p := NewRecordsParser(testLoc)
	line := RawLine{
		Source: "swss",
		Line:   "2026-09-26.10:15:32.101234|NEIGH_TABLE:Ethernet0:10.0.0.1|SET|neigh:00:11:22:33:44:55|family:IPv4",
	}
	r, ok := p.Parse(line)
	if !ok {
		t.Fatal("expected parse to succeed")
	}
	if r.Table != "NEIGH_TABLE" {
		t.Errorf("Table = %q, want NEIGH_TABLE", r.Table)
	}
	if r.Key != "Ethernet0:10.0.0.1" {
		t.Errorf("Key = %q, want Ethernet0:10.0.0.1", r.Key)
	}
}

func TestParseSwssSkipLines(t *testing.T) {
	p := NewRecordsParser(testLoc)

	skipLines := []string{
		"",
		"# this is a comment",
		"SIGHUP received, reopening log",
		"Reopening log file",
		"garbage",
		"only|two",
	}
	for _, line := range skipLines {
		_, ok := p.Parse(RawLine{Source: "swss", Line: line})
		if ok {
			t.Errorf("expected skip for %q", line)
		}
	}
}

func TestParseSairedisCreate(t *testing.T) {
	p := NewRecordsParser(testLoc)
	line := RawLine{
		Source: "sairedis",
		Seq:    "sairedis:5678:1234",
		Line:   `2026-09-26.10:15:32.123456|c|SAI_OBJECT_TYPE_ROUTE_ENTRY:{"dest":"10.1.0.0/24","switch_id":"oid:0x21000000000000","vr":"oid:0x3000000000022"}|SAI_ROUTE_ENTRY_ATTR_NEXT_HOP_ID=oid:0x5000000000a3c`,
	}
	r, ok := p.Parse(line)
	if !ok {
		t.Fatal("expected parse to succeed")
	}
	if r.Source != "sairedis" {
		t.Errorf("Source = %q, want sairedis", r.Source)
	}
	if r.DB != "ASIC_DB" {
		t.Errorf("DB = %q, want ASIC_DB", r.DB)
	}
	if r.Table != "SAI_OBJECT_TYPE_ROUTE_ENTRY" {
		t.Errorf("Table = %q, want SAI_OBJECT_TYPE_ROUTE_ENTRY", r.Table)
	}
	if r.Op != "c" {
		t.Errorf("Op = %q, want c", r.Op)
	}
	if r.Fields["SAI_ROUTE_ENTRY_ATTR_NEXT_HOP_ID"] != "oid:0x5000000000a3c" {
		t.Errorf("NEXT_HOP_ID = %q", r.Fields["SAI_ROUTE_ENTRY_ATTR_NEXT_HOP_ID"])
	}
	if r.Status != "" {
		t.Errorf("Status = %q, want empty", r.Status)
	}
	if r.Key[0] != '{' {
		t.Errorf("Key should be JSON, got %q", r.Key)
	}
}

func TestParseSairedisOidKey(t *testing.T) {
	p := NewRecordsParser(testLoc)
	line := RawLine{
		Source: "sairedis",
		Line:   "2026-09-26.10:15:32.123456|c|SAI_OBJECT_TYPE_NEXT_HOP:oid:0x5000000000a3c|SAI_NEXT_HOP_ATTR_IP=10.0.0.1",
	}
	r, ok := p.Parse(line)
	if !ok {
		t.Fatal("expected parse to succeed")
	}
	if r.Table != "SAI_OBJECT_TYPE_NEXT_HOP" {
		t.Errorf("Table = %q, want SAI_OBJECT_TYPE_NEXT_HOP", r.Table)
	}
	if r.Key != "oid:0x5000000000a3c" {
		t.Errorf("Key = %q, want oid:0x5000000000a3c", r.Key)
	}
}

func TestParseSairedisELine(t *testing.T) {
	p := NewRecordsParser(testLoc)

	line1 := RawLine{
		Source: "sairedis",
		Line:   `2026-09-26.10:15:32.123456|c|SAI_OBJECT_TYPE_ROUTE_ENTRY:{"dest":"10.1.0.0/24","switch_id":"oid:0x21000000000000","vr":"oid:0x3000000000022"}|SAI_ROUTE_ENTRY_ATTR_NEXT_HOP_ID=oid:0x5000000000a3c`,
	}
	r1, ok := p.Parse(line1)
	if !ok {
		t.Fatal("expected first parse to succeed")
	}
	if r1.Status != "" {
		t.Errorf("first record should have empty status, got %q", r1.Status)
	}

	line2 := RawLine{
		Source: "sairedis",
		Line:   "2026-09-26.10:15:33.000100|E|SAI_STATUS_TABLE_FULL",
	}
	r2, ok := p.Parse(line2)
	if !ok {
		t.Fatal("expected E line parse to succeed")
	}
	if r2.Status != "SAI_STATUS_TABLE_FULL" {
		t.Errorf("Status = %q, want SAI_STATUS_TABLE_FULL", r2.Status)
	}
	if r2.Fields["_response"] != "E" {
		t.Errorf("_response = %q, want E", r2.Fields["_response"])
	}
	if r2.Table != "SAI_OBJECT_TYPE_ROUTE_ENTRY" {
		t.Errorf("Table = %q, want SAI_OBJECT_TYPE_ROUTE_ENTRY", r2.Table)
	}
	if r2.Op != "c" {
		t.Errorf("Op = %q, want c (from the original record)", r2.Op)
	}
}

func TestParseSairedisELineNoPreceeding(t *testing.T) {
	p := NewRecordsParser(testLoc)

	line := RawLine{
		Source: "sairedis",
		Line:   "2026-09-26.10:15:33.000100|E|SAI_STATUS_FAILURE",
	}
	r, ok := p.Parse(line)
	if !ok {
		t.Fatal("expected E line parse to succeed even without preceding record")
	}
	if r.Status != "SAI_STATUS_FAILURE" {
		t.Errorf("Status = %q, want SAI_STATUS_FAILURE", r.Status)
	}
	if r.Op != "E" {
		t.Errorf("Op = %q, want E", r.Op)
	}
}

func TestParseSairedisSkipLines(t *testing.T) {
	p := NewRecordsParser(testLoc)
	skipLines := []string{
		"",
		"# comment line",
	}
	for _, line := range skipLines {
		_, ok := p.Parse(RawLine{Source: "sairedis", Line: line})
		if ok {
			t.Errorf("expected skip for %q", line)
		}
	}
}

func TestKeyNormalisation(t *testing.T) {
	key1 := `{"dest":"10.1.0.0/24","switch_id":"oid:0x21000000000000","vr":"oid:0x3000000000022"}`
	key2 := `{"vr":"oid:0x3000000000022","dest":"10.1.0.0/24","switch_id":"oid:0x21000000000000"}`

	norm1 := normaliseEntryKey(key1)
	norm2 := normaliseEntryKey(key2)

	if norm1 != norm2 {
		t.Errorf("normalised keys differ:\n  %s\n  %s", norm1, norm2)
	}
}

func TestNormaliseCIDR(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"10.1.0.0/24", "10.1.0.0/24"},
		{"10.1.0.5/24", "10.1.0.0/24"},
		{"::1/128", "::1/128"},
		{"not-a-cidr", "not-a-cidr"},
	}
	for _, tc := range tests {
		got := normaliseCIDR(tc.input)
		if got != tc.want {
			t.Errorf("normaliseCIDR(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestNormaliseIP(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"10.0.0.1", "10.0.0.1"},
		{"::1", "::1"},
		{"0:0:0:0:0:0:0:1", "::1"},
		{"not-an-ip", "not-an-ip"},
	}
	for _, tc := range tests {
		got := normaliseIP(tc.input)
		if got != tc.want {
			t.Errorf("normaliseIP(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestParseTimestamp(t *testing.T) {
	p := NewRecordsParser(testLoc)
	ts, ok := p.parseTimestamp("2026-09-26.10:15:32.101234")
	if !ok {
		t.Fatal("expected timestamp parse to succeed")
	}
	if ts.Year() != 2026 || ts.Month() != 9 || ts.Day() != 26 {
		t.Errorf("date = %v", ts)
	}
	if ts.Hour() != 10 || ts.Minute() != 15 || ts.Second() != 32 {
		t.Errorf("time = %v", ts)
	}
}

func BenchmarkParseSwss(b *testing.B) {
	p := NewRecordsParser(testLoc)
	line := RawLine{
		Source: "swss",
		Line:   "2026-09-26.10:15:32.101234|ROUTE_TABLE:10.1.0.0/24|SET|nexthop:10.0.0.1|ifname:Ethernet0",
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.Parse(line)
	}
}

func BenchmarkParseSairedis(b *testing.B) {
	p := NewRecordsParser(testLoc)
	line := RawLine{
		Source: "sairedis",
		Line:   `2026-09-26.10:15:32.123456|c|SAI_OBJECT_TYPE_ROUTE_ENTRY:{"dest":"10.1.0.0/24","switch_id":"oid:0x21000000000000","vr":"oid:0x3000000000022"}|SAI_ROUTE_ENTRY_ATTR_NEXT_HOP_ID=oid:0x5000000000a3c`,
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.Parse(line)
	}
}
