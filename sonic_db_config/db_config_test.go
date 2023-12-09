package dbconfig

import (
	"os"
	"testing"

	"github.com/sonic-net/sonic-gnmi/test_utils"
)

func TestGetDb(t *testing.T) {
	t.Run("Id", func(t *testing.T) {
		db_id := GetDbId("CONFIG_DB", GetDbDefaultInstance())
		if db_id != 4 {
			t.Fatalf(`Id("") = %d, want 4, error`, db_id)
		}
	})
	t.Run("Sock", func(t *testing.T) {
		sock_path := GetDbSock("CONFIG_DB", GetDbDefaultInstance())
		if sock_path != "/var/run/redis/redis.sock" {
			t.Fatalf(`Sock("") = %q, want "/var/run/redis/redis.sock", error`, sock_path)
		}
	})
	t.Run("AllNamespaces", func(t *testing.T) {
		ns_list := GetDbAllInstances()
		if len(ns_list) != 1 {
			t.Fatalf(`AllNamespaces("") = %q, want "1", error`, len(ns_list))
		}
		if ns_list[0] != GetDbDefaultInstance() {
			t.Fatalf(`AllNamespaces("") = %q, want default, error`, ns_list[0])
		}
	})
	t.Run("TcpAddr", func(t *testing.T) {
		tcp_addr := GetDbTcpAddr("CONFIG_DB", GetDbDefaultInstance())
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
		ns_list := GetDbAllInstances()
		if len(ns_list) != 2 {
			t.Fatalf(`AllNamespaces("") = %q, want "2", error`, len(ns_list))
		}
		if !((ns_list[0] == GetDbDefaultInstance() && ns_list[1] == "asic0") || (ns_list[0] == "asic0" && ns_list[1] == GetDbDefaultInstance())) {
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

func TestGetDbMultiDPU(t *testing.T) {
	Init()
	err := test_utils.SetupMultiDPU()
	if err != nil {
		t.Fatalf("error Setting up MultiDPU files with err %T", err)
	}

	/* https://www.gopherguides.com/articles/test-cleanup-in-go-1-14*/
	t.Cleanup(func() {
		if err := test_utils.CleanUpMultiDPU(); err != nil {
			t.Fatalf("error Cleaning up MultiDPU files with err %T", err)

		}
	})
	t.Run("CONFIG_DB Id", func(t *testing.T) {
		db_id := GetDbId("CONFIG_DB", "dpu0")
		if db_id != 4 {
			t.Fatalf(`Id("") = %d, want 4, error`, db_id)
		}
	})
	t.Run("DPU_APPL_DB Id", func(t *testing.T) {
		db_id := GetDbId("DPU_APPL_DB", "dpu0")
		if db_id != 15 {
			t.Fatalf(`Id("") = %d, want 15, error`, db_id)
		}
	})
	t.Run("Sock", func(t *testing.T) {
		sock_path := GetDbSock("CONFIG_DB", "dpu0")
		exp_path := "/var/run/redis/redis.sock"
		if sock_path != exp_path {
			t.Fatalf(`Sock("") = %q, want %s, error`, sock_path, exp_path)
		}
	})
	t.Run("AllDPU", func(t *testing.T) {
		ns_list := GetDbAllInstances()
		if len(ns_list) != 2 {
			t.Fatalf(`AllDPU("") = %q, want "2", error %v`, len(ns_list), ns_list)
		}
		if !((ns_list[0] == GetDbDefaultInstance() && ns_list[1] == "dpu0") || (ns_list[0] == "dpu0" && ns_list[1] == GetDbDefaultInstance())) {
			t.Fatalf(`AllDPU("") = %q %q, want default and dpu0, error`, ns_list[0], ns_list[1])
		}
	})
	t.Run("TcpAddr", func(t *testing.T) {
		tcp_addr := GetDbTcpAddr("CONFIG_DB", "dpu0")
		if tcp_addr != "127.0.0.1:6379" {
			t.Fatalf(`TcpAddr("") = %q, want 127.0.0.1:6379, error`, tcp_addr)
		}
	})
}

func TestOverrideDbConfigFile(t *testing.T) {
	Init()
	// Override database_config.json path to a garbage value by setting
	// env DB_CONFIG_PATH and verify that GetDbId() panics
	if err := os.Setenv("DB_CONFIG_PATH", "/tmp/.unknown_database_config_file.json"); err != nil {
		t.Fatalf("os.Setenv failed: %v", err)
	}
	t.Cleanup(func() {
		os.Unsetenv("DB_CONFIG_PATH")
		Init()
	})
	defer func() {
		r := recover()
		if err, _ := r.(error); !os.IsNotExist(err) {
			t.Fatalf("Unexpected panic: %v", r)
		}
	}()
	_ = GetDbId("CONFIG_DB", GetDbDefaultInstance())
	t.Fatal("GetDbId() should have paniced")
}
