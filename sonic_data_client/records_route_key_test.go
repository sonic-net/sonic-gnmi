package client

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Tests for the SAI_OBJECT_TYPE_ROUTE_ENTRY key format and per-entry splitting.
//
// Key format:  dest=<prefix>[,nh=<SAI_ROUTE_ENTRY_ATTR_NEXT_HOP_ID>]
//   * prefix is canonical (IPv4 or IPv6, host bits zeroed, IPv6 compressed)
//   * nh= is present only when the entry carries the next-hop attribute
//     (creates/sets); removes carry no attributes
//   * an entry key that is not the expected JSON falls back to
//     dest=<first 128 chars of the raw key>
//   * a bulk line (C/S/R) yields one Record per "||" entry, tagged with
//     _bulk_index/_bulk_count; the raw JSON key is kept in _entry
//
// Run with -v to see every produced record on the console:
//   go test -race -mod=vendor -run RouteKey -v ./sonic_data_client/

const routeKeyTS = "2026-09-30.19:07:46.230959"

// Real lines captured from a hardware switch (10.9.100.144) and the VS.
const (
	realBulkCreateTwoVRFs = routeKeyTS + `|C|SAI_OBJECT_TYPE_ROUTE_ENTRY||{"dest":"10.100.10.2/32","switch_id":"oid:0x21000000000000","vr":"oid:0x30000000015d5"}|SAI_ROUTE_ENTRY_ATTR_NEXT_HOP_ID=oid:0x4000000002f56||{"dest":"10.100.11.2/32","switch_id":"oid:0x21000000000000","vr":"oid:0x30000000015dc"}|SAI_ROUTE_ENTRY_ATTR_NEXT_HOP_ID=oid:0x4000000002f60`
	realSingleCreateV6    = routeKeyTS + `|c|SAI_OBJECT_TYPE_ROUTE_ENTRY:{"dest":"fe80::ea47:f3ff:fe00:a08/128","switch_id":"oid:0x21000000000000","vr":"oid:0x3000000000001"}|SAI_ROUTE_ENTRY_ATTR_PACKET_ACTION=SAI_PACKET_ACTION_FORWARD|SAI_ROUTE_ENTRY_ATTR_NEXT_HOP_ID=oid:0x4000000002f56`
	realBulkSetDropRoute  = routeKeyTS + `|S|SAI_OBJECT_TYPE_ROUTE_ENTRY||{"dest":"0.0.0.0/0","switch_id":"oid:0x21000000000000","vr":"oid:0x3000000000001"}|SAI_ROUTE_ENTRY_ATTR_PACKET_ACTION=SAI_PACKET_ACTION_DROP||{"dest":"::/0","switch_id":"oid:0x21000000000000","vr":"oid:0x3000000000001"}|SAI_ROUTE_ENTRY_ATTR_PACKET_ACTION=SAI_PACKET_ACTION_DROP`
	realSingleRemove      = routeKeyTS + `|r|SAI_OBJECT_TYPE_ROUTE_ENTRY:{"dest":"10.0.2.1/32","switch_id":"oid:0x21000000000000","vr":"oid:0x3000000000001"}`
	realBulkRemove        = routeKeyTS + `|R|SAI_OBJECT_TYPE_ROUTE_ENTRY||{"dest":"171.15.0.0/24","switch_id":"oid:0x21000000000000","vr":"oid:0x3000000000001"}||{"dest":"171.15.1.0/24","switch_id":"oid:0x21000000000000","vr":"oid:0x3000000000001"}`
	vsBulkRemoveOneEntry  = `2026-09-30.21:33:20.973602|R|SAI_OBJECT_TYPE_ROUTE_ENTRY||{"dest":"198.51.100.0/24","switch_id":"oid:0x21000000000000","vr":"oid:0x3000000000082"}`
	// Seen once in 175k lines on 10.9.100.144: the recorder truncated the key
	// (no closing brace, no vr). The fallback must still produce a usable key.
	realTruncatedKeySet = routeKeyTS + `|S|SAI_OBJECT_TYPE_ROUTE_ENTRY||{"dest":"172.82.135.0/24","switch_id":"oid:0x21000000000000"`
)

// parseAllRoute runs the real parser and prints each record so the output is
// visible on the console (go test -v).
func parseAllRoute(t *testing.T, name, line string) []*Record {
	t.Helper()
	p := NewRecordsParser(time.UTC)
	recs := p.ParseAll(RawLine{Source: "sairedis", Seq: "sairedis:1:0", Line: line})
	t.Logf("%s: %d record(s) from line %.110q", name, len(recs), line)
	for i, r := range recs {
		t.Logf("  [%d] op=%s key=%q", i, r.Op, r.Key)
		t.Logf("      fields=%s status=%q", fieldsForLog(r.Fields), r.Status)
	}
	return recs
}

func fieldsForLog(f map[string]string) string {
	var parts []string
	for _, k := range []string{"SAI_ROUTE_ENTRY_ATTR_NEXT_HOP_ID", "SAI_ROUTE_ENTRY_ATTR_PACKET_ACTION", "_bulk_index", "_bulk_count", "_response", "_entry"} {
		if v, ok := f[k]; ok {
			parts = append(parts, fmt.Sprintf("%s=%s", k, v))
		}
	}
	return "{" + strings.Join(parts, " ") + "}"
}

func wantKey(t *testing.T, r *Record, want string) {
	t.Helper()
	if r.Key != want {
		t.Errorf("key = %q, want %q", r.Key, want)
	}
}

// --- one test per case ------------------------------------------------------

func TestRouteKeySingleCreateV4WithNextHop(t *testing.T) {
	line := routeKeyTS + `|c|SAI_OBJECT_TYPE_ROUTE_ENTRY:{"dest":"10.1.0.0/24","switch_id":"oid:0x21000000000000","vr":"oid:0x3000000000022"}|SAI_ROUTE_ENTRY_ATTR_NEXT_HOP_ID=oid:0x5000000000a3c`
	recs := parseAllRoute(t, "single c v4", line)
	if len(recs) != 1 {
		t.Fatalf("want 1 record, got %d", len(recs))
	}
	wantKey(t, recs[0], "dest=10.1.0.0/24,nh=oid:0x5000000000a3c")
	if recs[0].Table != "SAI_OBJECT_TYPE_ROUTE_ENTRY" || recs[0].Op != "c" {
		t.Errorf("table/op = %s/%s", recs[0].Table, recs[0].Op)
	}
	if _, bulk := recs[0].Fields["_bulk_index"]; bulk {
		t.Error("single op must not carry _bulk_index")
	}
	if !strings.HasPrefix(recs[0].Fields["_entry"], `{"dest":"10.1.0.0/24"`) {
		t.Errorf("_entry should hold the raw JSON key, got %q", recs[0].Fields["_entry"])
	}
}

func TestRouteKeySingleRemoveNoAttrs(t *testing.T) {
	recs := parseAllRoute(t, "single r, no attributes", realSingleRemove)
	if len(recs) != 1 {
		t.Fatalf("want 1 record, got %d", len(recs))
	}
	wantKey(t, recs[0], "dest=10.0.2.1/32")
	if recs[0].Op != "r" {
		t.Errorf("op = %s", recs[0].Op)
	}
}

func TestRouteKeySingleCreateV6CanonicalWithPacketAction(t *testing.T) {
	recs := parseAllRoute(t, "single c v6 (real line)", realSingleCreateV6)
	if len(recs) != 1 {
		t.Fatalf("want 1 record, got %d", len(recs))
	}
	// Link-local host route: prefix canonical, nh appended, PACKET_ACTION stays in fields.
	wantKey(t, recs[0], "dest=fe80::ea47:f3ff:fe00:a08/128,nh=oid:0x4000000002f56")
	if recs[0].Fields["SAI_ROUTE_ENTRY_ATTR_PACKET_ACTION"] != "SAI_PACKET_ACTION_FORWARD" {
		t.Errorf("PACKET_ACTION lost: %v", recs[0].Fields)
	}
}

func TestRouteKeyV6NonCanonicalIsCompressed(t *testing.T) {
	line := routeKeyTS + `|c|SAI_OBJECT_TYPE_ROUTE_ENTRY:{"dest":"2001:0db8:0000:0000:0000:0000:0000:0001/64","switch_id":"oid:0x21000000000000","vr":"oid:0x3000000000001"}|SAI_ROUTE_ENTRY_ATTR_NEXT_HOP_ID=oid:0x40000000000aa`
	recs := parseAllRoute(t, "v6 non-canonical dest", line)
	if len(recs) != 1 {
		t.Fatalf("want 1 record, got %d", len(recs))
	}
	// net.ParseCIDR zeroes host bits and compresses: 2001:db8::/64
	wantKey(t, recs[0], "dest=2001:db8::/64,nh=oid:0x40000000000aa")
}

func TestRouteKeyBulkCreateTwoEntriesTwoVRFs(t *testing.T) {
	recs := parseAllRoute(t, "bulk C, 2 entries in different VRFs (real line)", realBulkCreateTwoVRFs)
	if len(recs) != 2 {
		t.Fatalf("want 2 records (one per entry), got %d", len(recs))
	}
	wantKey(t, recs[0], "dest=10.100.10.2/32,nh=oid:0x4000000002f56")
	wantKey(t, recs[1], "dest=10.100.11.2/32,nh=oid:0x4000000002f60")
	for i, r := range recs {
		if r.Fields["_bulk_index"] != fmt.Sprint(i) || r.Fields["_bulk_count"] != "2" {
			t.Errorf("[%d] bulk tags = %s/%s", i, r.Fields["_bulk_index"], r.Fields["_bulk_count"])
		}
		if r.Op != "C" || r.TS != recs[0].TS || r.Seq != recs[0].Seq || r.Raw != realBulkCreateTwoVRFs {
			t.Errorf("[%d] op/ts/seq/raw must be shared across the line", i)
		}
	}
	// Each entry keeps its own attributes and its own vr, never the line's.
	if recs[0].Fields["SAI_ROUTE_ENTRY_ATTR_NEXT_HOP_ID"] == recs[1].Fields["SAI_ROUTE_ENTRY_ATTR_NEXT_HOP_ID"] {
		t.Error("next hops leaked across entries")
	}
	if !strings.Contains(recs[0].Fields["_entry"], "15d5") || !strings.Contains(recs[1].Fields["_entry"], "15dc") {
		t.Error("vr leaked across entries")
	}
}

func TestRouteKeyBulkRemoveNoAttrs(t *testing.T) {
	recs := parseAllRoute(t, "bulk R, 2 entries (real line)", realBulkRemove)
	if len(recs) != 2 {
		t.Fatalf("want 2 records, got %d", len(recs))
	}
	wantKey(t, recs[0], "dest=171.15.0.0/24")
	wantKey(t, recs[1], "dest=171.15.1.0/24")
	for _, r := range recs {
		if _, has := r.Fields["SAI_ROUTE_ENTRY_ATTR_NEXT_HOP_ID"]; has {
			t.Error("remove must not invent a next hop")
		}
	}
}

func TestRouteKeyBulkRemoveSingleEntryFromVS(t *testing.T) {
	// The exact line from the VS whose gNMI output had key="" before this change.
	recs := parseAllRoute(t, "bulk R, 1 entry (VS line)", vsBulkRemoveOneEntry)
	if len(recs) != 1 {
		t.Fatalf("want 1 record, got %d", len(recs))
	}
	wantKey(t, recs[0], "dest=198.51.100.0/24")
	if recs[0].Fields["_bulk_count"] != "1" {
		t.Errorf("_bulk_count = %q", recs[0].Fields["_bulk_count"])
	}
}

func TestRouteKeyBulkSetDropRoutesV4AndV6(t *testing.T) {
	recs := parseAllRoute(t, "bulk S, drop routes v4+v6 (real line)", realBulkSetDropRoute)
	if len(recs) != 2 {
		t.Fatalf("want 2 records, got %d", len(recs))
	}
	// Blackhole routes carry PACKET_ACTION but no next hop: dest only.
	wantKey(t, recs[0], "dest=0.0.0.0/0")
	wantKey(t, recs[1], "dest=::/0")
	if recs[1].Fields["SAI_ROUTE_ENTRY_ATTR_PACKET_ACTION"] != "SAI_PACKET_ACTION_DROP" {
		t.Errorf("fields = %v", recs[1].Fields)
	}
}

func TestRouteKeyTruncatedKeyFallsBackToRawPrefix(t *testing.T) {
	recs := parseAllRoute(t, "truncated key (real line)", realTruncatedKeySet)
	if len(recs) != 1 {
		t.Fatalf("want 1 record, got %d", len(recs))
	}
	raw := `{"dest":"172.82.135.0/24","switch_id":"oid:0x21000000000000"`
	wantKey(t, recs[0], "dest="+raw) // shorter than 128 chars: copied whole
	if recs[0].Fields["_entry"] != raw {
		t.Errorf("_entry = %q", recs[0].Fields["_entry"])
	}
}

func TestRouteKeyGarbageKeyIsCappedAt128(t *testing.T) {
	garbage := strings.Repeat("x", 300)
	line := routeKeyTS + `|C|SAI_OBJECT_TYPE_ROUTE_ENTRY||` + garbage + `|SAI_ROUTE_ENTRY_ATTR_NEXT_HOP_ID=oid:0x1`
	recs := parseAllRoute(t, "300-char garbage key", line)
	if len(recs) != 1 {
		t.Fatalf("want 1 record, got %d", len(recs))
	}
	want := "dest=" + strings.Repeat("x", saiKeyFallbackLen) + ",nh=oid:0x1"
	wantKey(t, recs[0], want)
	if len(recs[0].Key) != len("dest=")+saiKeyFallbackLen+len(",nh=oid:0x1") {
		t.Errorf("key length %d", len(recs[0].Key))
	}
}

func TestRouteKeyJSONWithoutDestFallsBack(t *testing.T) {
	line := routeKeyTS + `|c|SAI_OBJECT_TYPE_ROUTE_ENTRY:{"switch_id":"oid:0x21000000000000","vr":"oid:0x3000000000001"}`
	recs := parseAllRoute(t, "JSON key with no dest", line)
	if len(recs) != 1 {
		t.Fatalf("want 1 record, got %d", len(recs))
	}
	wantKey(t, recs[0], `dest={"switch_id":"oid:0x21000000000000","vr":"oid:0x3000000000001"}`)
}

func TestRouteKeyBulkFailureReEmitsEveryEntry(t *testing.T) {
	p := NewRecordsParser(time.UTC)
	first := p.ParseAll(RawLine{Source: "sairedis", Seq: "sairedis:1:0", Line: realBulkCreateTwoVRFs})
	if len(first) != 2 {
		t.Fatalf("setup: want 2 records, got %d", len(first))
	}
	eLine := routeKeyTS + "|E|SAI_STATUS_INSUFFICIENT_RESOURCES"
	failed := p.ParseAll(RawLine{Source: "sairedis", Seq: "sairedis:1:400", Line: eLine})
	t.Logf("E line after bulk C: %d record(s) re-emitted", len(failed))
	for i, r := range failed {
		t.Logf("  [%d] op=%s key=%q status=%s fields=%s", i, r.Op, r.Key, r.Status, fieldsForLog(r.Fields))
	}
	if len(failed) != 2 {
		t.Fatalf("want both entries re-emitted, got %d", len(failed))
	}
	for i, r := range failed {
		if r.Status != "SAI_STATUS_INSUFFICIENT_RESOURCES" || r.Fields["_response"] != "E" || r.Op != "C" {
			t.Errorf("[%d] status=%q _response=%q op=%s", i, r.Status, r.Fields["_response"], r.Op)
		}
		if r.Key != first[i].Key {
			t.Errorf("[%d] key changed on re-emit: %q vs %q", i, r.Key, first[i].Key)
		}
		if first[i].Status != "" || first[i].Fields["_response"] != "" {
			t.Errorf("[%d] original record must not be mutated by the E line", i)
		}
	}
	// A second E with nothing pending is a bare failure record, not a repeat.
	again := p.ParseAll(RawLine{Source: "sairedis", Line: eLine})
	if len(again) != 1 || again[0].Op != "E" {
		t.Errorf("stray E: %+v", again)
	}
}

// Real pair from 10.9.100.144 (sairedis.rec.2.gz): a bulk remove of one
// entry failed; the E line carries the overall status and then one status per
// entry, "||"-separated.
func TestRouteKeyBulkFailurePerEntryStatus(t *testing.T) {
	p := NewRecordsParser(time.UTC)
	rLine := `2026-09-30.12:10:02.092594|R|SAI_OBJECT_TYPE_ROUTE_ENTRY||{"dest":"10.52.2.1/32","switch_id":"oid:0x21000000000000","vr":"oid:0x3000000004be8"}`
	eLine := `2026-09-30.12:10:02.094373|E|SAI_STATUS_FAILURE||SAI_STATUS_ITEM_NOT_FOUND`
	if got := p.ParseAll(RawLine{Source: "sairedis", Line: rLine}); len(got) != 1 {
		t.Fatalf("setup: want 1 record, got %d", len(got))
	}
	failed := p.ParseAll(RawLine{Source: "sairedis", Line: eLine})
	t.Logf("real bulk E line %q -> %d record(s)", eLine, len(failed))
	for i, r := range failed {
		t.Logf("  [%d] op=%s key=%q status=%s _bulk_status=%s", i, r.Op, r.Key, r.Status, r.Fields["_bulk_status"])
	}
	if len(failed) != 1 {
		t.Fatalf("want 1 re-emitted record, got %d", len(failed))
	}
	wantKey(t, failed[0], "dest=10.52.2.1/32")
	if failed[0].Status != "SAI_STATUS_ITEM_NOT_FOUND" {
		t.Errorf("status = %q, want the per-entry SAI_STATUS_ITEM_NOT_FOUND", failed[0].Status)
	}
	if failed[0].Fields["_bulk_status"] != "SAI_STATUS_FAILURE" || failed[0].Fields["_response"] != "E" {
		t.Errorf("fields = %v", failed[0].Fields)
	}

	// Two entries, two statuses: each Record gets its own.
	p.ParseAll(RawLine{Source: "sairedis", Line: realBulkCreateTwoVRFs})
	two := p.ParseAll(RawLine{Source: "sairedis", Line: routeKeyTS + "|E|SAI_STATUS_FAILURE||SAI_STATUS_SUCCESS||SAI_STATUS_INSUFFICIENT_RESOURCES"})
	for i, r := range two {
		t.Logf("  two-entry E [%d] key=%q status=%s", i, r.Key, r.Status)
	}
	if len(two) != 2 || two[0].Status != "SAI_STATUS_SUCCESS" || two[1].Status != "SAI_STATUS_INSUFFICIENT_RESOURCES" {
		t.Errorf("per-entry statuses wrong: %+v", two)
	}

	// Mismatched count (recorder gave 3 statuses for 2 entries): fall back to overall.
	p.ParseAll(RawLine{Source: "sairedis", Line: realBulkCreateTwoVRFs})
	mis := p.ParseAll(RawLine{Source: "sairedis", Line: routeKeyTS + "|E|SAI_STATUS_FAILURE||A||B||C"})
	if len(mis) != 2 || mis[0].Status != "SAI_STATUS_FAILURE" || mis[1].Status != "SAI_STATUS_FAILURE" {
		t.Errorf("mismatched per-entry list should fall back to overall: %+v", mis)
	}
}

func TestRouteKeyLargeBulkLineOneRecordPerEntry(t *testing.T) {
	// 10.9.100.144 has bulk lines of 65,536 entries (9.3 MB). Build one and
	// prove every entry comes out with a distinct key.
	const n = 65536
	var b strings.Builder
	b.WriteString(routeKeyTS + "|C|SAI_OBJECT_TYPE_ROUTE_ENTRY")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `||{"dest":"10.%d.%d.0/24","switch_id":"oid:0x21000000000000","vr":"oid:0x3000000000001"}|SAI_ROUTE_ENTRY_ATTR_NEXT_HOP_ID=oid:0x%x`,
			(i>>8)&0xff, i&0xff, 0x4000000000000+i%7)
	}
	line := b.String()
	p := NewRecordsParser(time.UTC)
	start := time.Now()
	recs := p.ParseAll(RawLine{Source: "sairedis", Line: line})
	el := time.Since(start)
	t.Logf("%d-byte bulk line -> %d records in %s; first=%q last=%q", len(line), len(recs), el, recs[0].Key, recs[len(recs)-1].Key)
	if len(recs) != n {
		t.Fatalf("want %d records, got %d", n, len(recs))
	}
	seen := make(map[string]struct{}, n)
	for _, r := range recs {
		seen[r.Key] = struct{}{}
	}
	if len(seen) != n {
		t.Errorf("keys not distinct: %d unique of %d", len(seen), n)
	}
	if recs[n-1].Fields["_bulk_index"] != fmt.Sprint(n-1) || recs[n-1].Fields["_bulk_count"] != fmt.Sprint(n) {
		t.Errorf("last bulk tags: %s/%s", recs[n-1].Fields["_bulk_index"], recs[n-1].Fields["_bulk_count"])
	}
}

// Parse (single-record interface) keeps working and returns the first entry.
func TestRouteKeyParseReturnsFirstEntry(t *testing.T) {
	p := NewRecordsParser(time.UTC)
	r, ok := p.Parse(RawLine{Source: "sairedis", Line: realBulkCreateTwoVRFs})
	if !ok || r == nil {
		t.Fatal("Parse failed")
	}
	t.Logf("Parse (first only): key=%q", r.Key)
	wantKey(t, r, "dest=10.100.10.2/32,nh=oid:0x4000000002f56")
}

// The matcher still correlates an APPL_DB route subscription to these records.
func TestRouteKeyCorrelationStillMatches(t *testing.T) {
	p := NewRecordsParser(time.UTC)
	recs := p.ParseAll(RawLine{Source: "sairedis", Line: realBulkCreateTwoVRFs})
	m := NewRecordsMatcher([]Subscription{{DB: "APPL_DB", Table: "ROUTE_TABLE", Key: "10.100.11.2/32"}})
	var hits []string
	for _, r := range recs {
		if ok, how := m.Match(r); ok {
			hits = append(hits, r.Key+" via "+how)
		}
	}
	t.Logf("correlation hits for ROUTE_TABLE/10.100.11.2/32: %v", hits)
	if len(hits) != 1 || !strings.HasPrefix(hits[0], "dest=10.100.11.2/32") {
		t.Errorf("want exactly the 10.100.11.2/32 entry to correlate, got %v", hits)
	}
}

// filter= must select entries, not whole bulk lines: a filter for one dest,
// one vr or one next hop matches only the sibling that carries it.
func TestRouteKeyFilterSelectsSingleBulkEntry(t *testing.T) {
	p := NewRecordsParser(time.UTC)
	recs := p.ParseAll(RawLine{Source: "sairedis", Line: realBulkCreateTwoVRFs})
	if len(recs) != 2 {
		t.Fatalf("setup: want 2 records, got %d", len(recs))
	}
	cases := []struct {
		filter string
		want   []int // indexes of entries expected to pass
	}{
		{"10.100.11.2", []int{1}},                         // dest of entry 1
		{"15d5", []int{0}},                                // vr of entry 0 (in _entry)
		{"oid:0x4000000002f60", []int{1}},                 // next hop of entry 1
		{"SAI_ROUTE_ENTRY_ATTR_NEXT_HOP_ID", []int{0, 1}}, // both carry it
		{"10.100.10.2/32,nh=", []int{0}},                  // rendered key text
		{"no-such-text", nil},
	}
	for _, c := range cases {
		sub := Subscription{DB: "ASIC_DB", Table: "ASIC_STATE", Filter: c.filter}
		var got []int
		for i, r := range recs {
			if filterPass(r, &sub) {
				got = append(got, i)
			}
		}
		t.Logf("filter=%q -> entries %v", c.filter, got)
		if fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("filter=%q: got %v, want %v", c.filter, got, c.want)
		}
	}
	// Single-op records keep matching on the whole line.
	single := p.ParseAll(RawLine{Source: "sairedis", Line: realSingleCreateV6})
	if !filterPass(single[0], &Subscription{Filter: "SAI_PACKET_ACTION_FORWARD"}) ||
		filterPass(single[0], &Subscription{Filter: "zzz"}) {
		t.Error("single-op filter behaviour changed")
	}
}

// Non-route object types are untouched by the key=value format.
func TestRouteKeyOtherTypesUnchanged(t *testing.T) {
	line := routeKeyTS + `|c|SAI_OBJECT_TYPE_NEIGHBOR_ENTRY:{"ip_address":"10.0.0.1","rif_id":"oid:0x6000000000a10","switch_id":"oid:0x21000000000000"}|SAI_NEIGHBOR_ENTRY_ATTR_DST_MAC_ADDRESS=00:11:22:33:44:55`
	recs := parseAllRoute(t, "neighbor entry (not a route)", line)
	if len(recs) != 1 || recs[0].Key[0] != '{' {
		t.Fatalf("neighbor key should stay canonical JSON, got %+v", recs)
	}
}
