package gnmi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func mustParseBlacklist(t *testing.T, content string) *PathsBlacklist {
	t.Helper()
	b, err := ParsePathsBlacklist(strings.NewReader(content), "test")
	if err != nil {
		t.Fatalf("ParsePathsBlacklist(%q) failed: %v", content, err)
	}
	return b
}

func elemPath(target string, names ...string) (*gnmipb.Path, *gnmipb.Path) {
	prefix := &gnmipb.Path{Target: target}
	path := &gnmipb.Path{}
	for _, name := range names {
		path.Elem = append(path.Elem, &gnmipb.PathElem{Name: name})
	}
	return prefix, path
}

func TestParsePathsBlacklist(t *testing.T) {
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
		{desc: "keyed path rejected", content: "APPL_DB /interfaces/interface[name=Ethernet0]/state\n", wantErr: true},
		{desc: "missing path", content: "COUNTERS_DB\n", wantErr: true},
		{desc: "too many fields", content: "COUNTERS_DB /COUNTERS extra\n", wantErr: true},
		{desc: "empty path", content: "COUNTERS_DB /\n", wantErr: true},
		{desc: "empty element", content: "APPL_DB /a//b\n", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			b, err := ParsePathsBlacklist(strings.NewReader(tt.content), "test")
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if b.Len() != tt.wantLen {
				t.Fatalf("Len() = %d, want %d", b.Len(), tt.wantLen)
			}
		})
	}
}

func TestLoadPathsBlacklist(t *testing.T) {
	if _, err := LoadPathsBlacklist("/nonexistent/blacklist.txt"); err == nil {
		t.Fatal("expected error for missing file, got nil")
	}

	file := filepath.Join(t.TempDir(), "blacklist.txt")
	if err := os.WriteFile(file, []byte("COUNTERS_DB /COUNTERS/Ethernet0\n"), 0644); err != nil {
		t.Fatal(err)
	}
	b, err := LoadPathsBlacklist(file)
	if err != nil {
		t.Fatalf("LoadPathsBlacklist failed: %v", err)
	}
	if b.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", b.Len())
	}
}

func TestCheckPaths(t *testing.T) {
	blacklist := mustParseBlacklist(t,
		"COUNTERS_DB /COUNTERS/Ethernet0\n"+
			"* /SECRET\n"+
			"APPL_DB /PORT_TABLE/*/oper_status\n")

	tests := []struct {
		desc    string
		target  string
		elems   []string
		blocked bool
	}{
		{desc: "exact match", target: "COUNTERS_DB", elems: []string{"COUNTERS", "Ethernet0"}, blocked: true},
		{desc: "deeper than entry", target: "COUNTERS_DB", elems: []string{"COUNTERS", "Ethernet0", "SAI_PORT_STAT_IF_IN_ERRORS"}, blocked: true},
		{desc: "ancestor of entry", target: "COUNTERS_DB", elems: []string{"COUNTERS"}, blocked: true},
		{desc: "request wildcard covers entry", target: "COUNTERS_DB", elems: []string{"COUNTERS", "*"}, blocked: true},
		{desc: "sibling allowed", target: "COUNTERS_DB", elems: []string{"COUNTERS", "Ethernet4"}, blocked: false},
		{desc: "other target allowed", target: "CONFIG_DB", elems: []string{"COUNTERS", "Ethernet0"}, blocked: false},
		{desc: "wildcard target blocks any target", target: "CONFIG_DB", elems: []string{"SECRET"}, blocked: true},
		{desc: "wildcard entry element", target: "APPL_DB", elems: []string{"PORT_TABLE", "Ethernet12", "oper_status"}, blocked: true},
		{desc: "wildcard entry element, other leaf", target: "APPL_DB", elems: []string{"PORT_TABLE", "Ethernet12", "admin_status"}, blocked: false},
		{desc: "unrelated path allowed", target: "APPL_DB", elems: []string{"LLDP_ENTRY_TABLE"}, blocked: false},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			prefix, path := elemPath(tt.target, tt.elems...)
			err := blacklist.CheckPaths(prefix, []*gnmipb.Path{path})
			if tt.blocked {
				if err == nil {
					t.Fatal("expected PermissionDenied, got nil")
				}
				if status.Code(err) != codes.PermissionDenied {
					t.Fatalf("expected PermissionDenied, got %v", err)
				}
			} else if err != nil {
				t.Fatalf("expected nil, got %v", err)
			}
		})
	}
}

func TestCheckPathsPrefixElems(t *testing.T) {
	blacklist := mustParseBlacklist(t, "COUNTERS_DB /COUNTERS/Ethernet0\n")

	// Path split across prefix elems and path elems.
	prefix := &gnmipb.Path{
		Target: "COUNTERS_DB",
		Elem:   []*gnmipb.PathElem{{Name: "COUNTERS"}},
	}
	path := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "Ethernet0"}}}
	if err := blacklist.CheckPaths(prefix, []*gnmipb.Path{path}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied, got %v", err)
	}
}

func TestCheckPathsDeprecatedElement(t *testing.T) {
	blacklist := mustParseBlacklist(t, "COUNTERS_DB /COUNTERS/Ethernet0\n")

	prefix := &gnmipb.Path{Target: "COUNTERS_DB"}
	path := &gnmipb.Path{Element: []string{"COUNTERS", "Ethernet0"}}
	if err := blacklist.CheckPaths(prefix, []*gnmipb.Path{path}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied, got %v", err)
	}

	allowed := &gnmipb.Path{Element: []string{"COUNTERS", "Ethernet4"}}
	if err := blacklist.CheckPaths(prefix, []*gnmipb.Path{allowed}); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestCheckPathsKeyedRequest(t *testing.T) {
	// Keys on request elements are ignored; matching is by element name.
	blacklist := mustParseBlacklist(t, "* /interfaces/interface/state\n")

	prefix := &gnmipb.Path{}
	path := &gnmipb.Path{Elem: []*gnmipb.PathElem{
		{Name: "interfaces"},
		{Name: "interface", Key: map[string]string{"name": "Ethernet0"}},
		{Name: "state"},
	}}
	if err := blacklist.CheckPaths(prefix, []*gnmipb.Path{path}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied, got %v", err)
	}
}

func TestCheckPathsNilBlacklist(t *testing.T) {
	var blacklist *PathsBlacklist
	prefix, path := elemPath("COUNTERS_DB", "COUNTERS", "Ethernet0")
	if err := blacklist.CheckPaths(prefix, []*gnmipb.Path{path}); err != nil {
		t.Fatalf("nil blacklist must allow everything, got %v", err)
	}
}

func TestCheckPathsMultiplePaths(t *testing.T) {
	blacklist := mustParseBlacklist(t, "COUNTERS_DB /COUNTERS/Ethernet0\n")
	prefix := &gnmipb.Path{Target: "COUNTERS_DB"}
	allowed := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "COUNTERS"}, {Name: "Ethernet4"}}}
	blocked := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "COUNTERS"}, {Name: "Ethernet0"}}}

	// One blacklisted path anywhere in the request rejects the whole RPC.
	if err := blacklist.CheckPaths(prefix, []*gnmipb.Path{allowed, blocked}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied, got %v", err)
	}
	if err := blacklist.CheckPaths(prefix, []*gnmipb.Path{allowed}); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}
