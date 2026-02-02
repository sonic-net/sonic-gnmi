package client

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis"
	"google.golang.org/grpc/metadata"
)

func TestShouldBypassValidation(t *testing.T) {
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
		{
			name: "context with empty bypass value",
			ctx: metadata.NewIncomingContext(
				context.Background(),
				metadata.Pairs(MetadataKeyBypassValidation, ""),
			),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shouldBypassValidation(tt.ctx)
			if result != tt.expected {
				t.Errorf("shouldBypassValidation() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestIsAllowedTable(t *testing.T) {
	tests := []struct {
		name      string
		patchList []map[string]interface{}
		expected  bool
	}{
		{
			name:      "empty patch list",
			patchList: []map[string]interface{}{},
			expected:  true,
		},
		{
			name: "single allowed table VNET",
			patchList: []map[string]interface{}{
				{"path": "/VNET/vnet1", "op": "add", "value": map[string]interface{}{"vni": "1000"}},
			},
			expected: true,
		},
		{
			name: "single allowed table ACL_RULE",
			patchList: []map[string]interface{}{
				{"path": "/ACL_RULE/rule1", "op": "add", "value": map[string]interface{}{"priority": "100"}},
			},
			expected: true,
		},
		{
			name: "multiple allowed tables",
			patchList: []map[string]interface{}{
				{"path": "/VNET/vnet1", "op": "add", "value": map[string]interface{}{}},
				{"path": "/VNET_ROUTE_TUNNEL/route1", "op": "add", "value": map[string]interface{}{}},
				{"path": "/BGP_PEER_RANGE/peer1", "op": "add", "value": map[string]interface{}{}},
			},
			expected: true,
		},
		{
			name: "disallowed table",
			patchList: []map[string]interface{}{
				{"path": "/PORT/Ethernet0", "op": "add", "value": map[string]interface{}{}},
			},
			expected: false,
		},
		{
			name: "mixed allowed and disallowed",
			patchList: []map[string]interface{}{
				{"path": "/VNET/vnet1", "op": "add", "value": map[string]interface{}{}},
				{"path": "/PORT/Ethernet0", "op": "add", "value": map[string]interface{}{}},
			},
			expected: false,
		},
		{
			name: "path with field",
			patchList: []map[string]interface{}{
				{"path": "/VLAN_SUB_INTERFACE/eth0.100/admin_status", "op": "replace", "value": "up"},
			},
			expected: true,
		},
		{
			name: "invalid path - no path key",
			patchList: []map[string]interface{}{
				{"op": "add", "value": map[string]interface{}{}},
			},
			expected: false,
		},
		{
			name: "invalid path - non-string path",
			patchList: []map[string]interface{}{
				{"path": 123, "op": "add"},
			},
			expected: false,
		},
		{
			name: "path with only slash",
			patchList: []map[string]interface{}{
				{"path": "/", "op": "add"},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isAllowedTable(tt.patchList)
			if result != tt.expected {
				t.Errorf("isAllowedTable() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestCheckSKU(t *testing.T) {
	// Start miniredis
	mr := miniredis.RunT(t)
	defer mr.Close()

	// Override the client getter for testing
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
			name:     "Empty SKU",
			hwsku:    "",
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
			// Set up the SKU in miniredis
			mr.FlushAll()
			if tt.hwsku != "" {
				mr.HSet("DEVICE_METADATA|localhost", "hwsku", tt.hwsku)
			}

			result := checkSKU()
			if result != tt.expected {
				t.Errorf("checkSKU() = %v, want %v for SKU %q", result, tt.expected, tt.hwsku)
			}
		})
	}
}

func TestCheckSKU_NoMetadata(t *testing.T) {
	// Start miniredis without DEVICE_METADATA
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

func TestApplyPatchDirectly(t *testing.T) {
	// Start miniredis
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

	client := &MixedDbClient{}

	t.Run("add operation with map value", func(t *testing.T) {
		mr.FlushAll()
		patchList := []map[string]interface{}{
			{
				"op":    "add",
				"path":  "/VNET/vnet1",
				"value": map[string]interface{}{"vni": "1000", "scope": "default"},
			},
		}

		err := client.applyPatchDirectly(patchList)
		if err != nil {
			t.Errorf("applyPatchDirectly() error = %v", err)
		}

		// Verify data was written
		vni, _ := mr.HGet("VNET|vnet1", "vni")
		if vni != "1000" {
			t.Errorf("Expected vni=1000, got %s", vni)
		}
		scope, _ := mr.HGet("VNET|vnet1", "scope")
		if scope != "default" {
			t.Errorf("Expected scope=default, got %s", scope)
		}
	})

	t.Run("replace operation", func(t *testing.T) {
		mr.FlushAll()
		mr.HSet("VNET|vnet1", "vni", "500")

		patchList := []map[string]interface{}{
			{
				"op":    "replace",
				"path":  "/VNET/vnet1",
				"value": map[string]interface{}{"vni": "2000"},
			},
		}

		err := client.applyPatchDirectly(patchList)
		if err != nil {
			t.Errorf("applyPatchDirectly() error = %v", err)
		}

		vni, _ := mr.HGet("VNET|vnet1", "vni")
		if vni != "2000" {
			t.Errorf("Expected vni=2000, got %s", vni)
		}
	})

	t.Run("remove operation", func(t *testing.T) {
		mr.FlushAll()
		mr.HSet("VNET|vnet1", "vni", "1000")

		patchList := []map[string]interface{}{
			{
				"op":   "remove",
				"path": "/VNET/vnet1",
			},
		}

		err := client.applyPatchDirectly(patchList)
		if err != nil {
			t.Errorf("applyPatchDirectly() error = %v", err)
		}

		exists := mr.Exists("VNET|vnet1")
		if exists {
			t.Error("Expected key to be deleted")
		}
	})

	t.Run("single field update", func(t *testing.T) {
		mr.FlushAll()
		mr.HSet("VNET|vnet1", "vni", "1000")
		mr.HSet("VNET|vnet1", "scope", "default")

		patchList := []map[string]interface{}{
			{
				"op":    "replace",
				"path":  "/VNET/vnet1/vni",
				"value": "3000",
			},
		}

		err := client.applyPatchDirectly(patchList)
		if err != nil {
			t.Errorf("applyPatchDirectly() error = %v", err)
		}

		vni, _ := mr.HGet("VNET|vnet1", "vni")
		if vni != "3000" {
			t.Errorf("Expected vni=3000, got %s", vni)
		}
		// scope should be unchanged
		scope, _ := mr.HGet("VNET|vnet1", "scope")
		if scope != "default" {
			t.Errorf("Expected scope=default, got %s", scope)
		}
	})

	t.Run("invalid path - too short", func(t *testing.T) {
		patchList := []map[string]interface{}{
			{
				"op":   "add",
				"path": "/VNET",
			},
		}

		err := client.applyPatchDirectly(patchList)
		if err == nil {
			t.Error("Expected error for invalid path")
		}
	})

	t.Run("multiple operations", func(t *testing.T) {
		mr.FlushAll()

		patchList := []map[string]interface{}{
			{
				"op":    "add",
				"path":  "/VNET/vnet1",
				"value": map[string]interface{}{"vni": "1000"},
			},
			{
				"op":    "add",
				"path":  "/VNET/vnet2",
				"value": map[string]interface{}{"vni": "2000"},
			},
		}

		err := client.applyPatchDirectly(patchList)
		if err != nil {
			t.Errorf("applyPatchDirectly() error = %v", err)
		}

		vni1, _ := mr.HGet("VNET|vnet1", "vni")
		vni2, _ := mr.HGet("VNET|vnet2", "vni")
		if vni1 != "1000" || vni2 != "2000" {
			t.Errorf("Expected vni1=1000, vni2=2000, got %s, %s", vni1, vni2)
		}
	})
}

func TestBypassAllowedTables(t *testing.T) {
	// Verify the allowed tables are as expected
	expectedTables := []string{"VNET", "VNET_ROUTE_TUNNEL", "VLAN_SUB_INTERFACE", "ACL_RULE", "BGP_PEER_RANGE"}

	for _, table := range expectedTables {
		if !BypassAllowedTables[table] {
			t.Errorf("Expected %s to be in BypassAllowedTables", table)
		}
	}

	// Verify some tables are NOT allowed
	disallowedTables := []string{"PORT", "INTERFACE", "VLAN", "LOOPBACK_INTERFACE"}
	for _, table := range disallowedTables {
		if BypassAllowedTables[table] {
			t.Errorf("Expected %s to NOT be in BypassAllowedTables", table)
		}
	}
}

func TestBypassAllowedSKUPrefixes(t *testing.T) {
	// Verify the allowed SKU prefixes
	expectedPrefixes := []string{"Cisco-8102", "Cisco-8101", "Cisco-8223"}

	if len(BypassAllowedSKUPrefixes) != len(expectedPrefixes) {
		t.Errorf("Expected %d SKU prefixes, got %d", len(expectedPrefixes), len(BypassAllowedSKUPrefixes))
	}

	for i, prefix := range expectedPrefixes {
		if BypassAllowedSKUPrefixes[i] != prefix {
			t.Errorf("Expected prefix %s at index %d, got %s", prefix, i, BypassAllowedSKUPrefixes[i])
		}
	}
}
