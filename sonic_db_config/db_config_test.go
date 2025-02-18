package dbconfig

import (
	"fmt"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/sonic-net/sonic-gnmi/swsscommon"
	"github.com/sonic-net/sonic-gnmi/test_utils"
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

// Test db_config API with multiple database
func TestGetDbMultiInstance(t *testing.T) {
	Init()
	err := test_utils.SetupMultiInstance()
	if err != nil {
		t.Fatalf("error Setting up MultiInstance files with err %T", err)
	}

	/* https://www.gopherguides.com/articles/test-cleanup-in-go-1-14*/
	t.Cleanup(func() {
		if err := test_utils.CleanUpMultiInstance(); err != nil {
			t.Fatalf("error Cleaning up MultiInstance files with err %T", err)
		}
	})
	dbkey := swsscommon.NewSonicDBKey()
	defer swsscommon.DeleteSonicDBKey(dbkey)
	dbkey.SetContainerName("dpu0")
	t.Run("Id", func(t *testing.T) {
		db_id, _ := GetDbIdByDBKey("DPU_APPL_DB", dbkey)
		if db_id != 15 {
			t.Fatalf(`Id("") = %d, want 4, error`, db_id)
		}
	})
	t.Run("Sock", func(t *testing.T) {
		sock_path, _ := GetDbSockByDBKey("CONFIG_DB", dbkey)
		if sock_path != "/var/run/redis/redis.sock" {
			t.Fatalf(`Sock("") = %q, want "/var/run/redis/redis.sock", error`, sock_path)
		}
	})
	t.Run("AllInstances", func(t *testing.T) {
		dbkey_list, _ := GetDbAllInstances()
		for _, dbkey := range dbkey_list {
			defer swsscommon.DeleteSonicDBKey(dbkey)
		}
		if len(dbkey_list) != 2 {
			t.Fatalf(`AllInstances("") = %q, want "2", error`, len(dbkey_list))
		}
		default_container := SONIC_DEFAULT_CONTAINER
		container0 := dbkey_list[0].GetContainerName()
		container1 := dbkey_list[1].GetContainerName()
		if container0 == default_container {
			if container1 != "dpu0" {
				t.Fatalf(`AllInstances("") = %q %q, want default and dpu0, error`, container0, container1)
			}
		} else if container0 == "dpu0" {
			if container1 != default_container {
				t.Fatalf(`AllInstances("") = %q %q, want default and dpu0, error`, container0, container1)
			}
		} else {
			t.Fatalf(`AllInstances("") = %q %q, want default and dpu0, error`, container0, container1)
		}
	})
	t.Run("TcpAddr", func(t *testing.T) {
		tcp_addr, _ := GetDbTcpAddrByDBKey("CONFIG_DB", dbkey)
		if tcp_addr != "127.0.0.1:6379" {
			t.Fatalf(`TcpAddr("") = %q, want 127.0.0.1:6379, error`, tcp_addr)
		}
	})
	t.Run("AllAPI", func(t *testing.T) {
		Init()
		_, err = CheckDbMultiInstance()
		if err != nil {
			t.Fatalf(`err %v`, err)
		}
		Init()
		dbkey_list, err := GetDbNonDefaultInstances()
		if err != nil {
			t.Fatalf(`err %v`, err)
		}
		for _, dbkey := range dbkey_list {
			defer swsscommon.DeleteSonicDBKey(dbkey)
		}
		Init()
		_, err = GetDbListByDBKey(dbkey)
		if err != nil {
			t.Fatalf(`err %v`, err)
		}
		Init()
		_, err = GetDbSeparatorByDBKey("CONFIG_DB", dbkey)
		if err != nil {
			t.Fatalf(`err %v`, err)
		}
		Init()
		_, err = GetDbSockByDBKey("CONFIG_DB", dbkey)
		if err != nil {
			t.Fatalf(`err %v`, err)
		}
		Init()
		_, err = GetDbHostNameByDBKey("CONFIG_DB", dbkey)
		if err != nil {
			t.Fatalf(`err %v`, err)
		}
		Init()
		_, err = GetDbPortByDBKey("CONFIG_DB", dbkey)
		if err != nil {
			t.Fatalf(`err %v`, err)
		}
		Init()
		_, err = GetDbTcpAddrByDBKey("CONFIG_DB", dbkey)
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
		_, err = CheckDbMultiInstance()
		if err == nil || err.Error() != "Test api error" {
			t.Fatalf(`No expected error`)
		}
		Init()
		_, err = GetDbNonDefaultInstances()
		if err == nil || err.Error() != "Test api error" {
			t.Fatalf(`No expected error`)
		}
		Init()
		_, err = GetDbAllInstances()
		if err == nil || err.Error() != "Test api error" {
			t.Fatalf(`No expected error`)
		}
		Init()
		_, err = GetDbListByDBKey(dbkey)
		if err == nil || err.Error() != "Test api error" {
			t.Fatalf(`No expected error`)
		}
		Init()
		_, err = GetDbSeparatorByDBKey("CONFIG_DB", dbkey)
		if err == nil || err.Error() != "Test api error" {
			t.Fatalf(`No expected error`)
		}
		Init()
		_, err = GetDbIdByDBKey("CONFIG_DB", dbkey)
		if err == nil || err.Error() != "Test api error" {
			t.Fatalf(`No expected error`)
		}
		Init()
		_, err = GetDbSockByDBKey("CONFIG_DB", dbkey)
		if err == nil || err.Error() != "Test api error" {
			t.Fatalf(`No expected error`)
		}
		Init()
		_, err = GetDbHostNameByDBKey("CONFIG_DB", dbkey)
		if err == nil || err.Error() != "Test api error" {
			t.Fatalf(`No expected error`)
		}
		Init()
		_, err = GetDbPortByDBKey("CONFIG_DB", dbkey)
		if err == nil || err.Error() != "Test api error" {
			t.Fatalf(`No expected error`)
		}
		Init()
		_, err = GetDbTcpAddrByDBKey("CONFIG_DB", dbkey)
		if err == nil || err.Error() != "Test api error" {
			t.Fatalf(`No expected error`)
		}
	})
}

// Test db_config API with redis_bmp database
func TestGetBMPDbInstance(t *testing.T) {
	Init()
	err := test_utils.SetupMultiInstance()
	if err != nil {
		t.Fatalf("error Setting up MultiInstance files with err %T", err)
	}

	/* https://www.gopherguides.com/articles/test-cleanup-in-go-1-14*/
	t.Cleanup(func() {
		if err := test_utils.CleanUpMultiInstance(); err != nil {
			t.Fatalf("error Cleaning up MultiInstance files with err %T", err)
		}
	})
	dbkey := swsscommon.NewSonicDBKey()
	defer swsscommon.DeleteSonicDBKey(dbkey)
	dbkey.SetContainerName("redis_bmp")
	t.Run("Id", func(t *testing.T) {
		db_id, _ := GetDbIdByDBKey("BMP_STATE_DB", dbkey)
		if db_id != 20 {
			t.Fatalf(`Id("") = %d, want 20, error`, db_id)
		}
	})
	t.Run("Sock", func(t *testing.T) {
		sock_path, _ := GetDbSockByDBKey("BMP_STATE_DB", dbkey)
		if sock_path != "/var/run/redis/redis_bmp.sock" {
			t.Fatalf(`Sock("") = %q, want "/var/run/redis/redis_bmp.sock", error`, sock_path)
		}
	})
	t.Run("TcpAddr", func(t *testing.T) {
		tcp_addr, _ := GetDbTcpAddrByDBKey("BMP_STATE_DB", dbkey)
		if tcp_addr != "127.0.0.1:6400" {
			t.Fatalf(`TcpAddr("") = %q, want 127.0.0.1:6400, error`, tcp_addr)
		}
	})
	t.Run("AllAPI", func(t *testing.T) {
		Init()
		_, err = CheckDbMultiInstance()
		if err != nil {
			t.Fatalf(`err %v`, err)
		}
		Init()
		dbkey_list, err := GetDbNonDefaultInstances()
		if err != nil {
			t.Fatalf(`err %v`, err)
		}
		for _, dbkey := range dbkey_list {
			defer swsscommon.DeleteSonicDBKey(dbkey)
		}
		Init()
		_, err = GetDbListByDBKey(dbkey)
		if err != nil {
			t.Fatalf(`err %v`, err)
		}
		Init()
		_, err = GetDbSeparatorByDBKey("BMP_STATE_DB", dbkey)
		if err != nil {
			t.Fatalf(`err %v`, err)
		}
		Init()
		_, err = GetDbSockByDBKey("BMP_STATE_DB", dbkey)
		if err != nil {
			t.Fatalf(`err %v`, err)
		}
		Init()
		_, err = GetDbHostNameByDBKey("BMP_STATE_DB", dbkey)
		if err != nil {
			t.Fatalf(`err %v`, err)
		}
		Init()
		_, err = GetDbPortByDBKey("BMP_STATE_DB", dbkey)
		if err != nil {
			t.Fatalf(`err %v`, err)
		}
		Init()
		_, err = GetDbTcpAddrByDBKey("BMP_STATE_DB", dbkey)
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
		_, err = CheckDbMultiInstance()
		if err == nil || err.Error() != "Test api error" {
			t.Fatalf(`No expected error`)
		}
		Init()
		_, err = GetDbNonDefaultInstances()
		if err == nil || err.Error() != "Test api error" {
			t.Fatalf(`No expected error`)
		}
		Init()
		_, err = GetDbAllInstances()
		if err == nil || err.Error() != "Test api error" {
			t.Fatalf(`No expected error`)
		}
		Init()
		_, err = GetDbListByDBKey(dbkey)
		if err == nil || err.Error() != "Test api error" {
			t.Fatalf(`No expected error`)
		}
		Init()
		_, err = GetDbSeparatorByDBKey("BMP_STATE_DB", dbkey)
		if err == nil || err.Error() != "Test api error" {
			t.Fatalf(`No expected error`)
		}
		Init()
		_, err = GetDbIdByDBKey("BMP_STATE_DB", dbkey)
		if err == nil || err.Error() != "Test api error" {
			t.Fatalf(`No expected error`)
		}
		Init()
		_, err = GetDbSockByDBKey("BMP_STATE_DB", dbkey)
		if err == nil || err.Error() != "Test api error" {
			t.Fatalf(`No expected error`)
		}
		Init()
		_, err = GetDbHostNameByDBKey("BMP_STATE_DB", dbkey)
		if err == nil || err.Error() != "Test api error" {
			t.Fatalf(`No expected error`)
		}
		Init()
		_, err = GetDbPortByDBKey("BMP_STATE_DB", dbkey)
		if err == nil || err.Error() != "Test api error" {
			t.Fatalf(`No expected error`)
		}
		Init()
		_, err = GetDbTcpAddrByDBKey("BMP_STATE_DB", dbkey)
		if err == nil || err.Error() != "Test api error" {
			t.Fatalf(`No expected error`)
		}
	})
}

func TestMain(m *testing.M) {
	defer test_utils.MemLeakCheck()
	m.Run()
}
