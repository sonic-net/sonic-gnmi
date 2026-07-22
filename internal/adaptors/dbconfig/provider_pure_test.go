//go:build pure

package dbconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestPureProviderReadsDatabaseConfig(t *testing.T) {
	usePureConfig(t, filepath.Join("testdata", "database_config.json"))
	runProviderContract(t, providerContract{
		database:  "CONFIG_DB",
		namespace: DefaultNamespace,
		id:        42,
		separator: "~",
		socket:    "/tmp/standalone-redis.sock",
		address:   "db.example:6388",
	})
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
		t.Errorf("GetDbAllNamespaces() = %v, want default namespace", namespaces)
	}

	nonDefault, err := GetDbNonDefaultNamespaces()
	if err != nil {
		t.Fatalf("GetDbNonDefaultNamespaces() error = %v", err)
	}
	if len(nonDefault) != 0 {
		t.Errorf("GetDbNonDefaultNamespaces() = %v, want none", nonDefault)
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
		t.Errorf("GetDbList() = %v, want %v", databases, wantDatabases)
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
	usePureConfig(t, filepath.Join("testdata", "database_config.json"))
	globalConfigFile = filepath.Join("..", "..", "..", "testdata", "database_global.json")

	err := DbInit()
	if err == nil || !strings.Contains(err.Error(), "global database configuration") {
		t.Errorf("DbInit() error = %v, want unsupported global configuration error", err)
	}
}

func TestPureProviderRejectsIncludes(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "database_config.json")
	if err := os.WriteFile(configFile, []byte(`{"INCLUDES":["database_config.json"]}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	usePureConfig(t, configFile)

	err := DbInit()
	if err == nil || !strings.Contains(err.Error(), "INCLUDES") {
		t.Errorf("DbInit() error = %v, want unsupported INCLUDES error", err)
	}
}

func TestDefaultTargetDoesNotInitializeProvider(t *testing.T) {
	usePureConfig(t, filepath.Join(t.TempDir(), "missing-database-config.json"))

	namespace, found, err := GetDbNamespaceFromTarget(DefaultNamespace)
	if err != nil {
		t.Fatalf("GetDbNamespaceFromTarget(default) error = %v", err)
	}
	if !found || namespace != DefaultNamespace {
		t.Errorf("GetDbNamespaceFromTarget(default) = %q, %t; want default, true", namespace, found)
	}
}

func usePureConfig(t *testing.T, path string) {
	t.Helper()
	databaseConfigFile = path
	globalConfigFile = filepath.Join(t.TempDir(), "missing-global-config.json")
	t.Cleanup(func() {
		databaseConfigFile = defaultDatabaseConfigFile
		globalConfigFile = defaultGlobalConfigFile
	})
	if err := Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
}

type providerContract struct {
	database  string
	namespace string
	id        int
	separator string
	socket    string
	address   string
}

func runProviderContract(t *testing.T, contract providerContract) {
	t.Helper()

	namespace, err := GetDbDefaultNamespace()
	if err != nil {
		t.Fatalf("GetDbDefaultNamespace() error = %v", err)
	}
	if namespace != contract.namespace {
		t.Errorf("GetDbDefaultNamespace() = %q, want %q", namespace, contract.namespace)
	}

	namespaces, err := GetDbAllNamespaces()
	if err != nil {
		t.Fatalf("GetDbAllNamespaces() error = %v", err)
	}
	if !slices.Contains(namespaces, contract.namespace) {
		t.Errorf("GetDbAllNamespaces() = %v, want namespace %q", namespaces, contract.namespace)
	}

	databases, err := GetDbList(contract.namespace)
	if err != nil {
		t.Fatalf("GetDbList() error = %v", err)
	}
	if !slices.Contains(databases, contract.database) {
		t.Errorf("GetDbList() = %v, want database %q", databases, contract.database)
	}

	id, err := GetDbId(contract.database, contract.namespace)
	if err != nil {
		t.Fatalf("GetDbId() error = %v", err)
	}
	if id != contract.id {
		t.Errorf("GetDbId() = %d, want %d", id, contract.id)
	}

	separator, err := GetDbSeparator(contract.database, contract.namespace)
	if err != nil {
		t.Fatalf("GetDbSeparator() error = %v", err)
	}
	if separator != contract.separator {
		t.Errorf("GetDbSeparator() = %q, want %q", separator, contract.separator)
	}

	socket, err := GetDbSock(contract.database, contract.namespace)
	if err != nil {
		t.Fatalf("GetDbSock() error = %v", err)
	}
	if socket != contract.socket {
		t.Errorf("GetDbSock() = %q, want %q", socket, contract.socket)
	}

	address, err := GetDbTcpAddr(contract.database, contract.namespace)
	if err != nil {
		t.Fatalf("GetDbTcpAddr() error = %v", err)
	}
	if address != contract.address {
		t.Errorf("GetDbTcpAddr() = %q, want %q", address, contract.address)
	}
}
