package gnmi

import (
	"reflect"
	"strings"
	"testing"

	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/sonic-net/sonic-gnmi/pkg/pathblacklist"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func mustPolicy(t *testing.T, content string) *pathblacklist.Policy {
	t.Helper()
	p, err := pathblacklist.Parse(strings.NewReader(content))
	if err != nil {
		t.Fatalf("Parse(%q) failed: %v", content, err)
	}
	return p
}

func TestCheckPathsBlacklist(t *testing.T) {
	policy := mustPolicy(t, "COUNTERS_DB /COUNTERS/Ethernet0\n")
	prefix := &gnmipb.Path{Target: "COUNTERS_DB"}
	blocked := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "COUNTERS"}, {Name: "Ethernet0"}}}
	allowed := &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "COUNTERS"}, {Name: "Ethernet4"}}}

	err := checkPathsBlacklist(policy, prefix, []*gnmipb.Path{allowed, blocked})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied, got %v", err)
	}
	// The denial must be generic: no policy contents in the client-visible error.
	if msg := status.Convert(err).Message(); strings.Contains(msg, "COUNTERS") {
		t.Fatalf("error message leaks policy contents: %q", msg)
	}

	if err := checkPathsBlacklist(policy, prefix, []*gnmipb.Path{allowed}); err != nil {
		t.Fatalf("expected nil for allowed path, got %v", err)
	}
	if err := checkPathsBlacklist(nil, prefix, []*gnmipb.Path{blocked}); err != nil {
		t.Fatalf("nil policy must allow everything, got %v", err)
	}
}

func TestNormalizeRequestPath(t *testing.T) {
	tests := []struct {
		desc   string
		prefix *gnmipb.Path
		path   *gnmipb.Path
		want   pathblacklist.Path
	}{
		{
			desc:   "target form",
			prefix: &gnmipb.Path{Target: "CONFIG_DB"},
			path:   &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "PORT"}}},
			want:   pathblacklist.Path{Target: "CONFIG_DB", Elems: []string{"PORT"}},
		},
		{
			desc:   "path split between prefix and path elems",
			prefix: &gnmipb.Path{Target: "COUNTERS_DB", Elem: []*gnmipb.PathElem{{Name: "COUNTERS"}}},
			path:   &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "Ethernet0"}}},
			want:   pathblacklist.Path{Target: "COUNTERS_DB", Elems: []string{"COUNTERS", "Ethernet0"}},
		},
		{
			desc:   "deprecated Element field",
			prefix: &gnmipb.Path{Target: "COUNTERS_DB", Element: []string{"COUNTERS"}},
			path:   &gnmipb.Path{Element: []string{"Ethernet0"}},
			want:   pathblacklist.Path{Target: "COUNTERS_DB", Elems: []string{"COUNTERS", "Ethernet0"}},
		},
		{
			desc:   "mixed encodings: prefix Element with path Elem",
			prefix: &gnmipb.Path{Target: "COUNTERS_DB", Element: []string{"COUNTERS"}},
			path:   &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "Ethernet0"}}},
			want:   pathblacklist.Path{Target: "COUNTERS_DB", Elems: []string{"COUNTERS", "Ethernet0"}},
		},
		{
			desc:   "mixed encodings: prefix Elem with path Element",
			prefix: &gnmipb.Path{Target: "COUNTERS_DB", Elem: []*gnmipb.PathElem{{Name: "COUNTERS"}}},
			path:   &gnmipb.Path{Element: []string{"Ethernet0"}},
			want:   pathblacklist.Path{Target: "COUNTERS_DB", Elems: []string{"COUNTERS", "Ethernet0"}},
		},
		{
			desc:   "keys on elements are ignored",
			prefix: &gnmipb.Path{},
			path: &gnmipb.Path{Elem: []*gnmipb.PathElem{
				{Name: "interfaces"},
				{Name: "interface", Key: map[string]string{"name": "Ethernet0"}},
			}},
			want: pathblacklist.Path{Elems: []string{"interfaces", "interface"}},
		},
		{
			desc:   "sonic-db origin in prefix",
			prefix: &gnmipb.Path{Origin: "sonic-db"},
			path: &gnmipb.Path{Elem: []*gnmipb.PathElem{
				{Name: "CONFIG_DB"}, {Name: "localhost"}, {Name: "PORT"}}},
			want: pathblacklist.Path{Target: "CONFIG_DB", Elems: []string{"PORT"}},
		},
		{
			desc:   "sonic-db origin in path",
			prefix: nil,
			path: &gnmipb.Path{Origin: "sonic-db", Elem: []*gnmipb.PathElem{
				{Name: "CONFIG_DB"}, {Name: "localhost"}, {Name: "PORT"}, {Name: "Ethernet0"}}},
			want: pathblacklist.Path{Target: "CONFIG_DB", Elems: []string{"PORT", "Ethernet0"}},
		},
		{
			desc:   "sonic-db origin without table maps to whole database",
			prefix: &gnmipb.Path{Origin: "sonic-db"},
			path:   &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "CONFIG_DB"}, {Name: "localhost"}}},
			want:   pathblacklist.Path{Target: "CONFIG_DB"},
		},
		{
			desc:   "nil prefix and empty path",
			prefix: nil,
			path:   &gnmipb.Path{},
			want:   pathblacklist.Path{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got := normalizeRequestPath(tt.prefix, tt.path)
			if got.Target != tt.want.Target || !reflect.DeepEqual(got.Elems, tt.want.Elems) {
				t.Fatalf("normalizeRequestPath() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestCheckPathsBlacklistSonicDbForm(t *testing.T) {
	policy := mustPolicy(t, "CONFIG_DB /PORT\n")

	// Both wire forms of the same request must be blocked.
	targetForm := checkPathsBlacklist(policy,
		&gnmipb.Path{Target: "CONFIG_DB"},
		[]*gnmipb.Path{{Elem: []*gnmipb.PathElem{{Name: "PORT"}}}})
	if status.Code(targetForm) != codes.PermissionDenied {
		t.Fatalf("target form: expected PermissionDenied, got %v", targetForm)
	}

	nativeForm := checkPathsBlacklist(policy,
		&gnmipb.Path{Origin: "sonic-db"},
		[]*gnmipb.Path{{Elem: []*gnmipb.PathElem{
			{Name: "CONFIG_DB"}, {Name: "localhost"}, {Name: "PORT"}}}})
	if status.Code(nativeForm) != codes.PermissionDenied {
		t.Fatalf("native form: expected PermissionDenied, got %v", nativeForm)
	}

	otherTable := checkPathsBlacklist(policy,
		&gnmipb.Path{Origin: "sonic-db"},
		[]*gnmipb.Path{{Elem: []*gnmipb.PathElem{
			{Name: "CONFIG_DB"}, {Name: "localhost"}, {Name: "VLAN"}}}})
	if otherTable != nil {
		t.Fatalf("native form other table: expected nil, got %v", otherTable)
	}
}

func TestCheckPathsBlacklistRootEntry(t *testing.T) {
	policy := mustPolicy(t, "STATE_DB /\n")
	blocked := checkPathsBlacklist(policy,
		&gnmipb.Path{Target: "STATE_DB"},
		[]*gnmipb.Path{{Elem: []*gnmipb.PathElem{{Name: "TRANSCEIVER_INFO"}}}})
	if status.Code(blocked) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for root entry, got %v", blocked)
	}
	allowed := checkPathsBlacklist(policy,
		&gnmipb.Path{Target: "APPL_DB"},
		[]*gnmipb.Path{{Elem: []*gnmipb.PathElem{{Name: "TRANSCEIVER_INFO"}}}})
	if allowed != nil {
		t.Fatalf("root entry must not affect other targets, got %v", allowed)
	}
}
