package pathblacklist

import (
	"strings"
	"testing"
)

func mustParse(t *testing.T, content string) *Policy {
	t.Helper()
	p, err := Parse(strings.NewReader(content))
	if err != nil {
		t.Fatalf("Parse(%q) failed: %v", content, err)
	}
	return p
}

func TestParse(t *testing.T) {
	tests := []struct {
		desc    string
		content string
		wantLen int
		wantErr bool
	}{
		{desc: "empty file", content: "", wantLen: 0},
		{desc: "blank lines skipped", content: "\n\nCOUNTERS_DB /COUNTERS\n\n", wantLen: 1},
		{desc: "multiple entries", content: "COUNTERS_DB /COUNTERS/Ethernet0\n* /a/b\n", wantLen: 2},
		{desc: "wildcard elements", content: "APPL_DB /PORT_TABLE/*/oper_status\n", wantLen: 1},
		{desc: "suffix glob elements", content: "COUNTERS_DB /COUNTERS/Ethernet*\n", wantLen: 1},
		{desc: "root path blocks whole target", content: "CONFIG_DB /\n", wantLen: 1},
		{desc: "missing path", content: "COUNTERS_DB\n", wantErr: true},
		{desc: "too many fields", content: "COUNTERS_DB /COUNTERS extra\n", wantErr: true},
		{desc: "relative path rejected", content: "COUNTERS_DB COUNTERS/Ethernet0\n", wantErr: true},
		{desc: "keyed path rejected", content: "APPL_DB /interfaces/interface[name=Ethernet0]/state\n", wantErr: true},
		{desc: "empty element", content: "APPL_DB /a//b\n", wantErr: true},
		{desc: "trailing slash rejected", content: "APPL_DB /a/b/\n", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			p, err := Parse(strings.NewReader(tt.content))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if p.Len() != tt.wantLen {
				t.Fatalf("Len() = %d, want %d", p.Len(), tt.wantLen)
			}
		})
	}
}

func TestMatch(t *testing.T) {
	policy := mustParse(t,
		"COUNTERS_DB /COUNTERS/Ethernet0\n"+
			"* /SECRET\n"+
			"APPL_DB /PORT_TABLE/*/oper_status\n"+
			"STATE_DB /\n")

	tests := []struct {
		desc    string
		req     Path
		blocked bool
	}{
		{desc: "exact match", req: Path{"COUNTERS_DB", []string{"COUNTERS", "Ethernet0"}}, blocked: true},
		{desc: "deeper than entry", req: Path{"COUNTERS_DB", []string{"COUNTERS", "Ethernet0", "SAI_PORT_STAT_IF_IN_ERRORS"}}, blocked: true},
		{desc: "ancestor of entry", req: Path{"COUNTERS_DB", []string{"COUNTERS"}}, blocked: true},
		{desc: "empty request path overlaps", req: Path{"COUNTERS_DB", nil}, blocked: true},
		{desc: "request wildcard covers entry", req: Path{"COUNTERS_DB", []string{"COUNTERS", "*"}}, blocked: true},
		{desc: "request suffix glob covers entry", req: Path{"COUNTERS_DB", []string{"COUNTERS", "Ethernet*"}}, blocked: true},
		{desc: "request suffix glob no overlap", req: Path{"COUNTERS_DB", []string{"COUNTERS", "PortChannel*"}}, blocked: false},
		{desc: "sibling allowed", req: Path{"COUNTERS_DB", []string{"COUNTERS", "Ethernet4"}}, blocked: false},
		{desc: "other target allowed", req: Path{"CONFIG_DB", []string{"COUNTERS", "Ethernet0"}}, blocked: false},
		{desc: "wildcard target blocks any target", req: Path{"CONFIG_DB", []string{"SECRET"}}, blocked: true},
		{desc: "wildcard entry element", req: Path{"APPL_DB", []string{"PORT_TABLE", "Ethernet12", "oper_status"}}, blocked: true},
		{desc: "wildcard entry element, other leaf", req: Path{"APPL_DB", []string{"PORT_TABLE", "Ethernet12", "admin_status"}}, blocked: false},
		{desc: "root entry blocks whole target", req: Path{"STATE_DB", []string{"TRANSCEIVER_INFO", "Ethernet0"}}, blocked: true},
		{desc: "root entry other target unaffected", req: Path{"ASIC_DB", []string{"TRANSCEIVER_INFO"}}, blocked: false},
		{desc: "unrelated path allowed", req: Path{"APPL_DB", []string{"LLDP_ENTRY_TABLE"}}, blocked: false},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			matched, got := policy.Match(tt.req)
			if got != tt.blocked {
				t.Fatalf("Match(%+v) = %v (entry %q), want %v", tt.req, got, matched, tt.blocked)
			}
			if got && matched == "" {
				t.Fatal("blocked match must report the matched entry")
			}
		})
	}
}

func TestSuffixGlobEntry(t *testing.T) {
	policy := mustParse(t, "COUNTERS_DB /COUNTERS/Ethernet*\n")
	if _, ok := policy.Match(Path{"COUNTERS_DB", []string{"COUNTERS", "Ethernet68"}}); !ok {
		t.Fatal("glob entry must block matching element")
	}
	if _, ok := policy.Match(Path{"COUNTERS_DB", []string{"COUNTERS", "PortChannel1"}}); ok {
		t.Fatal("glob entry must not block non-matching element")
	}
}

func TestNilPolicy(t *testing.T) {
	var policy *Policy
	if policy.Len() != 0 {
		t.Fatalf("nil policy Len() = %d, want 0", policy.Len())
	}
	if _, ok := policy.Match(Path{"COUNTERS_DB", []string{"COUNTERS"}}); ok {
		t.Fatal("nil policy must match nothing")
	}
}

func TestMatchedEntryFormat(t *testing.T) {
	policy := mustParse(t, "COUNTERS_DB /COUNTERS/Ethernet0\n")
	matched, ok := policy.Match(Path{"COUNTERS_DB", []string{"COUNTERS", "Ethernet0"}})
	if !ok {
		t.Fatal("expected match")
	}
	if matched != "COUNTERS_DB /COUNTERS/Ethernet0" {
		t.Fatalf("matched entry = %q, want policy file format", matched)
	}
}

func TestParseScannerError(t *testing.T) {
	// A line longer than bufio.Scanner's max token size triggers a scanner error.
	if _, err := Parse(strings.NewReader("A /" + strings.Repeat("x", 1024*1024))); err == nil {
		t.Fatal("expected scanner error for oversized line")
	}
}
