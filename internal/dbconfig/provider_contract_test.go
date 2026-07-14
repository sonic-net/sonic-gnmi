package dbconfig

import (
	"slices"
	"testing"
)

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
		t.Errorf("GetDbAllNamespaces() = %q, want namespace %q", namespaces, contract.namespace)
	}

	databases, err := GetDbList(contract.namespace)
	if err != nil {
		t.Fatalf("GetDbList() error = %v", err)
	}
	if !slices.Contains(databases, contract.database) {
		t.Errorf("GetDbList() = %q, want database %q", databases, contract.database)
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
