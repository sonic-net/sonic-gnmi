//go:build pure

package dbconfig

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPureProviderReadsDatabaseConfig(t *testing.T) {
	usePureConfig(t, filepath.Join("testdata", "database_config.json"))

	id, err := GetDbId("CONFIG_DB", DefaultNamespace)
	if err != nil {
		t.Fatalf("GetDbId() error = %v", err)
	}
	if id != 42 {
		t.Errorf("GetDbId() = %d, want 42", id)
	}

	separator, err := GetDbSeparator("CONFIG_DB", DefaultNamespace)
	if err != nil {
		t.Fatalf("GetDbSeparator() error = %v", err)
	}
	if separator != "~" {
		t.Errorf("GetDbSeparator() = %q, want %q", separator, "~")
	}

	socket, err := GetDbSock("CONFIG_DB", DefaultNamespace)
	if err != nil {
		t.Fatalf("GetDbSock() error = %v", err)
	}
	if socket != "/tmp/standalone-redis.sock" {
		t.Errorf("GetDbSock() = %q, want %q", socket, "/tmp/standalone-redis.sock")
	}

	address, err := GetDbTcpAddr("CONFIG_DB", DefaultNamespace)
	if err != nil {
		t.Fatalf("GetDbTcpAddr() error = %v", err)
	}
	if address != "db.example:6388" {
		t.Errorf("GetDbTcpAddr() = %q, want %q", address, "db.example:6388")
	}
}

func TestPureProviderDescribesStandaloneNamespace(t *testing.T) {
	usePureConfig(t, filepath.Join("testdata", "database_config.json"))

	namespace, err := GetDbDefaultNamespace()
	if err != nil {
		t.Fatalf("GetDbDefaultNamespace() error = %v", err)
	}
	if namespace != DefaultNamespace {
		t.Errorf("GetDbDefaultNamespace() = %q, want %q", namespace, DefaultNamespace)
	}

	namespaces, err := GetDbAllNamespaces()
	if err != nil {
		t.Fatalf("GetDbAllNamespaces() error = %v", err)
	}
	if !reflect.DeepEqual(namespaces, []string{DefaultNamespace}) {
		t.Errorf("GetDbAllNamespaces() = %q, want default namespace", namespaces)
	}

	nonDefault, err := GetDbNonDefaultNamespaces()
	if err != nil {
		t.Fatalf("GetDbNonDefaultNamespaces() error = %v", err)
	}
	if len(nonDefault) != 0 {
		t.Errorf("GetDbNonDefaultNamespaces() = %q, want none", nonDefault)
	}

	multiNamespace, err := CheckDbMultiNamespace()
	if err != nil {
		t.Fatalf("CheckDbMultiNamespace() error = %v", err)
	}
	if multiNamespace {
		t.Error("CheckDbMultiNamespace() = true, want false")
	}

	databases, err := GetDbList(DefaultNamespace)
	if err != nil {
		t.Fatalf("GetDbList() error = %v", err)
	}
	wantDatabases := []string{"CONFIG_DB", "STATE_DB"}
	if !reflect.DeepEqual(databases, wantDatabases) {
		t.Errorf("GetDbList() = %q, want %q", databases, wantDatabases)
	}
}

func TestPureProviderRejectsUnknownDatabaseAndNamespace(t *testing.T) {
	usePureConfig(t, filepath.Join("testdata", "database_config.json"))

	if _, err := GetDbId("UNKNOWN_DB", DefaultNamespace); err == nil || !strings.Contains(err.Error(), `database "UNKNOWN_DB"`) {
		t.Errorf("GetDbId() error = %v, want unknown database error", err)
	}
	if _, err := GetDbId("CONFIG_DB", "asic0"); err == nil || !strings.Contains(err.Error(), `namespace "asic0"`) {
		t.Errorf("GetDbId() error = %v, want unsupported namespace error", err)
	}

	namespace, found, err := GetDbNamespaceFromTarget(DefaultNamespace)
	if err != nil {
		t.Fatalf("GetDbNamespaceFromTarget(default) error = %v", err)
	}
	if !found || namespace != DefaultNamespace {
		t.Errorf("GetDbNamespaceFromTarget(default) = %q, %t; want default, true", namespace, found)
	}

	namespace, found, err = GetDbNamespaceFromTarget("asic0")
	if err != nil {
		t.Fatalf("GetDbNamespaceFromTarget(asic0) error = %v", err)
	}
	if found || namespace != "" {
		t.Errorf("GetDbNamespaceFromTarget(asic0) = %q, %t; want empty, false", namespace, found)
	}
}

func TestPureProviderRejectsGlobalConfiguration(t *testing.T) {
	usePureConfig(t, filepath.Join("..", "..", "testdata", "database_global.json"))

	err := DbInit()
	if err == nil || !strings.Contains(err.Error(), "global database configuration") {
		t.Errorf("DbInit() error = %v, want unsupported global configuration error", err)
	}
}

func usePureConfig(t *testing.T, path string) {
	t.Helper()
	databaseConfigFile = path
	t.Cleanup(func() {
		databaseConfigFile = defaultDatabaseConfigFile
	})
	if err := Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
}
