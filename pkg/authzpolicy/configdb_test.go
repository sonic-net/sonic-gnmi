package authzpolicy

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestRedisTableReader(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { client.Close() })

	ctx := context.Background()
	if err := client.HSet(ctx, "GRPC_AUTHZ_PRINCIPAL:client:one", "roles@", "reader").Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, "GRPC_AUTHZ_PRINCIPAL:client-two", "roles@", "writer").Err(); err != nil {
		t.Fatal(err)
	}

	rows, err := NewRedisTableReader(client, ":").ReadTable(ctx, PrincipalTable)
	if err != nil {
		t.Fatalf("ReadTable() failed: %v", err)
	}
	want := map[string]map[string]string{
		"client:one": {"roles@": "reader"},
		"client-two": {"roles@": "writer"},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("ReadTable() = %#v, want %#v", rows, want)
	}
}

func TestCompileConfigDBPolicy(t *testing.T) {
	principals := map[string]map[string]string{
		"writer.example.com": {"roles@": "writer,reader"},
		"reader.example.com": {"roles@": "reader"},
	}
	rules := map[string]map[string]string{
		"write": {"roles@": "writer", "rpc": "/gnmi.gNMI/Set", "effect": "allow"},
		"read":  {"roles@": "reader", "rpc": "/gnmi.gNMI/Get", "effect": "allow"},
		"deny":  {"roles@": "reader", "rpc": "/gnoi.file.File/Put", "effect": "deny"},
	}

	first, err := CompileConfigDBPolicy(principals, rules)
	if err != nil {
		t.Fatalf("CompileConfigDBPolicy() failed: %v", err)
	}
	second, err := CompileConfigDBPolicy(principals, rules)
	if err != nil {
		t.Fatalf("CompileConfigDBPolicy() second call failed: %v", err)
	}
	if first != second {
		t.Fatalf("compiled policy is not deterministic:\n%s\n%s", first, second)
	}

	var policy a43Policy
	if err := json.Unmarshal([]byte(first), &policy); err != nil {
		t.Fatalf("compiled policy is invalid JSON: %v", err)
	}
	if len(policy.DenyRules) != 1 || policy.DenyRules[0].Name != "deny" {
		t.Fatalf("deny rules = %#v, want one rule named deny", policy.DenyRules)
	}
	if got, want := policy.AllowRules[0].Name, "read"; got != want {
		t.Fatalf("first allow rule = %q, want %q", got, want)
	}
	if got, want := policy.AllowRules[0].Source.Principals, []string{"reader.example.com", "writer.example.com"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("read principals = %#v, want %#v", got, want)
	}
}

func TestCompileConfigDBPolicyRejectsInvalidRows(t *testing.T) {
	validPrincipals := map[string]map[string]string{
		"client.example.com": {"roles@": "reader"},
	}
	validRules := map[string]map[string]string{
		"read": {"roles@": "reader", "rpc": "/gnmi.gNMI/Get", "effect": "allow"},
	}
	tests := []struct {
		name       string
		principals map[string]map[string]string
		rules      map[string]map[string]string
		want       string
	}{
		{name: "missing principals", principals: nil, rules: validRules, want: "GRPC_AUTHZ_PRINCIPAL is empty"},
		{name: "wildcard principal", principals: map[string]map[string]string{"*.example.com": {"roles@": "reader"}}, rules: validRules, want: "literal A43 principal"},
		{name: "principal whitespace", principals: map[string]map[string]string{" client.example.com": {"roles@": "reader"}}, rules: validRules, want: "surrounding whitespace"},
		{name: "role whitespace", principals: map[string]map[string]string{"client.example.com": {"roles@": "reader, writer"}}, rules: validRules, want: "noncanonical value"},
		{name: "unknown role", principals: validPrincipals, rules: map[string]map[string]string{"read": {"roles@": "writer", "rpc": "/gnmi.gNMI/Get", "effect": "allow"}}, want: "unknown role"},
		{name: "invalid method", principals: validPrincipals, rules: map[string]map[string]string{"read": {"roles@": "reader", "rpc": "gnmi.gNMI.Get", "effect": "allow"}}, want: "invalid exact RPC method"},
		{name: "unknown principal field", principals: map[string]map[string]string{"client.example.com": {"roles@": "reader", "extra": "value"}}, rules: validRules, want: "unknown field"},
		{name: "unknown rule field", principals: validPrincipals, rules: map[string]map[string]string{"read": {"roles@": "reader", "rpc": "/gnmi.gNMI/Get", "effect": "allow", "extra": "value"}}, want: "unknown field"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := CompileConfigDBPolicy(test.principals, test.rules)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("CompileConfigDBPolicy() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestCompileConfigDBPolicyRejectsDenyOnlyPolicy(t *testing.T) {
	_, err := NewConfigDBSource(staticTableReader{tables: map[string]map[string]map[string]string{
		PrincipalTable: {
			"client.example.com": {"roles@": "reader"},
		},
		RuleTable: {
			"deny": {"roles@": "reader", "rpc": "/gnmi.gNMI/Get", "effect": "deny"},
		},
	}}).Load(context.Background())
	if err == nil || !strings.Contains(err.Error(), `"allow_rules" is not present`) {
		t.Fatalf("Load() error = %v, want missing allow_rules", err)
	}
}

type staticTableReader struct {
	tables map[string]map[string]map[string]string
}

func (r staticTableReader) ReadTable(_ context.Context, table string) (map[string]map[string]string, error) {
	return r.tables[table], nil
}

func TestConfigDBSourceLoad(t *testing.T) {
	source := NewConfigDBSource(staticTableReader{tables: map[string]map[string]map[string]string{
		PrincipalTable: {
			"client.example.com": {"roles@": "reader"},
		},
		RuleTable: {
			"read": {"roles@": "reader", "rpc": "/gnmi.gNMI/Get", "effect": "allow"},
		},
	}})

	interceptor, err := source.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if interceptor.UnaryInterceptor() == nil || interceptor.StreamInterceptor() == nil {
		t.Fatal("Load() returned incomplete interceptor")
	}
}
