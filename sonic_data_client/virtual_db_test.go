package client

import (
	"sort"
	"testing"

	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
)

// setupPortMaps populates the package-level maps used by v2rPortPhyAttr*
// functions with test data, and returns a cleanup function that restores the
// original values.
func setupPortMaps(t *testing.T) func() {
	t.Helper()

	origPortNameMap := countersPortNameMap
	origPort2NsMap := port2namespaceMap

	countersPortNameMap = map[string]string{
		"Ethernet0":  "oid:0x1000000000001",
		"Ethernet68": "oid:0x1000000000039",
	}
	port2namespaceMap = map[string]string{
		"Ethernet0":  "",
		"Ethernet68": "",
	}

	return func() {
		countersPortNameMap = origPortNameMap
		port2namespaceMap = origPort2NsMap
	}
}

// --------------------------------------------------------------------------
// Tests for countersDbHasTableKeys (db_client.go)
// --------------------------------------------------------------------------

func TestCountersDbHasTableKeys(t *testing.T) {
	tests := []struct {
		tableName string
		want      bool
	}{
		{"COUNTERS", true},
		{"PORT_PHY_ATTR", true},
		{"COUNTERS_PORT_NAME_MAP", false},
		{"RATES", false},
		{"PERIODIC_WATERMARKS", false},
		{"ACL_COUNTER_RULE_MAP", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.tableName, func(t *testing.T) {
			got := countersDbHasTableKeys(tt.tableName)
			if got != tt.want {
				t.Errorf("countersDbHasTableKeys(%q) = %v, want %v", tt.tableName, got, tt.want)
			}
		})
	}
}

// --------------------------------------------------------------------------
// Tests for v2rPortPhyAttrStats (virtual_db.go)
// --------------------------------------------------------------------------

func TestV2rPortPhyAttrStats_Wildcard(t *testing.T) {
	sdcfg.Init()
	restore := setupPortMaps(t)
	defer restore()

	paths := []string{"COUNTERS_DB", "PORT_PHY_ATTR", "Ethernet*"}
	tblPaths, err := v2rPortPhyAttrStats(paths)
	if err != nil {
		t.Fatalf("v2rPortPhyAttrStats returned error: %v", err)
	}
	if len(tblPaths) != 2 {
		t.Fatalf("expected 2 table paths, got %d", len(tblPaths))
	}

	// Sort for deterministic comparison (map iteration order is random)
	sort.Slice(tblPaths, func(i, j int) bool {
		return tblPaths[i].jsonTableKey < tblPaths[j].jsonTableKey
	})

	// Ethernet0
	tp := tblPaths[0]
	if tp.dbName != "COUNTERS_DB" {
		t.Errorf("dbName = %q, want COUNTERS_DB", tp.dbName)
	}
	if tp.tableName != "PORT_PHY_ATTR" {
		t.Errorf("tableName = %q, want PORT_PHY_ATTR", tp.tableName)
	}
	if tp.tableKey != "oid:0x1000000000001" {
		t.Errorf("tableKey = %q, want oid:0x1000000000001", tp.tableKey)
	}
	if tp.jsonTableKey != "Ethernet0" {
		t.Errorf("jsonTableKey = %q, want Ethernet0 (no alias translation)", tp.jsonTableKey)
	}

	// Ethernet68
	tp = tblPaths[1]
	if tp.tableKey != "oid:0x1000000000039" {
		t.Errorf("tableKey = %q, want oid:0x1000000000039", tp.tableKey)
	}
	if tp.jsonTableKey != "Ethernet68" {
		t.Errorf("jsonTableKey = %q, want Ethernet68 (no alias translation)", tp.jsonTableKey)
	}
}

func TestV2rPortPhyAttrStats_SinglePort(t *testing.T) {
	sdcfg.Init()
	restore := setupPortMaps(t)
	defer restore()

	paths := []string{"COUNTERS_DB", "PORT_PHY_ATTR", "Ethernet68"}
	tblPaths, err := v2rPortPhyAttrStats(paths)
	if err != nil {
		t.Fatalf("v2rPortPhyAttrStats returned error: %v", err)
	}
	if len(tblPaths) != 1 {
		t.Fatalf("expected 1 table path, got %d", len(tblPaths))
	}

	tp := tblPaths[0]
	if tp.dbName != "COUNTERS_DB" {
		t.Errorf("dbName = %q, want COUNTERS_DB", tp.dbName)
	}
	if tp.tableName != "PORT_PHY_ATTR" {
		t.Errorf("tableName = %q, want PORT_PHY_ATTR", tp.tableName)
	}
	if tp.tableKey != "oid:0x1000000000039" {
		t.Errorf("tableKey = %q, want oid:0x1000000000039", tp.tableKey)
	}
	// Single port mode should NOT set jsonTableKey
	if tp.jsonTableKey != "" {
		t.Errorf("jsonTableKey = %q, want empty for single port", tp.jsonTableKey)
	}
}

func TestV2rPortPhyAttrStats_InvalidPort(t *testing.T) {
	sdcfg.Init()
	restore := setupPortMaps(t)
	defer restore()

	paths := []string{"COUNTERS_DB", "PORT_PHY_ATTR", "EthernetXYZ"}
	_, err := v2rPortPhyAttrStats(paths)
	if err == nil {
		t.Fatal("expected error for invalid port name, got nil")
	}
}

func TestV2rPortPhyAttrStats_MissingNamespace(t *testing.T) {
	sdcfg.Init()
	restore := setupPortMaps(t)
	defer restore()

	// Add a port to the OID map but NOT to the namespace map
	countersPortNameMap["Ethernet99"] = "oid:0x1000000000099"

	// Wildcard should fail because Ethernet99 has no namespace
	paths := []string{"COUNTERS_DB", "PORT_PHY_ATTR", "Ethernet*"}
	_, err := v2rPortPhyAttrStats(paths)
	if err == nil {
		t.Fatal("expected error for port missing namespace, got nil")
	}
}

func TestV2rPortPhyAttrStats_SingleMissingNamespace(t *testing.T) {
	sdcfg.Init()
	restore := setupPortMaps(t)
	defer restore()

	// Ethernet68 exists in countersPortNameMap. Remove it from port2namespaceMap.
	delete(port2namespaceMap, "Ethernet68")

	paths := []string{"COUNTERS_DB", "PORT_PHY_ATTR", "Ethernet68"}
	_, err := v2rPortPhyAttrStats(paths)
	if err == nil {
		t.Fatal("expected error for port missing namespace, got nil")
	}
}

// --------------------------------------------------------------------------
// Tests for v2rPortPhyAttrFieldStats (virtual_db.go)
// --------------------------------------------------------------------------

func TestV2rPortPhyAttrFieldStats_Wildcard(t *testing.T) {
	sdcfg.Init()
	restore := setupPortMaps(t)
	defer restore()

	paths := []string{"COUNTERS_DB", "PORT_PHY_ATTR", "Ethernet*", "phy_rx_signal_detect"}
	tblPaths, err := v2rPortPhyAttrFieldStats(paths)
	if err != nil {
		t.Fatalf("v2rPortPhyAttrFieldStats returned error: %v", err)
	}
	if len(tblPaths) != 2 {
		t.Fatalf("expected 2 table paths, got %d", len(tblPaths))
	}

	sort.Slice(tblPaths, func(i, j int) bool {
		return tblPaths[i].jsonTableKey < tblPaths[j].jsonTableKey
	})

	tp := tblPaths[0]
	if tp.tableName != "PORT_PHY_ATTR" {
		t.Errorf("tableName = %q, want PORT_PHY_ATTR", tp.tableName)
	}
	if tp.tableKey != "oid:0x1000000000001" {
		t.Errorf("tableKey = %q, want oid:0x1000000000001", tp.tableKey)
	}
	if tp.field != "phy_rx_signal_detect" {
		t.Errorf("field = %q, want phy_rx_signal_detect", tp.field)
	}
	if tp.jsonTableKey != "Ethernet0" {
		t.Errorf("jsonTableKey = %q, want Ethernet0 (no alias)", tp.jsonTableKey)
	}
	if tp.jsonField != "phy_rx_signal_detect" {
		t.Errorf("jsonField = %q, want phy_rx_signal_detect", tp.jsonField)
	}

	tp = tblPaths[1]
	if tp.jsonTableKey != "Ethernet68" {
		t.Errorf("jsonTableKey = %q, want Ethernet68 (no alias)", tp.jsonTableKey)
	}
	if tp.field != "phy_rx_signal_detect" {
		t.Errorf("field = %q, want phy_rx_signal_detect", tp.field)
	}
}

func TestV2rPortPhyAttrFieldStats_SinglePort(t *testing.T) {
	sdcfg.Init()
	restore := setupPortMaps(t)
	defer restore()

	paths := []string{"COUNTERS_DB", "PORT_PHY_ATTR", "Ethernet0", "rx_snr"}
	tblPaths, err := v2rPortPhyAttrFieldStats(paths)
	if err != nil {
		t.Fatalf("v2rPortPhyAttrFieldStats returned error: %v", err)
	}
	if len(tblPaths) != 1 {
		t.Fatalf("expected 1 table path, got %d", len(tblPaths))
	}

	tp := tblPaths[0]
	if tp.dbName != "COUNTERS_DB" {
		t.Errorf("dbName = %q, want COUNTERS_DB", tp.dbName)
	}
	if tp.tableName != "PORT_PHY_ATTR" {
		t.Errorf("tableName = %q, want PORT_PHY_ATTR", tp.tableName)
	}
	if tp.tableKey != "oid:0x1000000000001" {
		t.Errorf("tableKey = %q, want oid:0x1000000000001", tp.tableKey)
	}
	if tp.field != "rx_snr" {
		t.Errorf("field = %q, want rx_snr", tp.field)
	}
	// Single port mode should NOT set jsonTableKey or jsonField
	if tp.jsonTableKey != "" {
		t.Errorf("jsonTableKey = %q, want empty for single port", tp.jsonTableKey)
	}
	if tp.jsonField != "" {
		t.Errorf("jsonField = %q, want empty for single port", tp.jsonField)
	}
}

func TestV2rPortPhyAttrFieldStats_InvalidPort(t *testing.T) {
	sdcfg.Init()
	restore := setupPortMaps(t)
	defer restore()

	paths := []string{"COUNTERS_DB", "PORT_PHY_ATTR", "EthernetBad", "phy_rx_signal_detect"}
	_, err := v2rPortPhyAttrFieldStats(paths)
	if err == nil {
		t.Fatal("expected error for invalid port name, got nil")
	}
}

func TestV2rPortPhyAttrFieldStats_MissingNamespace(t *testing.T) {
	sdcfg.Init()
	restore := setupPortMaps(t)
	defer restore()

	// Add port to OID map but not namespace map
	countersPortNameMap["Ethernet99"] = "oid:0x1000000000099"

	paths := []string{"COUNTERS_DB", "PORT_PHY_ATTR", "Ethernet*", "phy_rx_signal_detect"}
	_, err := v2rPortPhyAttrFieldStats(paths)
	if err == nil {
		t.Fatal("expected error for port missing namespace, got nil")
	}
}

func TestV2rPortPhyAttrFieldStats_SingleMissingNamespace(t *testing.T) {
	sdcfg.Init()
	restore := setupPortMaps(t)
	defer restore()

	delete(port2namespaceMap, "Ethernet0")

	paths := []string{"COUNTERS_DB", "PORT_PHY_ATTR", "Ethernet0", "rx_snr"}
	_, err := v2rPortPhyAttrFieldStats(paths)
	if err == nil {
		t.Fatal("expected error for port missing namespace, got nil")
	}
}
