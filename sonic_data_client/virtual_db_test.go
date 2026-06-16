package client

import (
	"sort"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis"
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

func TestV2rPortPhyAttrStats_EmptyMap(t *testing.T) {
	sdcfg.Init()
	restore := setupPortMaps(t)
	defer restore()

	// Clear the maps so no ports exist at all
	countersPortNameMap = map[string]string{}
	port2namespaceMap = map[string]string{}

	// Wildcard: should succeed with zero results (no ports to iterate)
	paths := []string{"COUNTERS_DB", "PORT_PHY_ATTR", "Ethernet*"}
	tblPaths, err := v2rPortPhyAttrStats(paths)
	if err != nil {
		t.Fatalf("expected no error for wildcard with empty map, got: %v", err)
	}
	if len(tblPaths) != 0 {
		t.Errorf("expected 0 table paths, got %d", len(tblPaths))
	}

	// Single port: should fail with "not a valid sonic interface"
	paths = []string{"COUNTERS_DB", "PORT_PHY_ATTR", "Ethernet0"}
	_, err = v2rPortPhyAttrStats(paths)
	if err == nil {
		t.Fatal("expected error for single port with empty map, got nil")
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

func TestV2rPortPhyAttrFieldStats_EmptyMap(t *testing.T) {
	sdcfg.Init()
	restore := setupPortMaps(t)
	defer restore()

	countersPortNameMap = map[string]string{}
	port2namespaceMap = map[string]string{}

	// Wildcard: should succeed with zero results
	paths := []string{"COUNTERS_DB", "PORT_PHY_ATTR", "Ethernet*", "phy_rx_signal_detect"}
	tblPaths, err := v2rPortPhyAttrFieldStats(paths)
	if err != nil {
		t.Fatalf("expected no error for wildcard with empty map, got: %v", err)
	}
	if len(tblPaths) != 0 {
		t.Errorf("expected 0 table paths, got %d", len(tblPaths))
	}

	// Single port: should fail
	paths = []string{"COUNTERS_DB", "PORT_PHY_ATTR", "Ethernet0", "phy_rx_signal_detect"}
	_, err = v2rPortPhyAttrFieldStats(paths)
	if err == nil {
		t.Fatal("expected error for single port with empty map, got nil")
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

// --------------------------------------------------------------------------
// Tests for Trie findNode backtracking (trie.go)
// --------------------------------------------------------------------------

func TestTrieBacktracking_VoQOverEthernet(t *testing.T) {
	// Ensure the global trie correctly routes VoQ paths through the "*" wildcard
	// when "Ethernet*" is also present at the same level.
	sdcfg.Init()

	// "str2-7804-lc7-1" with "VoQs" suffix should match "*" -> "VoQs" (VoQ handler)
	keys := []string{"COUNTERS_DB", "COUNTERS", "str2-7804-lc7-1", "VoQs"}
	node, ok := v2rTrie.Find(keys)
	if !ok || node == nil {
		t.Fatal("expected trie to find VoQ path for str2-7804-lc7-1/VoQs")
	}

	// "str2-7804-lc7-1|Asic0|Ethernet68" with "VoQs" should also match
	keys = []string{"COUNTERS_DB", "COUNTERS", "str2-7804-lc7-1|Asic0|Ethernet68", "VoQs"}
	node, ok = v2rTrie.Find(keys)
	if !ok || node == nil {
		t.Fatal("expected trie to find VoQ path for system port/VoQs")
	}
}

func TestTrieBacktracking_EthernetStillWorks(t *testing.T) {
	sdcfg.Init()

	// "Ethernet68" alone should still match "Ethernet*" (port stats)
	keys := []string{"COUNTERS_DB", "COUNTERS", "Ethernet68"}
	node, ok := v2rTrie.Find(keys)
	if !ok || node == nil {
		t.Fatal("expected trie to find Ethernet port stats path")
	}

	// "Ethernet68" with "Queues" should match "Ethernet*" -> "Queues"
	keys = []string{"COUNTERS_DB", "COUNTERS", "Ethernet68", "Queues"}
	node, ok = v2rTrie.Find(keys)
	if !ok || node == nil {
		t.Fatal("expected trie to find Ethernet queue stats path")
	}

	// "Ethernet68" with a field should match "Ethernet*" -> "*"
	keys = []string{"COUNTERS_DB", "COUNTERS", "Ethernet68", "SAI_PORT_STAT_IF_IN_OCTETS"}
	node, ok = v2rTrie.Find(keys)
	if !ok || node == nil {
		t.Fatal("expected trie to find Ethernet field stats path")
	}
}

func TestTrieBacktracking_NonExistentPath(t *testing.T) {
	sdcfg.Init()

	// Path that doesn't match anything
	keys := []string{"COUNTERS_DB", "COUNTERS", "str2-7804-lc7-1", "NonExistent"}
	_, ok := v2rTrie.Find(keys)
	if ok {
		t.Fatal("expected trie NOT to find non-existent path")
	}
}

// --------------------------------------------------------------------------
// Tests for parseVoQName (virtual_db.go)
// --------------------------------------------------------------------------

func TestParseVoQName_Valid(t *testing.T) {
	switchId, asicNs, ifName, voqIdx, err := parseVoQName("str2-7804-lc7-1|Asic0|Ethernet84:3")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if switchId != "str2-7804-lc7-1" {
		t.Errorf("switchId = %q, want str2-7804-lc7-1", switchId)
	}
	if asicNs != "Asic0" {
		t.Errorf("asicNamespace = %q, want Asic0", asicNs)
	}
	if ifName != "Ethernet84" {
		t.Errorf("interfaceName = %q, want Ethernet84", ifName)
	}
	if voqIdx != "3" {
		t.Errorf("voqIndex = %q, want 3", voqIdx)
	}
}

func TestParseVoQName_InvalidPipeParts(t *testing.T) {
	_, _, _, _, err := parseVoQName("str2-7804-lc7-1|Asic0")
	if err == nil {
		t.Fatal("expected error for only 2 pipe-separated parts")
	}
}

func TestParseVoQName_InvalidInterfaceFormat(t *testing.T) {
	_, _, _, _, err := parseVoQName("str2-7804-lc7-1|Asic0|Ethernet84")
	if err == nil {
		t.Fatal("expected error for missing colon in interface:voq")
	}
}

// --------------------------------------------------------------------------
// Tests for buildVoQJsonKey (virtual_db.go)
// --------------------------------------------------------------------------

func TestBuildVoQJsonKey(t *testing.T) {
	key := buildVoQJsonKey("str2-7804-lc7-1", "Asic0", "Ethernet84", "3")
	expected := "str2-7804-lc7-1|Asic0|Ethernet84:3"
	if key != expected {
		t.Errorf("got %q, want %q", key, expected)
	}
}

// --------------------------------------------------------------------------
// Tests for resolveVoQNamespace (virtual_db.go)
// --------------------------------------------------------------------------

func TestResolveVoQNamespace_FoundInMap(t *testing.T) {
	origMap := countersVoQOidNamespaceMap
	defer func() { countersVoQOidNamespaceMap = origMap }()

	countersVoQOidNamespaceMap = map[string]string{
		"oid:0x123": "asic0",
	}

	activeNs := map[string]*redis.Client{
		"asic0": redis.NewClient(&redis.Options{Addr: "localhost:6379"}),
	}

	ns := resolveVoQNamespace("oid:0x123", "Asic1", activeNs)
	if ns != "asic0" {
		t.Errorf("got %q, want asic0", ns)
	}
}

func TestResolveVoQNamespace_StaleNamespace(t *testing.T) {
	origMap := countersVoQOidNamespaceMap
	defer func() { countersVoQOidNamespaceMap = origMap }()

	countersVoQOidNamespaceMap = map[string]string{
		"oid:0x123": "asic2", // mapped to asic2
	}

	// activeNamespaces does NOT contain asic2
	activeNs := map[string]*redis.Client{
		"asic0": redis.NewClient(&redis.Options{Addr: "localhost:6379"}),
	}

	ns := resolveVoQNamespace("oid:0x123", "Asic1", activeNs)
	if ns != "asic1" { // falls back to lower(asicNamespace)
		t.Errorf("got %q, want asic1", ns)
	}
}

func TestResolveVoQNamespace_NotInMap(t *testing.T) {
	origMap := countersVoQOidNamespaceMap
	defer func() { countersVoQOidNamespaceMap = origMap }()

	countersVoQOidNamespaceMap = map[string]string{}

	activeNs := map[string]*redis.Client{
		"asic0": redis.NewClient(&redis.Options{Addr: "localhost:6379"}),
	}

	ns := resolveVoQNamespace("oid:0x999", "Asic0", activeNs)
	if ns != "asic0" { // falls back to lower(asicNamespace)
		t.Errorf("got %q, want asic0", ns)
	}
}

// --------------------------------------------------------------------------
// Tests for v2rSystemPortVoQStats (virtual_db.go)
// --------------------------------------------------------------------------

func setupVoQMaps(t *testing.T) func() {
	t.Helper()

	origVoQNameMap := countersVoQNameMap
	origVoQOidNsMap := countersVoQOidNamespaceMap
	origTarget2Redis := Target2RedisDb

	countersVoQNameMap = map[string]string{
		"str2-7804-lc7-1|Asic0|Ethernet68:0": "oid:0x160000000091c",
		"str2-7804-lc7-1|Asic0|Ethernet68:1": "oid:0x160000000091d",
		"str2-7804-lc7-1|Asic0|Ethernet84:0": "oid:0x160000000091e",
		"str2-7804-lc7-1|Asic0|Ethernet84:1": "oid:0x160000000092a",
	}
	countersVoQOidNamespaceMap = map[string]string{
		"oid:0x160000000091c": "",
		"oid:0x160000000091d": "",
		"oid:0x160000000091e": "",
		"oid:0x160000000092a": "",
	}

	// Set up a minimal Target2RedisDb so GetRedisClientsForDb returns a client
	Target2RedisDb = map[string]map[string]*redis.Client{
		"": {
			"COUNTERS_DB": redis.NewClient(&redis.Options{Addr: "localhost:6379"}),
		},
	}

	return func() {
		countersVoQNameMap = origVoQNameMap
		countersVoQOidNamespaceMap = origVoQOidNsMap
		Target2RedisDb = origTarget2Redis
	}
}

func TestV2rSystemPortVoQStats_Wildcard(t *testing.T) {
	sdcfg.Init()
	restore := setupVoQMaps(t)
	defer restore()

	paths := []string{"COUNTERS_DB", "COUNTERS", "SwitchName*", "VoQs"}
	tblPaths, err := v2rSystemPortVoQStats(paths)
	if err != nil {
		t.Fatalf("v2rSystemPortVoQStats returned error: %v", err)
	}
	if len(tblPaths) != 4 {
		t.Fatalf("expected 4 table paths, got %d", len(tblPaths))
	}

	// Verify all paths have expected fields
	for _, tp := range tblPaths {
		if tp.dbName != "COUNTERS_DB" {
			t.Errorf("dbName = %q, want COUNTERS_DB", tp.dbName)
		}
		if tp.tableName != "COUNTERS" {
			t.Errorf("tableName = %q, want COUNTERS", tp.tableName)
		}
		if tp.jsonTableKey == "" {
			t.Error("jsonTableKey should not be empty for wildcard VoQ query")
		}
	}
}

func TestV2rSystemPortVoQStats_SingleSwitch(t *testing.T) {
	sdcfg.Init()
	restore := setupVoQMaps(t)
	defer restore()

	paths := []string{"COUNTERS_DB", "COUNTERS", "str2-7804-lc7-1", "VoQs"}
	tblPaths, err := v2rSystemPortVoQStats(paths)
	if err != nil {
		t.Fatalf("v2rSystemPortVoQStats returned error: %v", err)
	}
	// All 4 VoQs belong to str2-7804-lc7-1
	if len(tblPaths) != 4 {
		t.Fatalf("expected 4 table paths for single switch, got %d", len(tblPaths))
	}
}

func TestV2rSystemPortVoQStats_SystemPort(t *testing.T) {
	sdcfg.Init()
	restore := setupVoQMaps(t)
	defer restore()

	paths := []string{"COUNTERS_DB", "COUNTERS", "str2-7804-lc7-1|Asic0|Ethernet68", "VoQs"}
	tblPaths, err := v2rSystemPortVoQStats(paths)
	if err != nil {
		t.Fatalf("v2rSystemPortVoQStats returned error: %v", err)
	}
	// Only 2 VoQs match Ethernet68
	if len(tblPaths) != 2 {
		t.Fatalf("expected 2 table paths for system port, got %d", len(tblPaths))
	}

	sort.Slice(tblPaths, func(i, j int) bool {
		return tblPaths[i].jsonTableKey < tblPaths[j].jsonTableKey
	})

	if tblPaths[0].jsonTableKey != "str2-7804-lc7-1|Asic0|Ethernet68:0" {
		t.Errorf("jsonTableKey = %q, want str2-7804-lc7-1|Asic0|Ethernet68:0", tblPaths[0].jsonTableKey)
	}
	if tblPaths[1].jsonTableKey != "str2-7804-lc7-1|Asic0|Ethernet68:1" {
		t.Errorf("jsonTableKey = %q, want str2-7804-lc7-1|Asic0|Ethernet68:1", tblPaths[1].jsonTableKey)
	}
}

func TestV2rSystemPortVoQStats_NoMatch(t *testing.T) {
	sdcfg.Init()
	restore := setupVoQMaps(t)
	defer restore()

	// Query for a switch name that doesn't exist in the map
	paths := []string{"COUNTERS_DB", "COUNTERS", "nonexistent-switch", "VoQs"}
	tblPaths, err := v2rSystemPortVoQStats(paths)
	if err != nil {
		t.Fatalf("v2rSystemPortVoQStats returned error: %v", err)
	}
	if len(tblPaths) != 0 {
		t.Errorf("expected 0 table paths for non-existent switch, got %d", len(tblPaths))
	}
}

func TestV2rSystemPortVoQStats_SystemPortNoMatch(t *testing.T) {
	sdcfg.Init()
	restore := setupVoQMaps(t)
	defer restore()

	paths := []string{"COUNTERS_DB", "COUNTERS", "str2-7804-lc7-1|Asic0|Ethernet99", "VoQs"}
	tblPaths, err := v2rSystemPortVoQStats(paths)
	if err != nil {
		t.Fatalf("v2rSystemPortVoQStats returned error: %v", err)
	}
	if len(tblPaths) != 0 {
		t.Errorf("expected 0 table paths for non-existent system port, got %d", len(tblPaths))
	}
}

// --------------------------------------------------------------------------
// Tests for getVoQCountersMap (virtual_db.go) - uses miniredis
// --------------------------------------------------------------------------

func setupMiniredisForVoQ(t *testing.T) (*miniredis.Miniredis, func()) {
	t.Helper()

	mr := miniredis.RunT(t)

	// Populate COUNTERS_VOQ_NAME_MAP hash
	mr.HSet("COUNTERS_VOQ_NAME_MAP", "str2-7804-lc7-1|Asic0|Ethernet84:0", "oid:0x160000000091c")
	mr.HSet("COUNTERS_VOQ_NAME_MAP", "str2-7804-lc7-1|Asic0|Ethernet84:1", "oid:0x160000000091d")

	// Populate COUNTERS:oid:* keys so the second pass can find them
	mr.Set("COUNTERS:oid:0x160000000091c", "exists")
	mr.Set("COUNTERS:oid:0x160000000091d", "exists")

	origTarget := Target2RedisDb
	ns, _ := sdcfg.GetDbDefaultNamespace()
	Target2RedisDb = map[string]map[string]*redis.Client{
		ns: {
			"COUNTERS_DB": redis.NewClient(&redis.Options{Addr: mr.Addr()}),
		},
	}

	return mr, func() {
		Target2RedisDb = origTarget
	}
}

func TestGetVoQCountersMap_Success(t *testing.T) {
	sdcfg.Init()
	_, restore := setupMiniredisForVoQ(t)
	defer restore()

	counterMap, oidNsMap, err := getVoQCountersMap("COUNTERS_VOQ_NAME_MAP")
	if err != nil {
		t.Fatalf("getVoQCountersMap returned error: %v", err)
	}
	if len(counterMap) != 2 {
		t.Errorf("expected 2 entries in counterMap, got %d", len(counterMap))
	}
	if counterMap["str2-7804-lc7-1|Asic0|Ethernet84:0"] != "oid:0x160000000091c" {
		t.Errorf("unexpected OID for Ethernet84:0: %v", counterMap["str2-7804-lc7-1|Asic0|Ethernet84:0"])
	}
	if len(oidNsMap) != 2 {
		t.Errorf("expected 2 OID-namespace mappings, got %d", len(oidNsMap))
	}
}

func TestGetVoQCountersMap_EmptyTable(t *testing.T) {
	sdcfg.Init()
	mr := miniredis.RunT(t)

	origTarget := Target2RedisDb
	ns, _ := sdcfg.GetDbDefaultNamespace()
	Target2RedisDb = map[string]map[string]*redis.Client{
		ns: {
			"COUNTERS_DB": redis.NewClient(&redis.Options{Addr: mr.Addr()}),
		},
	}
	defer func() { Target2RedisDb = origTarget }()

	counterMap, oidNsMap, err := getVoQCountersMap("COUNTERS_VOQ_NAME_MAP")
	if err != nil {
		t.Fatalf("getVoQCountersMap returned error: %v", err)
	}
	if len(counterMap) != 0 {
		t.Errorf("expected 0 entries, got %d", len(counterMap))
	}
	if len(oidNsMap) != 0 {
		t.Errorf("expected 0 OID-namespace mappings, got %d", len(oidNsMap))
	}
}

func TestGetVoQCountersMap_NoRedisClients(t *testing.T) {
	sdcfg.Init()

	origTarget := Target2RedisDb
	Target2RedisDb = map[string]map[string]*redis.Client{}
	defer func() { Target2RedisDb = origTarget }()

	// With no Redis clients, GetRedisClientsForDb returns empty map
	counterMap, oidNsMap, err := getVoQCountersMap("COUNTERS_VOQ_NAME_MAP")
	if err != nil {
		t.Fatalf("getVoQCountersMap returned error: %v", err)
	}
	if len(counterMap) != 0 {
		t.Errorf("expected 0 entries, got %d", len(counterMap))
	}
	if len(oidNsMap) != 0 {
		t.Errorf("expected 0 OID-namespace mappings, got %d", len(oidNsMap))
	}
}

// --------------------------------------------------------------------------
// Tests for initCountersVoQNameMap (virtual_db.go)
// --------------------------------------------------------------------------

func TestInitCountersVoQNameMap_Success(t *testing.T) {
	sdcfg.Init()
	_, restore := setupMiniredisForVoQ(t)
	defer restore()

	origVoQMap := countersVoQNameMap
	origVoQOidMap := countersVoQOidNamespaceMap
	countersVoQNameMap = make(map[string]string)
	countersVoQOidNamespaceMap = make(map[string]string)
	defer func() {
		countersVoQNameMap = origVoQMap
		countersVoQOidNamespaceMap = origVoQOidMap
	}()

	err := initCountersVoQNameMap()
	if err != nil {
		t.Fatalf("initCountersVoQNameMap returned error: %v", err)
	}
	if len(countersVoQNameMap) != 2 {
		t.Errorf("expected 2 VoQ entries, got %d", len(countersVoQNameMap))
	}
}

func TestInitCountersVoQNameMap_AlreadyPopulated(t *testing.T) {
	sdcfg.Init()

	origVoQMap := countersVoQNameMap
	countersVoQNameMap = map[string]string{"existing": "oid:0x1"}
	defer func() { countersVoQNameMap = origVoQMap }()

	// Should not re-initialize if already populated
	err := initCountersVoQNameMap()
	if err != nil {
		t.Fatalf("initCountersVoQNameMap returned error: %v", err)
	}
	if len(countersVoQNameMap) != 1 {
		t.Errorf("should not have re-initialized, expected 1, got %d", len(countersVoQNameMap))
	}
}

// --------------------------------------------------------------------------
// Tests for v2rSystemPortVoQStats with invalid VoQ names (error branches)
// --------------------------------------------------------------------------

func TestV2rSystemPortVoQStats_InvalidVoQName_Wildcard(t *testing.T) {
	sdcfg.Init()
	restore := setupVoQMaps(t)
	defer restore()

	// Add an invalid VoQ name (missing colon separator)
	countersVoQNameMap["badformat-no-pipes"] = "oid:0xBAD1"

	paths := []string{"COUNTERS_DB", "COUNTERS", "SwitchName*", "VoQs"}
	tblPaths, err := v2rSystemPortVoQStats(paths)
	if err != nil {
		t.Fatalf("v2rSystemPortVoQStats returned error: %v", err)
	}
	// Should still get 4 valid paths (the invalid one is skipped)
	if len(tblPaths) != 4 {
		t.Errorf("expected 4 valid table paths, got %d", len(tblPaths))
	}
}

func TestV2rSystemPortVoQStats_InvalidVoQName_SystemPort(t *testing.T) {
	sdcfg.Init()
	restore := setupVoQMaps(t)
	defer restore()

	// Add an invalid VoQ name that contains the requested system port string
	countersVoQNameMap["str2-7804-lc7-1|Asic0|Ethernet68-nocoIndex"] = "oid:0xBAD2"

	paths := []string{"COUNTERS_DB", "COUNTERS", "str2-7804-lc7-1|Asic0|Ethernet68", "VoQs"}
	tblPaths, err := v2rSystemPortVoQStats(paths)
	if err != nil {
		t.Fatalf("v2rSystemPortVoQStats returned error: %v", err)
	}
	// Should get 2 valid paths (Ethernet68:0 and Ethernet68:1), the invalid one is skipped
	if len(tblPaths) != 2 {
		t.Errorf("expected 2 valid table paths, got %d", len(tblPaths))
	}
}

func TestV2rSystemPortVoQStats_InvalidVoQName_SingleSwitch(t *testing.T) {
	sdcfg.Init()
	restore := setupVoQMaps(t)
	defer restore()

	// Add an invalid VoQ name that starts with "str2-7804-lc7-1|" but has bad format
	countersVoQNameMap["str2-7804-lc7-1|BadNoThirdPipe"] = "oid:0xBAD3"

	paths := []string{"COUNTERS_DB", "COUNTERS", "str2-7804-lc7-1", "VoQs"}
	tblPaths, err := v2rSystemPortVoQStats(paths)
	if err != nil {
		t.Fatalf("v2rSystemPortVoQStats returned error: %v", err)
	}
	// Should get 4 valid paths (the invalid one is skipped)
	if len(tblPaths) != 4 {
		t.Errorf("expected 4 valid table paths, got %d", len(tblPaths))
	}
}

// --------------------------------------------------------------------------
// Tests for trie findNode dead-end (trie.go line 144)
// --------------------------------------------------------------------------

func TestTrieBacktracking_DeadEndAtNonTerminal(t *testing.T) {
	sdcfg.Init()

	// Search for a path that matches "*" wildcard at COUNTERS level but
	// "*" node is non-terminal (it has "VoQs" child but no "" terminal).
	// This exercises line 144 (return nil when keys==0 at non-terminal).
	keys := []string{"COUNTERS_DB", "COUNTERS", "random-switch-name"}
	_, ok := v2rTrie.Find(keys)
	if ok {
		t.Fatal("expected trie NOT to find path ending at non-terminal node")
	}
}
