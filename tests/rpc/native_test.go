//go:build e2e

package rpc_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
)

// connectNativeRoleStore supplies connection metadata to the existing SWSS role
// adapter. It creates no Redis keys. Keep native dependencies out of testutil.
func connectNativeRoleStore(t *testing.T, socket string) {
	t.Helper()
	config := filepath.Join(t.TempDir(), "database_config.json")
	data, err := json.Marshal(map[string]any{
		"INSTANCES": map[string]any{"redis": map[string]any{"hostname": "127.0.0.1", "port": 6379, "unix_socket_path": socket}},
		"DATABASES": map[string]any{"CONFIG_DB": map[string]any{"id": 4, "separator": "|", "instance": "redis"}},
		"VERSION":   "1.0",
	})
	check(t, err)
	check(t, os.WriteFile(config, data, 0600))
	t.Setenv("DB_CONFIG_PATH", config)
	check(t, sdcfg.Init())
	check(t, sdcfg.DbInit())
	t.Cleanup(func() { check(t, sdcfg.Init()) })
}

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
