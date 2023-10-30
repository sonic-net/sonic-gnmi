package dbconfig

import (
	"fmt"
	"github.com/agiledragon/gomonkey/v2"
	"github.com/sonic-net/sonic-gnmi/test_utils"
	"testing"
)

func TestGetDb(t *testing.T) {
	ns, _ := GetDbDefaultNamespace()
	t.Run("Id", func(t *testing.T) {
		db_id, _ := GetDbId("CONFIG_DB", ns)
		if db_id != 4 {
			t.Fatalf(`Id("") = %d, want 4, error`, db_id)
		}
	})
	t.Run("Sock", func(t *testing.T) {
		sock_path, _ := GetDbSock("CONFIG_DB", ns)
		if sock_path != "/var/run/redis/redis.sock" {
			t.Fatalf(`Sock("") = %q, want "/var/run/redis/redis.sock", error`, sock_path)
		}
	})
	t.Run("AllNamespaces", func(t *testing.T) {
		ns_list, _ := GetDbAllNamespaces()
		if len(ns_list) != 1 {
			t.Fatalf(`AllNamespaces("") = %q, want "1", error`, len(ns_list))
		}
		if ns_list[0] != ns {
			t.Fatalf(`AllNamespaces("") = %q, want default, error`, ns_list[0])
		}
	})
	t.Run("TcpAddr", func(t *testing.T) {
		tcp_addr, _ := GetDbTcpAddr("CONFIG_DB", ns)
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
	ns, _ := GetDbDefaultNamespace()
	t.Run("Id", func(t *testing.T) {
		db_id, _ := GetDbId("CONFIG_DB", "asic0")
		if db_id != 4 {
			t.Fatalf(`Id("") = %d, want 4, error`, db_id)
		}
	})
	t.Run("Sock", func(t *testing.T) {
		sock_path, _ := GetDbSock("CONFIG_DB", "asic0")
		if sock_path != "/var/run/redis0/redis.sock" {
			t.Fatalf(`Sock("") = %q, want "/var/run/redis0/redis.sock", error`, sock_path)
		}
	})
	t.Run("AllNamespaces", func(t *testing.T) {
		ns_list, _ := GetDbAllNamespaces()
		if len(ns_list) != 2 {
			t.Fatalf(`AllNamespaces("") = %q, want "2", error`, len(ns_list))
		}
		if !((ns_list[0] == ns && ns_list[1] == "asic0") || (ns_list[0] == "asic0" && ns_list[1] == ns)) {
			t.Fatalf(`AllNamespaces("") = %q %q, want default and asic0, error`, ns_list[0], ns_list[1])
		}
	})
	t.Run("TcpAddr", func(t *testing.T) {
		tcp_addr, _ := GetDbTcpAddr("CONFIG_DB", "asic0")
		if tcp_addr != "127.0.0.1:6379" {
			t.Fatalf(`TcpAddr("") = %q, want 127.0.0.1:6379, error`, tcp_addr)
		}
	})
	t.Run("AllAPI", func(t *testing.T) {
		Init()
		_, err = CheckDbMultiNamespace()
		if err != nil {
			t.Fatalf(`err %v`, err)
		}
		Init()
		_, err = GetDbNonDefaultNamespaces()
		if err != nil {
			t.Fatalf(`err %v`, err)
		}
		Init()
		_, err = GetDbList("asic0")
		if err != nil {
			t.Fatalf(`err %v`, err)
		}
		Init()
		_, err = GetDbSeparator("CONFIG_DB", "asic0")
		if err != nil {
			t.Fatalf(`err %v`, err)
		}
		Init()
		_, err = GetDbSock("CONFIG_DB", "asic0")
		if err != nil {
			t.Fatalf(`err %v`, err)
		}
		Init()
		_, err = GetDbHostName("CONFIG_DB", "asic0")
		if err != nil {
			t.Fatalf(`err %v`, err)
		}
		Init()
		_, err = GetDbPort("CONFIG_DB", "asic0")
		if err != nil {
			t.Fatalf(`err %v`, err)
		}
		Init()
		_, err = GetDbTcpAddr("CONFIG_DB", "asic0")
		if err != nil {
			t.Fatalf(`err %v`, err)
		}
		err = DbInit()
		if err != nil {
			t.Fatalf(`err %v`, err)
		}
	})
	t.Run("AllAPIError", func(t *testing.T) {
		mock1 := gomonkey.ApplyFunc(DbInit, func() (err error) {
			return fmt.Errorf("Test api error")
		})
		defer mock1.Reset()
		var err error
		Init()
		_, err = CheckDbMultiNamespace()
		if err == nil || err.Error() != "Test api error" {
			t.Fatalf(`No expected error`)
		}
		Init()
		_, err = GetDbNonDefaultNamespaces()
		if err == nil || err.Error() != "Test api error" {
			t.Fatalf(`No expected error`)
		}
		Init()
		_, err = GetDbAllNamespaces()
		if err == nil || err.Error() != "Test api error" {
			t.Fatalf(`No expected error`)
		}
		Init()
		_, err = GetDbList("asic0")
		if err == nil || err.Error() != "Test api error" {
			t.Fatalf(`No expected error`)
		}
		Init()
		_, err = GetDbSeparator("CONFIG_DB", "asic0")
		if err == nil || err.Error() != "Test api error" {
			t.Fatalf(`No expected error`)
		}
		Init()
		_, err = GetDbId("CONFIG_DB", "asic0")
		if err == nil || err.Error() != "Test api error" {
			t.Fatalf(`No expected error`)
		}
		Init()
		_, err = GetDbSock("CONFIG_DB", "asic0")
		if err == nil || err.Error() != "Test api error" {
			t.Fatalf(`No expected error`)
		}
		Init()
		_, err = GetDbHostName("CONFIG_DB", "asic0")
		if err == nil || err.Error() != "Test api error" {
			t.Fatalf(`No expected error`)
		}
		Init()
		_, err = GetDbPort("CONFIG_DB", "asic0")
		if err == nil || err.Error() != "Test api error" {
			t.Fatalf(`No expected error`)
		}
		Init()
		_, err = GetDbTcpAddr("CONFIG_DB", "asic0")
		if err == nil || err.Error() != "Test api error" {
			t.Fatalf(`No expected error`)
		}
	})
}
