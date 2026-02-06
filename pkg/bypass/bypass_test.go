//go:build !gnmi_memcheck
// +build !gnmi_memcheck

package bypass

import (
	"context"
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
