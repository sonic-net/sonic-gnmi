//go:build e2e

package rpc_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/redis/go-redis/v9"
	sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
	"github.com/sonic-net/sonic-gnmi/tests/rpc/testutil"
)

// connectNativeRedis points both production role lookup and data reads at the
// same real Redis. It seeds no keys and restores the process-global client map.
func connectNativeRedis(t *testing.T, fixture *testutil.Fixture) {
	t.Helper()
	config := filepath.Join(t.TempDir(), "database_config.json")
	data, err := json.Marshal(map[string]any{
		"INSTANCES": map[string]any{"redis": map[string]any{"hostname": "127.0.0.1", "port": 6379, "unix_socket_path": fixture.Socket}},
		"DATABASES": map[string]any{"CONFIG_DB": map[string]any{"id": 4, "separator": "|", "instance": "redis"}},
		"VERSION":   "1.0",
	})
	check(t, err)
	check(t, os.WriteFile(config, data, 0600))
	t.Setenv("DB_CONFIG_PATH", config)
	check(t, sdcfg.Init())
	check(t, sdcfg.DbInit())
	t.Cleanup(func() { check(t, sdcfg.Init()) })
	previous := sdc.Target2RedisDb
	sdc.Target2RedisDb = map[string]map[string]*redis.Client{"": {"CONFIG_DB": fixture.DB}}
	t.Cleanup(func() { sdc.Target2RedisDb = previous })
}

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
