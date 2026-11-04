package gnmidbpath

import (
	"testing"

	pb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func path(names ...string) *pb.Path {
	elems := make([]*pb.PathElem, 0, len(names))
	for _, name := range names {
		elems = append(elems, &pb.PathElem{Name: name})
	}
	return &pb.Path{Elem: elems}
}

func TestAppend(t *testing.T) {
	tests := []struct {
		name          string
		previous      Resolution
		prefix, input *pb.Path
		want          Resolution
		processed     bool
		message       string
	}{
		{name: "nil skipped", previous: Resolution{"CONFIG_DB", "asic0"}, want: Resolution{"CONFIG_DB", "asic0"}},
		{name: "deprecated skipped", input: &pb.Path{Element: []string{"CONFIG_DB", "localhost"}}},
		{name: "empty", input: path(), message: "Invalid elem length"},
		{name: "prefix nil elems", prefix: &pb.Path{}, input: path(), message: "No valid elem"},
		{name: "short", input: path("CONFIG_DB"), message: "Invalid elem length"},
		{name: "prefix", prefix: path("CONFIG_DB"), input: path("localhost", "PORT"), want: Resolution{"CONFIG_DB", "localhost"}, processed: true},
		{name: "target ignored", prefix: &pb.Path{Target: "STATE_DB"}, input: path("CONFIG_DB", "localhost"), want: Resolution{"CONFIG_DB", "localhost"}, processed: true},
		{name: "target conflict first", previous: Resolution{"CONFIG_DB", "asic0"}, input: path("STATE_DB", "asic1"), want: Resolution{"CONFIG_DB", "asic0"}, message: "Target conflict in path"},
		{name: "instance conflict", previous: Resolution{"CONFIG_DB", "asic0"}, input: path("CONFIG_DB", "asic1"), want: Resolution{"CONFIG_DB", "asic0"}, message: "Namespace/container conflict in path"},
		{name: "empty sentinel", input: path("", ""), processed: true},
		{name: "nil element", input: &pb.Path{Elem: []*pb.PathElem{nil, {Name: "localhost"}}}, want: Resolution{"", "localhost"}, processed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, processed, err := tt.previous.Append(tt.prefix, tt.input)
			if got != tt.want || processed != tt.processed {
				t.Fatalf("got=%+v processed=%v", got, processed)
			}
			if tt.message == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if status.Code(err) != codes.Unimplemented || status.Convert(err).Message() != tt.message {
				t.Fatalf("err=%v want=%q", err, tt.message)
			}
		})
	}
}

func TestAppendPreservesBackingArrayBehavior(t *testing.T) {
	backing := []*pb.PathElem{{Name: "CONFIG_DB"}, {Name: "sentinel"}}
	prefix := &pb.Path{Elem: backing[:1]}
	input := path("localhost")
	got, processed, err := (Resolution{}).Append(prefix, input)
	if err != nil || !processed || got != (Resolution{"CONFIG_DB", "localhost"}) {
		t.Fatalf("result=%+v %v %v", got, processed, err)
	}
	if len(prefix.Elem) != 1 || backing[1] != input.Elem[0] {
		t.Fatal("prefix backing-array behavior changed")
	}
}

func TestParseTarget(t *testing.T) {
	tests := []struct {
		input, db, qualifier string
		known, malformed     bool
	}{
		{"CONFIG_DB", "CONFIG_DB", "", true, false},
		{"CONFIG_DB/asic0", "CONFIG_DB", "asic0", true, false},
		{"CONFIG_DB/", "CONFIG_DB", "", true, false},
		{"CONFIG_DB/asic0/extra", "CONFIG_DB", "", true, true},
		{"UNKNOWN/asic0", "UNKNOWN", "asic0", false, false},
		{"config_db", "config_db", "", false, false},
		{"", "", "", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			db, qualifier, known, malformed := ParseTarget(tt.input)
			if db != tt.db || qualifier != tt.qualifier || known != tt.known || malformed != tt.malformed {
				t.Fatalf("ParseTarget(%q)=(%q,%q,%v,%v)", tt.input, db, qualifier, known, malformed)
			}
		})
	}
}
