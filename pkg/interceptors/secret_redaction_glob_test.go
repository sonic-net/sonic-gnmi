package interceptors

import (
	"context"
	"strings"
	"testing"

	pb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestSecretRedactionAmbiguousTableGlob(t *testing.T) {
	for _, pattern := range []string{"SNMP_COMMUNIT?", "NTP_KE?", "SNMP_TRAP_*", "[S]NMP_COMMUNITY",
		`NTP_K\EY`, "NTP_KE[Z-A]", "SNMP_COMMUNIT[Z-A]", "NTP_K[E-]x]Y",
		"SNMP_COMMUNITY|prefix", "SNMP_COMMUNITY|*", "SNMP_COMMUNITY?*", "SNMP_TRAP_CONFIG?*", "NTP_KEY?*"} {
		for _, route := range []struct {
			target, origin string
			native         bool
		}{
			{"CONFIG_DB", "", false},
			{"CONFIG_DB/ns", "sonic-db", false},
			{"", "sonic-db", true},
			{"UNRECOGNIZED_DB", "sonic-db", true},
			{"CONFIG_DB/ns/extra", "sonic-db", true},
		} {
			prefix := &pb.Path{Target: route.target, Origin: route.origin}
			path := testPath(pattern)
			if route.native {
				path = testPath("CONFIG_DB", "localhost", pattern)
			}
			_, err := (&secretRedactor{}).UnaryInterceptor()(context.Background(),
				&pb.GetRequest{Prefix: prefix, Path: []*pb.Path{path}},
				&grpc.UnaryServerInfo{FullMethod: gnmiGetMethod},
				func(context.Context, interface{}) (interface{}, error) {
					t.Error("ambiguous secret-bearing table glob reached retrieval")
					return &pb.GetResponse{}, nil
				})
			if status.Code(err) != codes.InvalidArgument {
				t.Fatal("secret-bearing table glob did not fail closed")
			}
		}
	}
}

func TestSecretRedactionSetCompletionPrefix(t *testing.T) {
	prefix := testPath("CONFIG_DB", "localhost", "SNMP_COMMUNITY")
	paths := requestPaths(&pb.SetRequest{
		Prefix:  prefix,
		Delete:  []*pb.Path{testPath(syntheticSecret)},
		Replace: []*pb.Update{{Path: testPath(syntheticSecret)}},
		Update:  []*pb.Update{{Path: testPath(syntheticSecret)}},
	})
	if strings.Contains(strings.Join(paths, " "), syntheticSecret) {
		t.Fatal("prefix-relative Set completion log contains the synthetic community")
	}
}

func TestSecretRedactionPreservesSafeTableQueries(t *testing.T) {
	for _, name := range []string{"*", "PORT*", "PORT[0-9]*", "VENDOR_LITERAL_TABLE"} {
		result, err := (&secretRedactor{}).UnaryInterceptor()(context.Background(),
			&pb.GetRequest{Prefix: &pb.Path{Target: "CONFIG_DB"}, Path: []*pb.Path{testPath(name)}},
			&grpc.UnaryServerInfo{FullMethod: gnmiGetMethod},
			func(context.Context, interface{}) (interface{}, error) {
				return (&secretFixtureServer{}).Get(context.Background(), &pb.GetRequest{})
			})
		if err != nil {
			t.Fatal("safe table query was rejected")
		}
		assertRedactedWire(t, result.(*pb.GetResponse))
	}
	mixed := testPath("NTP_KE?")
	mixed.Element = []string{"PORT"}
	_, err := (&secretRedactor{}).UnaryInterceptor()(context.Background(),
		&pb.GetRequest{Prefix: &pb.Path{Target: "CONFIG_DB"}, Path: []*pb.Path{mixed}},
		&grpc.UnaryServerInfo{FullMethod: gnmiGetMethod},
		func(context.Context, interface{}) (interface{}, error) {
			t.Error("mixed path encodings reached retrieval")
			return &pb.GetResponse{}, nil
		})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatal("ambiguous path encoding did not fail closed")
	}
	for _, prefix := range []*pb.Path{
		{Target: "CONFIG_DB", Element: []string{"PORT"}},
		{Target: "CONFIG_DB", Elem: []*pb.PathElem{{Name: "PORT"}}},
	} {
		path := testPath("NTP_KE?")
		if len(prefix.Elem) > 0 {
			path = &pb.Path{Element: []string{"NTP_KE?"}}
		}
		_, err := (&secretRedactor{}).UnaryInterceptor()(context.Background(),
			&pb.GetRequest{Prefix: prefix, Path: []*pb.Path{path}},
			&grpc.UnaryServerInfo{FullMethod: gnmiGetMethod},
			func(context.Context, interface{}) (interface{}, error) {
				t.Error("cross-prefix mixed encodings reached retrieval")
				return &pb.GetResponse{}, nil
			})
		if status.Code(err) != codes.InvalidArgument {
			t.Fatal("cross-prefix mixed encodings did not fail closed")
		}
	}
}
