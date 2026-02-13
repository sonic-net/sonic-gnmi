//go:build !gnmi_memcheck
// +build !gnmi_memcheck

package bypass

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc/metadata"
)

func TestHasBypassHeader(t *testing.T) {
	tests := []struct {
		name     string
		ctx      context.Context
		expected bool
	}{
		{
			name:     "nil context",
			ctx:      nil,
			expected: false,
		},
		{
			name:     "context without metadata",
			ctx:      context.Background(),
			expected: false,
		},
		{
			name: "context with bypass=true",
			ctx: metadata.NewIncomingContext(
				context.Background(),
				metadata.Pairs(MetadataKeyBypassValidation, "true"),
			),
			expected: true,
		},
		{
			name: "context with bypass=false",
			ctx: metadata.NewIncomingContext(
				context.Background(),
				metadata.Pairs(MetadataKeyBypassValidation, "false"),
			),
			expected: false,
		},
		{
			name: "context with bypass=TRUE (case sensitive)",
			ctx: metadata.NewIncomingContext(
				context.Background(),
				metadata.Pairs(MetadataKeyBypassValidation, "TRUE"),
			),
			expected: false,
		},
		{
			name: "context with other metadata but no bypass",
			ctx: metadata.NewIncomingContext(
				context.Background(),
				metadata.Pairs("other-key", "other-value"),
			),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := hasBypassHeader(tt.ctx)
			if result != tt.expected {
				t.Errorf("hasBypassHeader() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestCheckSKU(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	originalFunc := getConfigDbClientFunc
	defer func() { getConfigDbClientFunc = originalFunc }()

	getConfigDbClientFunc = func() (*redis.Client, error) {
		return redis.NewClient(&redis.Options{
			Addr: mr.Addr(),
			DB:   0,
		}), nil
	}

	tests := []struct {
		name     string
		hwsku    string
		expected bool
	}{
		{
			name:     "Cisco 8102 SKU",
			hwsku:    "Cisco-8102-28FH-DPU-C28",
			expected: true,
		},
		{
			name:     "Cisco 8101 SKU",
			hwsku:    "Cisco-8101-O32",
			expected: true,
		},
		{
			name:     "Cisco 8223 SKU",
			hwsku:    "Cisco-8223-something",
			expected: true,
		},
		{
			name:     "Non-matching SKU",
			hwsku:    "Force10-Z9100-C32",
			expected: false,
		},
		{
			name:     "Partial match not at prefix",
			hwsku:    "Something-Cisco-8102",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mr.FlushAll()
			mr.HSet("DEVICE_METADATA|localhost", "hwsku", tt.hwsku)

			result := checkSKU()
			if result != tt.expected {
				t.Errorf("checkSKU() = %v, want %v for SKU %q", result, tt.expected, tt.hwsku)
			}
		})
	}
}

func TestCheckSKU_NoMetadata(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	originalFunc := getConfigDbClientFunc
	defer func() { getConfigDbClientFunc = originalFunc }()

	getConfigDbClientFunc = func() (*redis.Client, error) {
		return redis.NewClient(&redis.Options{
			Addr: mr.Addr(),
			DB:   0,
		}), nil
	}

	// Don't set any data - should return false
	result := checkSKU()
	if result != false {
		t.Errorf("checkSKU() = %v, want false when DEVICE_METADATA is missing", result)
	}
}

func TestCheckAllowedTables(t *testing.T) {
	tests := []struct {
		name     string
		tables   []string
		expected bool
	}{
		{
			name:     "single allowed table VNET",
			tables:   []string{"VNET"},
			expected: true,
		},
		{
			name:     "single allowed table ACL_RULE",
			tables:   []string{"ACL_RULE"},
			expected: true,
		},
		{
			name:     "multiple allowed tables",
			tables:   []string{"VNET", "VNET_ROUTE_TUNNEL", "BGP_PEER_RANGE"},
			expected: true,
		},
		{
			name:     "disallowed table",
			tables:   []string{"PORT"},
			expected: false,
		},
		{
			name:     "mixed allowed and disallowed",
			tables:   []string{"VNET", "PORT"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var updates []*gnmipb.Update
			for _, table := range tt.tables {
				updates = append(updates, &gnmipb.Update{
					Path: &gnmipb.Path{
						Elem: []*gnmipb.PathElem{
							{Name: "CONFIG_DB"},
							{Name: "localhost"},
							{Name: table},
							{Name: "key1"},
						},
					},
				})
			}

			result := checkAllowedTables(nil, updates)
			if result != tt.expected {
				t.Errorf("checkAllowedTables() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestCheckAllowedDeletePaths(t *testing.T) {
	tests := []struct {
		name     string
		tables   []string
		expected bool
	}{
		{
			name:     "single allowed table",
			tables:   []string{"VNET"},
			expected: true,
		},
		{
			name:     "disallowed table",
			tables:   []string{"INTERFACE"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var deletes []*gnmipb.Path
			for _, table := range tt.tables {
				deletes = append(deletes, &gnmipb.Path{
					Elem: []*gnmipb.PathElem{
						{Name: "CONFIG_DB"},
						{Name: "localhost"},
						{Name: table},
						{Name: "key1"},
					},
				})
			}

			result := checkAllowedDeletePaths(nil, deletes)
			if result != tt.expected {
				t.Errorf("checkAllowedDeletePaths() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestExtractTable(t *testing.T) {
	tests := []struct {
		name     string
		path     *gnmipb.Path
		expected string
	}{
		{
			name: "full path with CONFIG_DB/localhost",
			path: &gnmipb.Path{
				Elem: []*gnmipb.PathElem{
					{Name: "CONFIG_DB"},
					{Name: "localhost"},
					{Name: "VNET"},
					{Name: "vnet1"},
				},
			},
			expected: "VNET",
		},
		{
			name: "path without CONFIG_DB prefix",
			path: &gnmipb.Path{
				Elem: []*gnmipb.PathElem{
					{Name: "VNET"},
					{Name: "vnet1"},
				},
			},
			expected: "VNET",
		},
		{
			name: "table only path",
			path: &gnmipb.Path{
				Elem: []*gnmipb.PathElem{
					{Name: "CONFIG_DB"},
					{Name: "localhost"},
					{Name: "VNET_ROUTE_TUNNEL"},
				},
			},
			expected: "VNET_ROUTE_TUNNEL",
		},
		{
			name:     "empty path",
			path:     &gnmipb.Path{},
			expected: "",
		},
		{
			name:     "nil path",
			path:     nil,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractTable(nil, tt.path)
			if result != tt.expected {
				t.Errorf("extractTable() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestParsePath(t *testing.T) {
	tests := []struct {
		name          string
		path          *gnmipb.Path
		expectedTable string
		expectedKey   string
		expectedField string
	}{
		{
			name: "TABLE/KEY path",
			path: &gnmipb.Path{
				Elem: []*gnmipb.PathElem{
					{Name: "CONFIG_DB"},
					{Name: "localhost"},
					{Name: "VNET"},
					{Name: "vnet1"},
				},
			},
			expectedTable: "VNET",
			expectedKey:   "vnet1",
			expectedField: "",
		},
		{
			name: "TABLE/KEY/FIELD path",
			path: &gnmipb.Path{
				Elem: []*gnmipb.PathElem{
					{Name: "CONFIG_DB"},
					{Name: "localhost"},
					{Name: "VNET"},
					{Name: "vnet1"},
					{Name: "vni"},
				},
			},
			expectedTable: "VNET",
			expectedKey:   "vnet1",
			expectedField: "vni",
		},
		{
			name: "TABLE only path (bulk)",
			path: &gnmipb.Path{
				Elem: []*gnmipb.PathElem{
					{Name: "CONFIG_DB"},
					{Name: "localhost"},
					{Name: "VNET_ROUTE_TUNNEL"},
				},
			},
			expectedTable: "VNET_ROUTE_TUNNEL",
			expectedKey:   "",
			expectedField: "",
		},
		{
			name: "key with JSON pointer encoding",
			path: &gnmipb.Path{
				Elem: []*gnmipb.PathElem{
					{Name: "CONFIG_DB"},
					{Name: "localhost"},
					{Name: "VNET_ROUTE_TUNNEL"},
					{Name: "Vnet1|10.0.0.1~132"},
				},
			},
			expectedTable: "VNET_ROUTE_TUNNEL",
			expectedKey:   "Vnet1|10.0.0.1/32",
			expectedField: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			table, key, field := parsePath(nil, tt.path)
			if table != tt.expectedTable {
				t.Errorf("parsePath() table = %v, want %v", table, tt.expectedTable)
			}
			if key != tt.expectedKey {
				t.Errorf("parsePath() key = %v, want %v", key, tt.expectedKey)
			}
			if field != tt.expectedField {
				t.Errorf("parsePath() field = %v, want %v", field, tt.expectedField)
			}
		})
	}
}

func TestDecodeJsonPointer(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "decode ~1 to /",
			input:    "10.0.0.1~132",
			expected: "10.0.0.1/32",
		},
		{
			name:     "decode ~0 to ~",
			input:    "test~0value",
			expected: "test~value",
		},
		{
			name:     "no encoding",
			input:    "simple-key",
			expected: "simple-key",
		},
		{
			name:     "multiple ~1",
			input:    "a~1b~1c",
			expected: "a/b/c",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := decodeJsonPointer(tt.input)
			if result != tt.expected {
				t.Errorf("decodeJsonPointer(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestApply(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	originalFunc := getConfigDbClientFunc
	defer func() { getConfigDbClientFunc = originalFunc }()

	getConfigDbClientFunc = func() (*redis.Client, error) {
		return redis.NewClient(&redis.Options{
			Addr: mr.Addr(),
			DB:   0,
		}), nil
	}

	t.Run("single entry with JSON value", func(t *testing.T) {
		mr.FlushAll()
		updates := []*gnmipb.Update{
			{
				Path: &gnmipb.Path{
					Elem: []*gnmipb.PathElem{
						{Name: "CONFIG_DB"},
						{Name: "localhost"},
						{Name: "VNET"},
						{Name: "vnet1"},
					},
				},
				Val: &gnmipb.TypedValue{
					Value: &gnmipb.TypedValue_JsonIetfVal{
						JsonIetfVal: []byte(`{"vni": "1000", "scope": "default"}`),
					},
				},
			},
		}

		err := Apply(context.Background(), nil, updates)
		if err != nil {
			t.Errorf("Apply() error = %v", err)
		}

		vni := mr.HGet("VNET|vnet1", "vni")
		if vni != "1000" {
			t.Errorf("Expected vni=1000, got %s", vni)
		}
		scope := mr.HGet("VNET|vnet1", "scope")
		if scope != "default" {
			t.Errorf("Expected scope=default, got %s", scope)
		}
	})

	t.Run("bulk table update", func(t *testing.T) {
		mr.FlushAll()
		updates := []*gnmipb.Update{
			{
				Path: &gnmipb.Path{
					Elem: []*gnmipb.PathElem{
						{Name: "CONFIG_DB"},
						{Name: "localhost"},
						{Name: "VNET_ROUTE_TUNNEL"},
					},
				},
				Val: &gnmipb.TypedValue{
					Value: &gnmipb.TypedValue_JsonIetfVal{
						JsonIetfVal: []byte(`{"Vnet1|10.0.0.1/32": {"endpoint": "1.1.1.1", "vni": "1000"}, "Vnet2|10.0.0.2/32": {"endpoint": "2.2.2.2", "vni": "2000"}}`),
					},
				},
			},
		}

		err := Apply(context.Background(), nil, updates)
		if err != nil {
			t.Errorf("Apply() error = %v", err)
		}

		endpoint1 := mr.HGet("VNET_ROUTE_TUNNEL|Vnet1|10.0.0.1/32", "endpoint")
		if endpoint1 != "1.1.1.1" {
			t.Errorf("Expected endpoint=1.1.1.1, got %s", endpoint1)
		}
		endpoint2 := mr.HGet("VNET_ROUTE_TUNNEL|Vnet2|10.0.0.2/32", "endpoint")
		if endpoint2 != "2.2.2.2" {
			t.Errorf("Expected endpoint=2.2.2.2, got %s", endpoint2)
		}
	})

	t.Run("invalid path - no table", func(t *testing.T) {
		updates := []*gnmipb.Update{
			{
				Path: &gnmipb.Path{
					Elem: []*gnmipb.PathElem{
						{Name: "CONFIG_DB"},
						{Name: "localhost"},
					},
				},
				Val: &gnmipb.TypedValue{
					Value: &gnmipb.TypedValue_JsonIetfVal{
						JsonIetfVal: []byte(`{"key": "value"}`),
					},
				},
			},
		}

		err := Apply(context.Background(), nil, updates)
		if err == nil {
			t.Error("Expected error for invalid path")
		}
	})

	t.Run("bulk update without JSON", func(t *testing.T) {
		updates := []*gnmipb.Update{
			{
				Path: &gnmipb.Path{
					Elem: []*gnmipb.PathElem{
						{Name: "CONFIG_DB"},
						{Name: "localhost"},
						{Name: "VNET"},
					},
				},
				Val: &gnmipb.TypedValue{},
			},
		}

		err := Apply(context.Background(), nil, updates)
		if err == nil {
			t.Error("Expected error for bulk update without JSON")
		}
	})

	t.Run("empty JSON value - single entry", func(t *testing.T) {
		mr.FlushAll()
		updates := []*gnmipb.Update{
			{
				Path: &gnmipb.Path{
					Elem: []*gnmipb.PathElem{
						{Name: "CONFIG_DB"},
						{Name: "localhost"},
						{Name: "VNET"},
						{Name: "vnet1"},
					},
				},
				Val: &gnmipb.TypedValue{
					Value: &gnmipb.TypedValue_JsonIetfVal{
						JsonIetfVal: []byte(`{}`),
					},
				},
			},
		}

		err := Apply(context.Background(), nil, updates)
		if err != nil {
			t.Errorf("Apply() error = %v, expected nil for empty JSON", err)
		}

		// Key should exist with NULL placeholder (SONiC convention)
		if !mr.Exists("VNET|vnet1") {
			t.Error("Key should exist with NULL placeholder")
		}
		nullVal := mr.HGet("VNET|vnet1", "NULL")
		if nullVal != "NULL" {
			t.Errorf("Expected NULL=NULL, got %s", nullVal)
		}
	})

	t.Run("empty JSON value - bulk update", func(t *testing.T) {
		mr.FlushAll()
		updates := []*gnmipb.Update{
			{
				Path: &gnmipb.Path{
					Elem: []*gnmipb.PathElem{
						{Name: "CONFIG_DB"},
						{Name: "localhost"},
						{Name: "VNET"},
					},
				},
				Val: &gnmipb.TypedValue{
					Value: &gnmipb.TypedValue_JsonIetfVal{
						JsonIetfVal: []byte(`{"vnet1": {}, "vnet2": {"vni": "2000"}}`),
					},
				},
			},
		}

		err := Apply(context.Background(), nil, updates)
		if err != nil {
			t.Errorf("Apply() error = %v", err)
		}

		// vnet1 should exist with NULL placeholder
		if !mr.Exists("VNET|vnet1") {
			t.Error("VNET|vnet1 should exist with NULL placeholder")
		}
		nullVal := mr.HGet("VNET|vnet1", "NULL")
		if nullVal != "NULL" {
			t.Errorf("Expected NULL=NULL for vnet1, got %s", nullVal)
		}
		// vnet2 should exist with its field
		vni := mr.HGet("VNET|vnet2", "vni")
		if vni != "2000" {
			t.Errorf("Expected vni=2000 for vnet2, got %s", vni)
		}
	})
}

func TestDelete(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	originalFunc := getConfigDbClientFunc
	defer func() { getConfigDbClientFunc = originalFunc }()

	getConfigDbClientFunc = func() (*redis.Client, error) {
		return redis.NewClient(&redis.Options{
			Addr: mr.Addr(),
			DB:   0,
		}), nil
	}

	t.Run("delete existing key", func(t *testing.T) {
		mr.FlushAll()
		mr.HSet("VNET|vnet1", "vni", "1000")

		deletes := []*gnmipb.Path{
			{
				Elem: []*gnmipb.PathElem{
					{Name: "CONFIG_DB"},
					{Name: "localhost"},
					{Name: "VNET"},
					{Name: "vnet1"},
				},
			},
		}

		err := Delete(context.Background(), nil, deletes)
		if err != nil {
			t.Errorf("Delete() error = %v", err)
		}

		if mr.Exists("VNET|vnet1") {
			t.Error("Expected key to be deleted")
		}
	})

	t.Run("delete with invalid path - no key", func(t *testing.T) {
		deletes := []*gnmipb.Path{
			{
				Elem: []*gnmipb.PathElem{
					{Name: "CONFIG_DB"},
					{Name: "localhost"},
					{Name: "VNET"},
				},
			},
		}

		err := Delete(context.Background(), nil, deletes)
		if err == nil {
			t.Error("Expected error for invalid delete path")
		}
	})
}

func TestShouldBypass(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	originalFunc := getConfigDbClientFunc
	defer func() { getConfigDbClientFunc = originalFunc }()

	getConfigDbClientFunc = func() (*redis.Client, error) {
		return redis.NewClient(&redis.Options{
			Addr: mr.Addr(),
			DB:   0,
		}), nil
	}

	// Set up allowed SKU
	mr.HSet("DEVICE_METADATA|localhost", "hwsku", "Cisco-8102-test")

	t.Run("all conditions met", func(t *testing.T) {
		ctx := metadata.NewIncomingContext(
			context.Background(),
			metadata.Pairs(MetadataKeyBypassValidation, "true"),
		)
		updates := []*gnmipb.Update{
			{
				Path: &gnmipb.Path{
					Elem: []*gnmipb.PathElem{
						{Name: "CONFIG_DB"},
						{Name: "localhost"},
						{Name: "VNET"},
						{Name: "vnet1"},
					},
				},
			},
		}

		result := ShouldBypass(ctx, nil, updates)
		if !result {
			t.Error("ShouldBypass() should return true when all conditions met")
		}
	})

	t.Run("no bypass header", func(t *testing.T) {
		ctx := context.Background()
		updates := []*gnmipb.Update{
			{
				Path: &gnmipb.Path{
					Elem: []*gnmipb.PathElem{
						{Name: "VNET"},
						{Name: "vnet1"},
					},
				},
			},
		}

		result := ShouldBypass(ctx, nil, updates)
		if result {
			t.Error("ShouldBypass() should return false without bypass header")
		}
	})

	t.Run("disallowed table", func(t *testing.T) {
		ctx := metadata.NewIncomingContext(
			context.Background(),
			metadata.Pairs(MetadataKeyBypassValidation, "true"),
		)
		updates := []*gnmipb.Update{
			{
				Path: &gnmipb.Path{
					Elem: []*gnmipb.PathElem{
						{Name: "PORT"},
						{Name: "Ethernet0"},
					},
				},
			},
		}

		result := ShouldBypass(ctx, nil, updates)
		if result {
			t.Error("ShouldBypass() should return false for disallowed table")
		}
	})
}

func TestShouldBypassDelete(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	originalFunc := getConfigDbClientFunc
	defer func() { getConfigDbClientFunc = originalFunc }()

	getConfigDbClientFunc = func() (*redis.Client, error) {
		return redis.NewClient(&redis.Options{
			Addr: mr.Addr(),
			DB:   0,
		}), nil
	}

	// Set up allowed SKU
	mr.HSet("DEVICE_METADATA|localhost", "hwsku", "Cisco-8102-test")

	t.Run("all conditions met", func(t *testing.T) {
		ctx := metadata.NewIncomingContext(
			context.Background(),
			metadata.Pairs(MetadataKeyBypassValidation, "true"),
		)
		deletes := []*gnmipb.Path{
			{
				Elem: []*gnmipb.PathElem{
					{Name: "CONFIG_DB"},
					{Name: "localhost"},
					{Name: "VNET"},
					{Name: "vnet1"},
				},
			},
		}

		result := ShouldBypassDelete(ctx, nil, deletes)
		if !result {
			t.Error("ShouldBypassDelete() should return true when all conditions met")
		}
	})

	t.Run("disallowed table", func(t *testing.T) {
		ctx := metadata.NewIncomingContext(
			context.Background(),
			metadata.Pairs(MetadataKeyBypassValidation, "true"),
		)
		deletes := []*gnmipb.Path{
			{
				Elem: []*gnmipb.PathElem{
					{Name: "INTERFACE"},
					{Name: "Ethernet0"},
				},
			},
		}

		result := ShouldBypassDelete(ctx, nil, deletes)
		if result {
			t.Error("ShouldBypassDelete() should return false for disallowed table")
		}
	})
}

func TestAllowedTables(t *testing.T) {
	expectedTables := []string{"VNET", "VNET_ROUTE_TUNNEL", "VLAN_SUB_INTERFACE", "ACL_RULE", "BGP_PEER_RANGE"}

	for _, table := range expectedTables {
		if !AllowedTables[table] {
			t.Errorf("Expected %s to be in AllowedTables", table)
		}
	}

	disallowedTables := []string{"PORT", "INTERFACE", "VLAN", "LOOPBACK_INTERFACE"}
	for _, table := range disallowedTables {
		if AllowedTables[table] {
			t.Errorf("Expected %s to NOT be in AllowedTables", table)
		}
	}
}

func TestAllowedSKUPrefixes(t *testing.T) {
	expectedPrefixes := []string{"Cisco-8102", "Cisco-8101", "Cisco-8223"}

	if len(AllowedSKUPrefixes) != len(expectedPrefixes) {
		t.Errorf("Expected %d SKU prefixes, got %d", len(expectedPrefixes), len(AllowedSKUPrefixes))
	}

	for i, prefix := range expectedPrefixes {
		if AllowedSKUPrefixes[i] != prefix {
			t.Errorf("Expected prefix %s at index %d, got %s", prefix, i, AllowedSKUPrefixes[i])
		}
	}
}

func TestApplyScalarField(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	originalFunc := getConfigDbClientFunc
	defer func() { getConfigDbClientFunc = originalFunc }()

	getConfigDbClientFunc = func() (*redis.Client, error) {
		return redis.NewClient(&redis.Options{
			Addr: mr.Addr(),
			DB:   0,
		}), nil
	}

	t.Run("scalar string field update", func(t *testing.T) {
		mr.FlushAll()
		updates := []*gnmipb.Update{
			{
				Path: &gnmipb.Path{
					Elem: []*gnmipb.PathElem{
						{Name: "CONFIG_DB"},
						{Name: "localhost"},
						{Name: "VNET"},
						{Name: "vnet1"},
						{Name: "vni"},
					},
				},
				Val: &gnmipb.TypedValue{
					Value: &gnmipb.TypedValue_StringVal{
						StringVal: "5000",
					},
				},
			},
		}

		err := Apply(context.Background(), nil, updates)
		if err != nil {
			t.Errorf("Apply() error = %v", err)
		}

		vni := mr.HGet("VNET|vnet1", "vni")
		if vni != "5000" {
			t.Errorf("Expected vni=5000, got %s", vni)
		}
	})

	t.Run("scalar int field update", func(t *testing.T) {
		mr.FlushAll()
		updates := []*gnmipb.Update{
			{
				Path: &gnmipb.Path{
					Elem: []*gnmipb.PathElem{
						{Name: "CONFIG_DB"},
						{Name: "localhost"},
						{Name: "VNET"},
						{Name: "vnet1"},
						{Name: "priority"},
					},
				},
				Val: &gnmipb.TypedValue{
					Value: &gnmipb.TypedValue_IntVal{
						IntVal: 100,
					},
				},
			},
		}

		err := Apply(context.Background(), nil, updates)
		if err != nil {
			t.Errorf("Apply() error = %v", err)
		}

		priority := mr.HGet("VNET|vnet1", "priority")
		if priority != "100" {
			t.Errorf("Expected priority=100, got %s", priority)
		}
	})

	t.Run("scalar uint field update", func(t *testing.T) {
		mr.FlushAll()
		updates := []*gnmipb.Update{
			{
				Path: &gnmipb.Path{
					Elem: []*gnmipb.PathElem{
						{Name: "CONFIG_DB"},
						{Name: "localhost"},
						{Name: "VNET"},
						{Name: "vnet1"},
						{Name: "counter"},
					},
				},
				Val: &gnmipb.TypedValue{
					Value: &gnmipb.TypedValue_UintVal{
						UintVal: 999,
					},
				},
			},
		}

		err := Apply(context.Background(), nil, updates)
		if err != nil {
			t.Errorf("Apply() error = %v", err)
		}

		counter := mr.HGet("VNET|vnet1", "counter")
		if counter != "999" {
			t.Errorf("Expected counter=999, got %s", counter)
		}
	})
}

func TestApplyErrors(t *testing.T) {
	t.Run("client error", func(t *testing.T) {
		originalFunc := getConfigDbClientFunc
		defer func() { getConfigDbClientFunc = originalFunc }()

		getConfigDbClientFunc = func() (*redis.Client, error) {
			return nil, fmt.Errorf("connection refused")
		}

		updates := []*gnmipb.Update{
			{
				Path: &gnmipb.Path{
					Elem: []*gnmipb.PathElem{
						{Name: "VNET"},
						{Name: "vnet1"},
					},
				},
				Val: &gnmipb.TypedValue{
					Value: &gnmipb.TypedValue_JsonIetfVal{
						JsonIetfVal: []byte(`{"vni": "1000"}`),
					},
				},
			},
		}

		err := Apply(context.Background(), nil, updates)
		if err == nil {
			t.Error("Expected error for client failure")
		}
	})

	t.Run("invalid JSON in bulk update", func(t *testing.T) {
		mr := miniredis.RunT(t)
		defer mr.Close()

		originalFunc := getConfigDbClientFunc
		defer func() { getConfigDbClientFunc = originalFunc }()

		getConfigDbClientFunc = func() (*redis.Client, error) {
			return redis.NewClient(&redis.Options{
				Addr: mr.Addr(),
				DB:   0,
			}), nil
		}

		updates := []*gnmipb.Update{
			{
				Path: &gnmipb.Path{
					Elem: []*gnmipb.PathElem{
						{Name: "CONFIG_DB"},
						{Name: "localhost"},
						{Name: "VNET"},
					},
				},
				Val: &gnmipb.TypedValue{
					Value: &gnmipb.TypedValue_JsonIetfVal{
						JsonIetfVal: []byte(`{invalid json`),
					},
				},
			},
		}

		err := Apply(context.Background(), nil, updates)
		if err == nil {
			t.Error("Expected error for invalid JSON")
		}
	})

	t.Run("invalid JSON in single entry", func(t *testing.T) {
		mr := miniredis.RunT(t)
		defer mr.Close()

		originalFunc := getConfigDbClientFunc
		defer func() { getConfigDbClientFunc = originalFunc }()

		getConfigDbClientFunc = func() (*redis.Client, error) {
			return redis.NewClient(&redis.Options{
				Addr: mr.Addr(),
				DB:   0,
			}), nil
		}

		updates := []*gnmipb.Update{
			{
				Path: &gnmipb.Path{
					Elem: []*gnmipb.PathElem{
						{Name: "CONFIG_DB"},
						{Name: "localhost"},
						{Name: "VNET"},
						{Name: "vnet1"},
					},
				},
				Val: &gnmipb.TypedValue{
					Value: &gnmipb.TypedValue_JsonIetfVal{
						JsonIetfVal: []byte(`not valid json`),
					},
				},
			},
		}

		err := Apply(context.Background(), nil, updates)
		if err == nil {
			t.Error("Expected error for invalid JSON")
		}
	})
}

func TestDeleteErrors(t *testing.T) {
	t.Run("client error", func(t *testing.T) {
		originalFunc := getConfigDbClientFunc
		defer func() { getConfigDbClientFunc = originalFunc }()

		getConfigDbClientFunc = func() (*redis.Client, error) {
			return nil, fmt.Errorf("connection refused")
		}

		deletes := []*gnmipb.Path{
			{
				Elem: []*gnmipb.PathElem{
					{Name: "VNET"},
					{Name: "vnet1"},
				},
			},
		}

		err := Delete(context.Background(), nil, deletes)
		if err == nil {
			t.Error("Expected error for client failure")
		}
	})
}

func TestCheckSKUError(t *testing.T) {
	originalFunc := getConfigDbClientFunc
	defer func() { getConfigDbClientFunc = originalFunc }()

	getConfigDbClientFunc = func() (*redis.Client, error) {
		return nil, fmt.Errorf("connection refused")
	}

	result := checkSKU()
	if result {
		t.Error("checkSKU() should return false when client fails")
	}
}

func TestExtractTableWithPrefix(t *testing.T) {
	prefix := &gnmipb.Path{
		Elem: []*gnmipb.PathElem{
			{Name: "CONFIG_DB"},
			{Name: "localhost"},
		},
	}
	path := &gnmipb.Path{
		Elem: []*gnmipb.PathElem{
			{Name: "VNET"},
			{Name: "vnet1"},
		},
	}

	table := extractTable(prefix, path)
	if table != "VNET" {
		t.Errorf("Expected table=VNET, got %s", table)
	}
}

func TestCheckAllowedDeletePathsEmptyTable(t *testing.T) {
	deletes := []*gnmipb.Path{
		{
			Elem: []*gnmipb.PathElem{},
		},
	}

	result := checkAllowedDeletePaths(nil, deletes)
	if result {
		t.Error("checkAllowedDeletePaths() should return false for empty path")
	}
}

func TestCheckAllowedTablesEmptyTable(t *testing.T) {
	updates := []*gnmipb.Update{
		{
			Path: &gnmipb.Path{
				Elem: []*gnmipb.PathElem{},
			},
		},
	}

	result := checkAllowedTables(nil, updates)
	if result {
		t.Error("checkAllowedTables() should return false for empty path")
	}
}

func TestGetConfigDbClientDefault_EnvUnixSocket(t *testing.T) {
	// Save and restore original env
	originalAddr := os.Getenv("REDIS_ADDR")
	defer os.Setenv("REDIS_ADDR", originalAddr)

	// Set REDIS_ADDR to unix socket path
	os.Setenv("REDIS_ADDR", "/var/run/redis/test.sock")

	client, err := getConfigDbClientDefault()
	if err != nil {
		t.Errorf("getConfigDbClientDefault() error = %v", err)
	}
	if client == nil {
		t.Fatal("Expected non-nil client")
	}
	defer client.Close()

	// Client should be configured for unix socket
	opts := client.Options()
	if opts.Network != "unix" {
		t.Errorf("Expected network=unix, got %s", opts.Network)
	}
	if opts.Addr != "/var/run/redis/test.sock" {
		t.Errorf("Expected addr=/var/run/redis/test.sock, got %s", opts.Addr)
	}
	if opts.DB != configDbId {
		t.Errorf("Expected DB=%d, got %d", configDbId, opts.DB)
	}
}

func TestGetConfigDbClientDefault_EnvTcpAddress(t *testing.T) {
	// Save and restore original env
	originalAddr := os.Getenv("REDIS_ADDR")
	defer os.Setenv("REDIS_ADDR", originalAddr)

	// Set REDIS_ADDR to TCP address
	os.Setenv("REDIS_ADDR", "192.168.1.100:6380")

	client, err := getConfigDbClientDefault()
	if err != nil {
		t.Errorf("getConfigDbClientDefault() error = %v", err)
	}
	if client == nil {
		t.Fatal("Expected non-nil client")
	}
	defer client.Close()

	// Client should be configured for TCP
	opts := client.Options()
	if opts.Network != "tcp" {
		t.Errorf("Expected network=tcp, got %s", opts.Network)
	}
	if opts.Addr != "192.168.1.100:6380" {
		t.Errorf("Expected addr=192.168.1.100:6380, got %s", opts.Addr)
	}
	if opts.DB != configDbId {
		t.Errorf("Expected DB=%d, got %d", configDbId, opts.DB)
	}
}

func TestGetConfigDbClientDefault_FallbackToTcp(t *testing.T) {
	// Save and restore original env
	originalAddr := os.Getenv("REDIS_ADDR")
	defer os.Setenv("REDIS_ADDR", originalAddr)

	// Clear REDIS_ADDR to trigger auto-detection
	os.Unsetenv("REDIS_ADDR")

	// The default unix socket /var/run/redis/redis.sock typically doesn't exist in test env
	// so it should fall back to TCP
	client, err := getConfigDbClientDefault()
	if err != nil {
		t.Errorf("getConfigDbClientDefault() error = %v", err)
	}
	if client == nil {
		t.Fatal("Expected non-nil client")
	}
	defer client.Close()

	opts := client.Options()
	// Either unix or tcp is acceptable - depends on whether /var/run/redis/redis.sock exists
	if opts.Network != "unix" && opts.Network != "tcp" {
		t.Errorf("Expected network=unix or tcp, got %s", opts.Network)
	}
	if opts.DB != configDbId {
		t.Errorf("Expected DB=%d, got %d", configDbId, opts.DB)
	}
}

func TestGetConfigDbClientDefault_UnixSocketExists(t *testing.T) {
	// Save and restore original env
	originalAddr := os.Getenv("REDIS_ADDR")
	defer os.Setenv("REDIS_ADDR", originalAddr)

	// Clear REDIS_ADDR
	os.Unsetenv("REDIS_ADDR")

	// Create a temporary file to simulate the unix socket existing
	tmpDir := t.TempDir()
	tmpSocket := tmpDir + "/redis.sock"
	f, err := os.Create(tmpSocket)
	if err != nil {
		t.Fatalf("Failed to create temp socket file: %v", err)
	}
	f.Close()

	// Temporarily override the default socket path for testing
	// Since we can't easily override constants, we'll test the logic indirectly
	// by checking behavior with REDIS_ADDR set to a path that exists
	os.Setenv("REDIS_ADDR", tmpSocket)

	client, err := getConfigDbClientDefault()
	if err != nil {
		t.Errorf("getConfigDbClientDefault() error = %v", err)
	}
	if client == nil {
		t.Fatal("Expected non-nil client")
	}
	defer client.Close()

	opts := client.Options()
	if opts.Network != "unix" {
		t.Errorf("Expected network=unix for path starting with /, got %s", opts.Network)
	}
	if opts.Addr != tmpSocket {
		t.Errorf("Expected addr=%s, got %s", tmpSocket, opts.Addr)
	}
}

func TestGetConfigDbClientDefault_ConfigDbId(t *testing.T) {
	// Verify the constant is set correctly for CONFIG_DB
	if configDbId != 4 {
		t.Errorf("Expected configDbId=4 (CONFIG_DB), got %d", configDbId)
	}
}

func TestGetConfigDbClientDefault_DefaultConstants(t *testing.T) {
	// Verify default constants are set correctly
	if defaultRedisSocket != "/var/run/redis/redis.sock" {
		t.Errorf("Expected defaultRedisSocket=/var/run/redis/redis.sock, got %s", defaultRedisSocket)
	}
	if defaultRedisTCP != "127.0.0.1:6379" {
		t.Errorf("Expected defaultRedisTCP=127.0.0.1:6379, got %s", defaultRedisTCP)
	}
}

func TestTrySet(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	originalFunc := getConfigDbClientFunc
	defer func() { getConfigDbClientFunc = originalFunc }()

	getConfigDbClientFunc = func() (*redis.Client, error) {
		return redis.NewClient(&redis.Options{
			Addr: mr.Addr(),
			DB:   0,
		}), nil
	}

	// Set up allowed SKU
	mr.HSet("DEVICE_METADATA|localhost", "hwsku", "Cisco-8102-test")

	t.Run("bypass conditions not met - no header", func(t *testing.T) {
		ctx := context.Background()
		updates := []*gnmipb.Update{
			{
				Path: &gnmipb.Path{
					Elem: []*gnmipb.PathElem{{Name: "VNET"}, {Name: "vnet1"}},
				},
				Val: &gnmipb.TypedValue{
					Value: &gnmipb.TypedValue_JsonIetfVal{JsonIetfVal: []byte(`{"vni": "1000"}`)},
				},
			},
		}

		resp, used, err := TrySet(ctx, nil, nil, updates)
		if used {
			t.Error("TrySet should return used=false when bypass header missing")
		}
		if resp != nil {
			t.Error("Expected nil response when bypass not used")
		}
		if err != nil {
			t.Errorf("Expected nil error, got %v", err)
		}
	})

	t.Run("bypass conditions not met - disallowed table", func(t *testing.T) {
		ctx := metadata.NewIncomingContext(
			context.Background(),
			metadata.Pairs(MetadataKeyBypassValidation, "true"),
		)
		updates := []*gnmipb.Update{
			{
				Path: &gnmipb.Path{
					Elem: []*gnmipb.PathElem{{Name: "PORT"}, {Name: "Ethernet0"}},
				},
				Val: &gnmipb.TypedValue{
					Value: &gnmipb.TypedValue_JsonIetfVal{JsonIetfVal: []byte(`{"speed": "100000"}`)},
				},
			},
		}

		resp, used, err := TrySet(ctx, nil, nil, updates)
		if used {
			t.Error("TrySet should return used=false for disallowed table")
		}
		if resp != nil {
			t.Error("Expected nil response when bypass not used")
		}
		if err != nil {
			t.Errorf("Expected nil error, got %v", err)
		}
	})

	t.Run("bypass with updates only", func(t *testing.T) {
		mr.FlushAll()
		mr.HSet("DEVICE_METADATA|localhost", "hwsku", "Cisco-8102-test")

		ctx := metadata.NewIncomingContext(
			context.Background(),
			metadata.Pairs(MetadataKeyBypassValidation, "true"),
		)
		updates := []*gnmipb.Update{
			{
				Path: &gnmipb.Path{
					Elem: []*gnmipb.PathElem{{Name: "VNET"}, {Name: "vnet1"}},
				},
				Val: &gnmipb.TypedValue{
					Value: &gnmipb.TypedValue_JsonIetfVal{JsonIetfVal: []byte(`{"vni": "1000"}`)},
				},
			},
		}

		resp, used, err := TrySet(ctx, nil, nil, updates)
		if !used {
			t.Error("TrySet should return used=true when bypass conditions met")
		}
		if err != nil {
			t.Errorf("Expected nil error, got %v", err)
		}
		if resp == nil {
			t.Fatal("Expected non-nil response")
		}
		if len(resp.Response) != 1 {
			t.Errorf("Expected 1 result, got %d", len(resp.Response))
		}
		if resp.Response[0].Op != gnmipb.UpdateResult_UPDATE {
			t.Errorf("Expected UPDATE op, got %v", resp.Response[0].Op)
		}

		// Verify data was written
		vni := mr.HGet("VNET|vnet1", "vni")
		if vni != "1000" {
			t.Errorf("Expected vni=1000, got %s", vni)
		}
	})

	t.Run("bypass with deletes only", func(t *testing.T) {
		mr.FlushAll()
		mr.HSet("DEVICE_METADATA|localhost", "hwsku", "Cisco-8102-test")
		mr.HSet("VNET|vnet1", "vni", "1000")

		ctx := metadata.NewIncomingContext(
			context.Background(),
			metadata.Pairs(MetadataKeyBypassValidation, "true"),
		)
		deletes := []*gnmipb.Path{
			{
				Elem: []*gnmipb.PathElem{{Name: "VNET"}, {Name: "vnet1"}},
			},
		}

		resp, used, err := TrySet(ctx, nil, deletes, nil)
		if !used {
			t.Error("TrySet should return used=true when bypass conditions met")
		}
		if err != nil {
			t.Errorf("Expected nil error, got %v", err)
		}
		if resp == nil {
			t.Fatal("Expected non-nil response")
		}
		if len(resp.Response) != 1 {
			t.Errorf("Expected 1 result, got %d", len(resp.Response))
		}
		if resp.Response[0].Op != gnmipb.UpdateResult_DELETE {
			t.Errorf("Expected DELETE op, got %v", resp.Response[0].Op)
		}

		// Verify data was deleted
		if mr.Exists("VNET|vnet1") {
			t.Error("Expected key to be deleted")
		}
	})

	t.Run("bypass with mixed deletes and updates", func(t *testing.T) {
		mr.FlushAll()
		mr.HSet("DEVICE_METADATA|localhost", "hwsku", "Cisco-8102-test")
		mr.HSet("VNET|vnet_old", "vni", "999")

		ctx := metadata.NewIncomingContext(
			context.Background(),
			metadata.Pairs(MetadataKeyBypassValidation, "true"),
		)
		deletes := []*gnmipb.Path{
			{
				Elem: []*gnmipb.PathElem{{Name: "VNET"}, {Name: "vnet_old"}},
			},
		}
		updates := []*gnmipb.Update{
			{
				Path: &gnmipb.Path{
					Elem: []*gnmipb.PathElem{{Name: "VNET"}, {Name: "vnet_new"}},
				},
				Val: &gnmipb.TypedValue{
					Value: &gnmipb.TypedValue_JsonIetfVal{JsonIetfVal: []byte(`{"vni": "2000"}`)},
				},
			},
		}

		resp, used, err := TrySet(ctx, nil, deletes, updates)
		if !used {
			t.Error("TrySet should return used=true")
		}
		if err != nil {
			t.Errorf("Expected nil error, got %v", err)
		}
		if resp == nil {
			t.Fatal("Expected non-nil response")
		}
		if len(resp.Response) != 2 {
			t.Errorf("Expected 2 results, got %d", len(resp.Response))
		}

		// Verify delete happened (first per gNMI spec)
		if mr.Exists("VNET|vnet_old") {
			t.Error("Expected old key to be deleted")
		}
		// Verify update happened
		vni := mr.HGet("VNET|vnet_new", "vni")
		if vni != "2000" {
			t.Errorf("Expected vni=2000, got %s", vni)
		}
	})

	t.Run("empty operations", func(t *testing.T) {
		ctx := metadata.NewIncomingContext(
			context.Background(),
			metadata.Pairs(MetadataKeyBypassValidation, "true"),
		)

		resp, used, err := TrySet(ctx, nil, nil, nil)
		if used {
			t.Error("TrySet should return used=false for empty operations")
		}
		if resp != nil {
			t.Error("Expected nil response")
		}
		if err != nil {
			t.Errorf("Expected nil error, got %v", err)
		}
	})
}

func TestTrySetErrors(t *testing.T) {
	t.Run("update error returns used=true with error", func(t *testing.T) {
		originalFunc := getConfigDbClientFunc
		defer func() { getConfigDbClientFunc = originalFunc }()

		getConfigDbClientFunc = func() (*redis.Client, error) {
			return nil, fmt.Errorf("connection refused")
		}

		ctx := metadata.NewIncomingContext(
			context.Background(),
			metadata.Pairs(MetadataKeyBypassValidation, "true"),
		)
		updates := []*gnmipb.Update{
			{
				Path: &gnmipb.Path{
					Elem: []*gnmipb.PathElem{{Name: "VNET"}, {Name: "vnet1"}},
				},
				Val: &gnmipb.TypedValue{
					Value: &gnmipb.TypedValue_JsonIetfVal{JsonIetfVal: []byte(`{"vni": "1000"}`)},
				},
			},
		}

		// Need to mock checkSKU to pass - but it will fail on Apply
		// For this test, we need the SKU check to pass first
		mr := miniredis.RunT(t)
		defer mr.Close()

		// First call succeeds (SKU check), second fails (Apply)
		callCount := 0
		getConfigDbClientFunc = func() (*redis.Client, error) {
			callCount++
			if callCount == 1 {
				// SKU check succeeds
				return redis.NewClient(&redis.Options{Addr: mr.Addr(), DB: 0}), nil
			}
			// Apply fails
			return nil, fmt.Errorf("connection refused")
		}
		mr.HSet("DEVICE_METADATA|localhost", "hwsku", "Cisco-8102-test")

		resp, used, err := TrySet(ctx, nil, nil, updates)
		if !used {
			t.Error("TrySet should return used=true when bypass was attempted")
		}
		if err == nil {
			t.Error("Expected error")
		}
		if resp != nil {
			t.Error("Expected nil response on error")
		}
	})
}

func TestConvertToRedisFields(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]interface{}
		expected map[string]interface{}
	}{
		{
			name: "scalar string value",
			input: map[string]interface{}{
				"name": "test",
			},
			expected: map[string]interface{}{
				"name": "test",
			},
		},
		{
			name: "scalar int value",
			input: map[string]interface{}{
				"vni": 1000,
			},
			expected: map[string]interface{}{
				"vni": "1000",
			},
		},
		{
			name: "list value - SONiC convention",
			input: map[string]interface{}{
				"ip_range": []interface{}{"10.0.1.0/24", "10.0.2.0/24"},
			},
			expected: map[string]interface{}{
				"ip_range@": "10.0.1.0/24,10.0.2.0/24",
			},
		},
		{
			name: "mixed scalar and list values",
			input: map[string]interface{}{
				"name":     "WLPARTNER_PASSIVE_V4",
				"peer_asn": "4210000062",
				"ip_range": []interface{}{"10.0.1.0/24", "10.0.2.0/24"},
			},
			expected: map[string]interface{}{
				"name":      "WLPARTNER_PASSIVE_V4",
				"peer_asn":  "4210000062",
				"ip_range@": "10.0.1.0/24,10.0.2.0/24",
			},
		},
		{
			name: "empty list",
			input: map[string]interface{}{
				"ports": []interface{}{},
			},
			expected: map[string]interface{}{
				"ports@": "",
			},
		},
		{
			name: "single item list",
			input: map[string]interface{}{
				"members": []interface{}{"Ethernet0"},
			},
			expected: map[string]interface{}{
				"members@": "Ethernet0",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertToRedisFields(tt.input)
			if len(result) != len(tt.expected) {
				t.Errorf("convertToRedisFields() returned %d fields, want %d", len(result), len(tt.expected))
			}
			for k, v := range tt.expected {
				if result[k] != v {
					t.Errorf("convertToRedisFields()[%s] = %v, want %v", k, result[k], v)
				}
			}
		})
	}
}

func TestApplyWithListFields(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	originalFunc := getConfigDbClientFunc
	defer func() { getConfigDbClientFunc = originalFunc }()

	getConfigDbClientFunc = func() (*redis.Client, error) {
		return redis.NewClient(&redis.Options{
			Addr: mr.Addr(),
			DB:   0,
		}), nil
	}

	t.Run("BGP_PEER_RANGE with ip_range list - single entry", func(t *testing.T) {
		mr.FlushAll()
		// This mimics the exact request from the bug report
		updates := []*gnmipb.Update{
			{
				Path: &gnmipb.Path{
					Elem: []*gnmipb.PathElem{
						{Name: "CONFIG_DB"},
						{Name: "localhost"},
						{Name: "BGP_PEER_RANGE"},
						{Name: "Vnet_1000|WLPARTNER_PASSIVE_V4"},
					},
				},
				Val: &gnmipb.TypedValue{
					Value: &gnmipb.TypedValue_JsonIetfVal{
						JsonIetfVal: []byte(`{"name":"WLPARTNER_PASSIVE_V4","peer_asn":"4210000062","ip_range":["10.0.1.0/24", "10.0.2.0/24"]}`),
					},
				},
			},
		}

		err := Apply(context.Background(), nil, updates)
		if err != nil {
			t.Errorf("Apply() error = %v", err)
		}

		// Verify SONiC convention: ip_range@ with comma-separated values
		ipRange := mr.HGet("BGP_PEER_RANGE|Vnet_1000|WLPARTNER_PASSIVE_V4", "ip_range@")
		expectedIpRange := "10.0.1.0/24,10.0.2.0/24"
		if ipRange != expectedIpRange {
			t.Errorf("Expected ip_range@=%s, got %s", expectedIpRange, ipRange)
		}

		// Verify scalar fields remain unchanged
		name := mr.HGet("BGP_PEER_RANGE|Vnet_1000|WLPARTNER_PASSIVE_V4", "name")
		if name != "WLPARTNER_PASSIVE_V4" {
			t.Errorf("Expected name=WLPARTNER_PASSIVE_V4, got %s", name)
		}

		peerAsn := mr.HGet("BGP_PEER_RANGE|Vnet_1000|WLPARTNER_PASSIVE_V4", "peer_asn")
		if peerAsn != "4210000062" {
			t.Errorf("Expected peer_asn=4210000062, got %s", peerAsn)
		}

		// Verify ip_range (without @) does NOT exist
		badIpRange := mr.HGet("BGP_PEER_RANGE|Vnet_1000|WLPARTNER_PASSIVE_V4", "ip_range")
		if badIpRange != "" {
			t.Errorf("ip_range (without @) should not exist, got %s", badIpRange)
		}
	})

	t.Run("bulk update with list fields", func(t *testing.T) {
		mr.FlushAll()
		updates := []*gnmipb.Update{
			{
				Path: &gnmipb.Path{
					Elem: []*gnmipb.PathElem{
						{Name: "CONFIG_DB"},
						{Name: "localhost"},
						{Name: "BGP_PEER_RANGE"},
					},
				},
				Val: &gnmipb.TypedValue{
					Value: &gnmipb.TypedValue_JsonIetfVal{
						JsonIetfVal: []byte(`{
							"Vnet_1000|PEER_V4": {"name":"PEER_V4","ip_range":["10.1.0.0/24"]},
							"Vnet_2000|PEER_V6": {"name":"PEER_V6","ip_range":["10.2.0.0/24","10.3.0.0/24"]}
						}`),
					},
				},
			},
		}

		err := Apply(context.Background(), nil, updates)
		if err != nil {
			t.Errorf("Apply() error = %v", err)
		}

		// Verify first entry
		ipRange1 := mr.HGet("BGP_PEER_RANGE|Vnet_1000|PEER_V4", "ip_range@")
		if ipRange1 != "10.1.0.0/24" {
			t.Errorf("Expected ip_range@=10.1.0.0/24, got %s", ipRange1)
		}

		// Verify second entry
		ipRange2 := mr.HGet("BGP_PEER_RANGE|Vnet_2000|PEER_V6", "ip_range@")
		if ipRange2 != "10.2.0.0/24,10.3.0.0/24" {
			t.Errorf("Expected ip_range@=10.2.0.0/24,10.3.0.0/24, got %s", ipRange2)
		}
	})
}
