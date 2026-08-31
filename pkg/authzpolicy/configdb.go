package authzpolicy

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/authz"
)

const (
	PrincipalTable = "GRPC_AUTHZ_PRINCIPAL"
	RuleTable      = "GRPC_AUTHZ_RULE"
	rolesField     = "roles@"
)

var fullMethodPattern = regexp.MustCompile(`^/([A-Za-z_][A-Za-z0-9_]*\.)*[A-Za-z_][A-Za-z0-9_]*/[A-Za-z_][A-Za-z0-9_]*$`)

// TableReader returns every row in one CONFIG_DB table.
type TableReader interface {
	ReadTable(context.Context, string) (map[string]map[string]string, error)
}

// RedisClient contains the Redis operations needed to read policy tables.
type RedisClient interface {
	Scan(context.Context, uint64, string, int64) *redis.ScanCmd
	HGetAll(context.Context, string) *redis.MapStringStringCmd
}

type RedisTableReader struct {
	client    RedisClient
	separator string
}

// NewRedisTableReader creates a CONFIG_DB table reader.
func NewRedisTableReader(client RedisClient, separator string) *RedisTableReader {
	return &RedisTableReader{client: client, separator: separator}
}

func (r *RedisTableReader) ReadTable(ctx context.Context, table string) (map[string]map[string]string, error) {
	prefix := table + r.separator
	pattern := prefix + "*"
	rows := make(map[string]map[string]string)
	var cursor uint64
	for {
		keys, next, err := r.client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return nil, fmt.Errorf("scan %s: %w", table, err)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if !strings.HasPrefix(key, prefix) {
				return nil, fmt.Errorf("CONFIG_DB key %q is outside table %s", key, table)
			}
			name := strings.TrimPrefix(key, prefix)
			if name == "" {
				return nil, fmt.Errorf("CONFIG_DB table %s contains an empty row name", table)
			}
			fields, err := r.client.HGetAll(ctx, key).Result()
			if err != nil {
				return nil, fmt.Errorf("read CONFIG_DB key %s: %w", key, err)
			}
			if len(fields) == 0 {
				return nil, fmt.Errorf("CONFIG_DB key %s disappeared while loading policy", key)
			}
			rows[name] = fields
		}
		cursor = next
		if cursor == 0 {
			return rows, nil
		}
	}
}

// ConfigDBSource compiles normalized CONFIG_DB rows into grpc-go A43 policy.
type ConfigDBSource struct {
	reader TableReader
}

func NewConfigDBSource(reader TableReader) *ConfigDBSource {
	return &ConfigDBSource{reader: reader}
}

func (s *ConfigDBSource) Load(ctx context.Context) (*PolicyInterceptor, error) {
	principals, err := s.reader.ReadTable(ctx, PrincipalTable)
	if err != nil {
		return nil, err
	}
	rules, err := s.reader.ReadTable(ctx, RuleTable)
	if err != nil {
		return nil, err
	}
	policy, err := CompileConfigDBPolicy(principals, rules)
	if err != nil {
		return nil, err
	}
	interceptor, err := authz.NewStatic(policy)
	if err != nil {
		return nil, fmt.Errorf("validate compiled CONFIG_DB authorization policy: %w", err)
	}
	return newPolicyInterceptor(interceptor.UnaryInterceptor, interceptor.StreamInterceptor, nil), nil
}

type a43Policy struct {
	Name       string    `json:"name"`
	DenyRules  []a43Rule `json:"deny_rules"`
	AllowRules []a43Rule `json:"allow_rules"`
}

type a43Rule struct {
	Name    string     `json:"name"`
	Source  a43Source  `json:"source"`
	Request a43Request `json:"request"`
}

type a43Source struct {
	Principals []string `json:"principals"`
}

type a43Request struct {
	Paths []string `json:"paths"`
}

// CompileConfigDBPolicy converts normalized CONFIG_DB rows to deterministic A43 JSON.
func CompileConfigDBPolicy(principals, rules map[string]map[string]string) (string, error) {
	if len(principals) == 0 {
		return "", fmt.Errorf("CONFIG_DB table %s is empty", PrincipalTable)
	}
	if len(rules) == 0 {
		return "", fmt.Errorf("CONFIG_DB table %s is empty", RuleTable)
	}

	rolePrincipals := make(map[string]map[string]struct{})
	principalNames := sortedKeys(principals)
	for _, principal := range principalNames {
		if principal == "" {
			return "", fmt.Errorf("%s contains an empty principal", PrincipalTable)
		}
		if strings.TrimSpace(principal) != principal {
			return "", fmt.Errorf("principal %q must not have surrounding whitespace", principal)
		}
		if strings.HasPrefix(principal, "*") || strings.HasSuffix(principal, "*") {
			return "", fmt.Errorf("principal %q must be a literal A43 principal", principal)
		}
		row := principals[principal]
		if err := rejectUnknownFields(PrincipalTable, principal, row, rolesField); err != nil {
			return "", err
		}
		roles, err := parseListField(PrincipalTable, principal, row, rolesField)
		if err != nil {
			return "", err
		}
		for _, role := range roles {
			if rolePrincipals[role] == nil {
				rolePrincipals[role] = make(map[string]struct{})
			}
			rolePrincipals[role][principal] = struct{}{}
		}
	}

	policy := a43Policy{
		Name:       "sonic-config-db",
		DenyRules:  make([]a43Rule, 0),
		AllowRules: make([]a43Rule, 0),
	}
	for _, name := range sortedKeys(rules) {
		if name == "" {
			return "", fmt.Errorf("%s contains an empty rule name", RuleTable)
		}
		if strings.TrimSpace(name) != name {
			return "", fmt.Errorf("rule name %q must not have surrounding whitespace", name)
		}
		row := rules[name]
		if err := rejectUnknownFields(RuleTable, name, row, rolesField, "rpc", "effect"); err != nil {
			return "", err
		}
		roles, err := parseListField(RuleTable, name, row, rolesField)
		if err != nil {
			return "", err
		}
		principalSet := make(map[string]struct{})
		for _, role := range roles {
			members, ok := rolePrincipals[role]
			if !ok {
				return "", fmt.Errorf("%s row %q references unknown role %q", RuleTable, name, role)
			}
			for principal := range members {
				principalSet[principal] = struct{}{}
			}
		}
		rpc := strings.TrimSpace(row["rpc"])
		if !fullMethodPattern.MatchString(rpc) {
			return "", fmt.Errorf("%s row %q has invalid exact RPC method %q", RuleTable, name, rpc)
		}
		rule := a43Rule{
			Name:    name,
			Source:  a43Source{Principals: sortedSet(principalSet)},
			Request: a43Request{Paths: []string{rpc}},
		}
		switch row["effect"] {
		case "allow":
			policy.AllowRules = append(policy.AllowRules, rule)
		case "deny":
			policy.DenyRules = append(policy.DenyRules, rule)
		default:
			return "", fmt.Errorf("%s row %q has invalid effect %q", RuleTable, name, row["effect"])
		}
	}

	contents, err := json.Marshal(policy)
	if err != nil {
		return "", fmt.Errorf("marshal CONFIG_DB authorization policy: %w", err)
	}
	return string(contents), nil
}

func parseListField(table, name string, row map[string]string, field string) ([]string, error) {
	raw, ok := row[field]
	if !ok || raw == "" {
		return nil, fmt.Errorf("%s row %q is missing %s", table, name, field)
	}
	values := make(map[string]struct{})
	for _, value := range strings.Split(raw, ",") {
		if value == "" {
			return nil, fmt.Errorf("%s row %q has an empty value in %s", table, name, field)
		}
		if strings.TrimSpace(value) != value {
			return nil, fmt.Errorf("%s row %q has noncanonical value %q in %s", table, name, value, field)
		}
		values[value] = struct{}{}
	}
	return sortedSet(values), nil
}

func rejectUnknownFields(table, name string, row map[string]string, allowed ...string) error {
	known := make(map[string]struct{}, len(allowed))
	for _, field := range allowed {
		known[field] = struct{}{}
	}
	for field := range row {
		if _, ok := known[field]; !ok {
			return fmt.Errorf("%s row %q contains unknown field %q", table, name, field)
		}
	}
	return nil
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedSet(values map[string]struct{}) []string {
	return sortedKeys(values)
}
