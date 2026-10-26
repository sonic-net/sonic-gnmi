package dbconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestProviderReadsDatabaseConfig(t *testing.T) {
	useConfig(t)

	if got, err := GetDbId("CONFIG_DB", ""); err != nil || got != 42 {
		t.Fatalf("GetDbId() = %d, %v; want 42, nil", got, err)
	}
	if got, err := GetDbSeparator("CONFIG_DB", ""); err != nil || got != "~" {
		t.Errorf("GetDbSeparator() = %q, %v; want ~, nil", got, err)
	}
	if got, err := GetDbSock("CONFIG_DB", ""); err != nil || got != "/tmp/standalone-redis.sock" {
		t.Errorf("GetDbSock() = %q, %v; want fixture socket, nil", got, err)
	}
	if got, err := GetDbHostName("CONFIG_DB", ""); err != nil || got != "db.example" {
		t.Errorf("GetDbHostName() = %q, %v; want db.example, nil", got, err)
	}
	if got, err := GetDbPort("CONFIG_DB", ""); err != nil || got != 6388 {
		t.Errorf("GetDbPort() = %d, %v; want 6388, nil", got, err)
	}
	if got, err := GetDbTcpAddr("CONFIG_DB", ""); err != nil || got != "db.example:6388" {
		t.Errorf("GetDbTcpAddr() = %q, %v; want db.example:6388, nil", got, err)
	}
	databases, err := GetDbList("")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(databases)
	if want := []string{"CONFIG_DB", "STATE_DB"}; !reflect.DeepEqual(databases, want) {
		t.Errorf("GetDbList() = %v, want %v", databases, want)
	}
	if got, err := GetDbAllNamespaces(); err != nil || !reflect.DeepEqual(got, []string{""}) {
		t.Errorf("GetDbAllNamespaces() = %v, %v; want default namespace, nil", got, err)
	}
	if got, err := GetDbNonDefaultNamespaces(); err != nil || len(got) != 0 {
		t.Errorf("GetDbNonDefaultNamespaces() = %v, %v; want none, nil", got, err)
	}
	if got, err := CheckDbMultiNamespace(); err != nil || got {
		t.Errorf("CheckDbMultiNamespace() = %t, %v; want false, nil", got, err)
	}
	if namespace, found, err := GetDbNamespaceFromTarget("asic0"); err != nil || found || namespace != "" {
		t.Errorf("GetDbNamespaceFromTarget(asic0) = %q, %t, %v; want empty, false, nil", namespace, found, err)
	}
	if _, err := GetDbId("UNKNOWN_DB", ""); err == nil {
		t.Error("GetDbId(UNKNOWN_DB) succeeded")
	}
	if _, err := GetDbId("CONFIG_DB", "asic0"); err == nil {
		t.Error("GetDbId(asic0) succeeded")
	}
}

func TestProviderConfigurationReset(t *testing.T) {
	config := useConfig(t)
	if got, err := GetDbId("CONFIG_DB", ""); err != nil || got != 42 {
		t.Fatalf("initial GetDbId() = %d, %v; want 42, nil", got, err)
	}
	data, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.ReplaceAll(string(data), `"id": 42`, `"id": 44`))
	if err := os.WriteFile(config, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := GetDbId("CONFIG_DB", ""); err != nil || got != 42 {
		t.Fatalf("cached GetDbId() = %d, %v; want 42, nil", got, err)
	}
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	if got, err := GetDbId("CONFIG_DB", ""); err != nil || got != 44 {
		t.Fatalf("reset GetDbId() = %d, %v; want 44, nil", got, err)
	}
}

func TestDefaultTargetDoesNotInitializeProvider(t *testing.T) {
	useConfig(t)
	t.Setenv("DB_CONFIG_PATH", filepath.Join(t.TempDir(), "missing.json"))
	if namespace, found, err := GetDbNamespaceFromTarget(""); err != nil || !found || namespace != "" {
		t.Errorf("GetDbNamespaceFromTarget(default) = %q, %t, %v; want default, true, nil", namespace, found, err)
	}
}

func useConfig(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "database_config.json"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "database_config.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DB_CONFIG_PATH", path)
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := Init(); err != nil {
			t.Error(err)
		}
	})
	return path
}
