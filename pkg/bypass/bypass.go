// Package bypass provides fast-path direct ConfigDB writes for gNMI Set operations,
// bypassing DBUS/GCU validation when specific conditions are met.
package bypass

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/go-redis/redis"
	"github.com/golang/glog"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc/metadata"
)

const (
	// MetadataKeyBypassValidation is the gRPC metadata key for bypass validation
	MetadataKeyBypassValidation = "x-sonic-ss-bypass-validation"
)

// AllowedTables lists ConfigDB tables that can bypass validation (exact match)
var AllowedTables = map[string]bool{
	"VNET":               true,
	"VNET_ROUTE_TUNNEL":  true,
	"VLAN_SUB_INTERFACE": true,
	"ACL_RULE":           true,
	"BGP_PEER_RANGE":     true,
}

// AllowedSKUPrefixes lists HwSku prefixes that can use bypass validation
var AllowedSKUPrefixes = []string{
	"Cisco-8102",
	"Cisco-8101",
	"Cisco-8223",
}

// Default CONFIG_DB connection settings
// CONFIG_DB is database ID 4 in SONiC
const (
	defaultRedisAddr = "127.0.0.1:6379"
	configDbId       = 4
)

// getConfigDbClientFunc allows mocking in tests
var getConfigDbClientFunc = getConfigDbClientDefault

func getConfigDbClientDefault() (*redis.Client, error) {
	// Use environment variable if set, otherwise use default
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = defaultRedisAddr
	}

	client := redis.NewClient(&redis.Options{
		Network:     "tcp",
		Addr:        addr,
		Password:    "",
		DB:          configDbId,
		DialTimeout: 0,
	})
	return client, nil
}

// ShouldBypass checks if the request should use the fast bypass path.
// Returns true only if ALL conditions are met:
// 1. Metadata header x-sonic-ss-bypass-validation: true
// 2. SKU matches allowed prefixes
// 3. All target tables are in the allowlist
func ShouldBypass(ctx context.Context, prefix *gnmipb.Path, updates []*gnmipb.Update) bool {
	if !hasBypassHeader(ctx) {
		return false
	}
	if !checkSKU() {
		return false
	}
	if !checkAllowedTables(prefix, updates) {
		return false
	}
	return true
}

// ShouldBypassDelete checks if delete paths should use the fast bypass path.
// Same conditions as ShouldBypass but for delete operations.
func ShouldBypassDelete(ctx context.Context, prefix *gnmipb.Path, deletes []*gnmipb.Path) bool {
	if !hasBypassHeader(ctx) {
		return false
	}
	if !checkSKU() {
		return false
	}
	if !checkAllowedDeletePaths(prefix, deletes) {
		return false
	}
	return true
}

// checkAllowedDeletePaths verifies all delete paths target allowed tables
func checkAllowedDeletePaths(prefix *gnmipb.Path, deletes []*gnmipb.Path) bool {
	for _, path := range deletes {
		table := extractTable(prefix, path)
		if table == "" {
			glog.V(2).Infof("Bypass: could not extract table from delete path")
			return false
		}
		if !AllowedTables[table] {
			glog.V(2).Infof("Bypass: table %s not in allowlist for delete", table)
			return false
		}
	}
	return true
}

// hasBypassHeader checks gRPC metadata for bypass header
func hasBypassHeader(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return false
	}
	if values := md.Get(MetadataKeyBypassValidation); len(values) > 0 {
		return values[0] == "true"
	}
	return false
}

// checkSKU verifies device SKU matches one of the allowed prefixes
func checkSKU() bool {
	rclient, err := getConfigDbClientFunc()
	if err != nil {
		glog.V(2).Infof("Bypass: failed to get CONFIG_DB client: %v", err)
		return false
	}
	defer rclient.Close()

	hwsku, err := rclient.HGet("DEVICE_METADATA|localhost", "hwsku").Result()
	if err != nil {
		glog.V(2).Infof("Bypass: failed to read SKU: %v", err)
		return false
	}

	for _, prefix := range AllowedSKUPrefixes {
		if strings.HasPrefix(hwsku, prefix) {
			return true
		}
	}
	glog.V(2).Infof("Bypass: SKU %s does not match any allowed prefix", hwsku)
	return false
}

// checkAllowedTables verifies all target tables are in the allowlist
func checkAllowedTables(prefix *gnmipb.Path, updates []*gnmipb.Update) bool {
	for _, update := range updates {
		table := extractTable(prefix, update.GetPath())
		if table == "" {
			glog.V(2).Infof("Bypass: could not extract table from path")
			return false
		}
		if !AllowedTables[table] {
			glog.V(2).Infof("Bypass: table %s not in allowlist", table)
			return false
		}
	}
	return true
}

// extractTable extracts the table name from gNMI path
// Expected path format: CONFIG_DB/localhost/TABLE/KEY or just TABLE/KEY
func extractTable(prefix *gnmipb.Path, path *gnmipb.Path) string {
	var elems []*gnmipb.PathElem
	if prefix != nil {
		elems = append(elems, prefix.GetElem()...)
	}
	if path != nil {
		elems = append(elems, path.GetElem()...)
	}

	// Find the table name - it comes after CONFIG_DB/localhost or is the first element
	for i, elem := range elems {
		name := elem.GetName()
		if name == "CONFIG_DB" || name == "localhost" {
			continue
		}
		// First non-CONFIG_DB/localhost element is the table
		if i < len(elems) {
			return name
		}
	}
	return ""
}

// Apply executes the bypass write directly to ConfigDB
// Returns nil on success, error on failure
func Apply(ctx context.Context, prefix *gnmipb.Path, updates []*gnmipb.Update) error {
	rclient, err := getConfigDbClientFunc()
	if err != nil {
		return fmt.Errorf("bypass: failed to get CONFIG_DB client: %v", err)
	}
	defer rclient.Close()

	for _, update := range updates {
		table, key, field := parsePath(prefix, update.GetPath())
		if table == "" {
			return fmt.Errorf("bypass: invalid path, cannot extract table")
		}

		val := update.GetVal()
		jsonVal := val.GetJsonIetfVal()

		// Bulk table update: key is empty, JSON contains multiple entries
		// Path: CONFIG_DB/localhost/TABLE
		// JSON: {"entryKey1": {"field": "value"}, "entryKey2": {...}}
		if key == "" {
			if len(jsonVal) == 0 {
				return fmt.Errorf("bypass: bulk update requires JSON value")
			}
			var bulkData map[string]map[string]interface{}
			if err := json.Unmarshal(jsonVal, &bulkData); err != nil {
				return fmt.Errorf("bypass: failed to unmarshal bulk JSON: %v", err)
			}
			for entryKey, entryFields := range bulkData {
				redisKey := table + "|" + entryKey
				fields := make(map[string]interface{})
				for k, v := range entryFields {
					fields[k] = fmt.Sprintf("%v", v)
				}
				if _, err := rclient.HMSet(redisKey, fields).Result(); err != nil {
					return fmt.Errorf("bypass: HMSet failed for %s: %v", redisKey, err)
				}
				glog.V(2).Infof("Bypass: wrote %s with %d fields", redisKey, len(fields))
			}
			continue
		}

		// Single entry update: path has TABLE/KEY
		redisKey := table + "|" + key

		// Handle JSON IETF value
		if len(jsonVal) > 0 {
			var data map[string]interface{}
			if err := json.Unmarshal(jsonVal, &data); err != nil {
				return fmt.Errorf("bypass: failed to unmarshal JSON: %v", err)
			}

			fields := make(map[string]interface{})
			for k, v := range data {
				fields[k] = fmt.Sprintf("%v", v)
			}
			if _, err := rclient.HMSet(redisKey, fields).Result(); err != nil {
				return fmt.Errorf("bypass: HMSet failed for %s: %v", redisKey, err)
			}
			glog.V(2).Infof("Bypass: wrote %s with %d fields", redisKey, len(fields))
			continue
		}

		// Handle scalar value for single field update
		if field != "" {
			strVal := ""
			if v := val.GetStringVal(); v != "" {
				strVal = v
			} else if v := val.GetIntVal(); v != 0 {
				strVal = fmt.Sprintf("%d", v)
			} else if v := val.GetUintVal(); v != 0 {
				strVal = fmt.Sprintf("%d", v)
			}
			if _, err := rclient.HSet(redisKey, field, strVal).Result(); err != nil {
				return fmt.Errorf("bypass: HSet failed for %s.%s: %v", redisKey, field, err)
			}
			glog.V(2).Infof("Bypass: wrote %s.%s = %s", redisKey, field, strVal)
		}
	}

	return nil
}

// Delete executes bypass delete directly to ConfigDB
// Returns nil on success, error on failure
func Delete(ctx context.Context, prefix *gnmipb.Path, deletes []*gnmipb.Path) error {
	rclient, err := getConfigDbClientFunc()
	if err != nil {
		return fmt.Errorf("bypass: failed to get CONFIG_DB client: %v", err)
	}
	defer rclient.Close()

	for _, path := range deletes {
		table, key, _ := parsePath(prefix, path)
		if table == "" || key == "" {
			return fmt.Errorf("bypass: invalid delete path, cannot extract table/key")
		}

		redisKey := table + "|" + key
		if _, err := rclient.Del(redisKey).Result(); err != nil {
			return fmt.Errorf("bypass: Del failed for %s: %v", redisKey, err)
		}
		glog.V(2).Infof("Bypass: deleted %s", redisKey)
	}

	return nil
}

// parsePath extracts table, key, and optional field from gNMI path
func parsePath(prefix *gnmipb.Path, path *gnmipb.Path) (table, key, field string) {
	var elems []*gnmipb.PathElem
	if prefix != nil {
		elems = append(elems, prefix.GetElem()...)
	}
	if path != nil {
		elems = append(elems, path.GetElem()...)
	}

	// Skip CONFIG_DB and localhost
	var parts []string
	for _, elem := range elems {
		name := elem.GetName()
		if name == "CONFIG_DB" || name == "localhost" {
			continue
		}
		parts = append(parts, name)
	}

	if len(parts) >= 1 {
		table = parts[0]
	}
	if len(parts) >= 2 {
		key = decodeJsonPointer(parts[1])
	}
	if len(parts) >= 3 {
		field = parts[2]
	}
	return
}

// decodeJsonPointer decodes JSON Pointer escaping (RFC 6901)
// ~1 -> /
// ~0 -> ~
func decodeJsonPointer(s string) string {
	s = strings.ReplaceAll(s, "~1", "/")
	s = strings.ReplaceAll(s, "~0", "~")
	return s
}
