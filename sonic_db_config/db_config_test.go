package dbconfig

import (
	"github.com/Azure/sonic-telemetry/test_utils"
	"testing"
)

func TestGetDb(t *testing.T) {
	t.Run("Id", func(t *testing.T) {
		db_id := GetDbId("CONFIG_DB", GetDbDefaultNamespace())
		if db_id != 4 {
			t.Fatalf(`Id("") = %d, want 4, error`, db_id)
		}
	})
	t.Run("Sock", func(t *testing.T) {
		sock_path := GetDbSock("CONFIG_DB", GetDbDefaultNamespace())
		if sock_path != "/var/run/redis/redis.sock" {
			t.Fatalf(`Sock("") = %q, want "/var/run/redis/redis.sock", error`, sock_path)
		}
	})
	t.Run("AllNamespaces", func(t *testing.T) {
		ns_list := GetDbAllNamespaces()
		if len(ns_list) != 1 {
			t.Fatalf(`AllNamespaces("") = %q, want "1", error`, len(ns_list))
		}
		if ns_list[0] != GetDbDefaultNamespace() {
			t.Fatalf(`AllNamespaces("") = %q, want default, error`, ns_list[0])
		}
	})
	t.Run("TcpAddr", func(t *testing.T) {
		tcp_addr := GetDbTcpAddr("CONFIG_DB", GetDbDefaultNamespace())
		if tcp_addr != "127.0.0.1:6379" {
			t.Fatalf(`TcpAddr("") = %q, want 127.0.0.1:6379, error`, tcp_addr)
		}
	})
}
func TestGetDbMultiNs(t *testing.T) {
	Init()
	err := test_utils.SetupMultiNamespace()
	if err != nil {
		t.Fatalf("error Setting up MultiNamespace files with err %T", err)
	}

	/* https://www.gopherguides.com/articles/test-cleanup-in-go-1-14*/
	t.Cleanup(func() {
		if err := test_utils.CleanUpMultiNamespace(); err != nil {
			t.Fatalf("error Cleaning up MultiNamespace files with err %T", err)

		}
	})
	t.Run("Id", func(t *testing.T) {
		db_id := GetDbId("CONFIG_DB", "asic0")
		if db_id != 4 {
			t.Fatalf(`Id("") = %d, want 4, error`, db_id)
		}
	})
	t.Run("Sock", func(t *testing.T) {
		sock_path := GetDbSock("CONFIG_DB", "asic0")
		if sock_path != "/var/run/redis0/redis.sock" {
			t.Fatalf(`Sock("") = %q, want "/var/run/redis0/redis.sock", error`, sock_path)
		}
	})
	t.Run("AllNamespaces", func(t *testing.T) {
		ns_list := GetDbAllNamespaces()
		if len(ns_list) != 2 {
			t.Fatalf(`AllNamespaces("") = %q, want "2", error`, len(ns_list))
		}
		if !((ns_list[0] == GetDbDefaultNamespace() && ns_list[1] == "asic0") || (ns_list[0] == "asic0" && ns_list[1] == GetDbDefaultNamespace())) {
			t.Fatalf(`AllNamespaces("") = %q %q, want default and asic0, error`, ns_list[0], ns_list[1])
		}
	})
	t.Run("TcpAddr", func(t *testing.T) {
		tcp_addr := GetDbTcpAddr("CONFIG_DB", "asic0")
		if tcp_addr != "127.0.0.1:6379" {
			t.Fatalf(`TcpAddr("") = %q, want 127.0.0.1:6379, error`, tcp_addr)
		}
	})
}
