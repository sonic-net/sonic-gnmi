package common_utils

import (
	"fmt"
	"testing"
	"time"
	"github.com/sonic-net/sonic-gnmi/swsscommon"
)

func VerifyData(t *testing.T, table string, key string, field string, expectedValue string, expectedErr string) error {
	dbSubscriber := GetDbSubscriber()
	value, err := dbSubscriber.GetData(table, key, field)
	if err != nil && fmt.Sprint(err) != expectedErr {
		return fmt.Errorf("Fail to verify data, error: %v", err)
	} else if err == nil && expectedErr != "" {
		return fmt.Errorf("Fail to verify data, expected error: %v", expectedErr)
	}

	if value != expectedValue {
		return fmt.Errorf("Fail to verify data, value: %s", value)
	}

	return nil
}

func TestDbSubscriberRoutine(t *testing.T) {
	// prepare data according to design doc
	// Design doc: https://github.com/sonic-net/SONiC/blob/master/doc/smart-switch/ip-address-assigment/smart-switch-ip-address-assignment.md?plain=1

	if !swsscommon.SonicDBConfigIsInit() {
		swsscommon.SonicDBConfigInitialize()
	}

	var configDb = swsscommon.NewDBConnector("CONFIG_DB", uint(0), true)
	configDb.Flushdb()
	
	var midPlaneTable = swsscommon.NewTable(configDb, "MID_PLANE_BRIDGE")
	var dpusTable = swsscommon.NewTable(configDb, "DPUS")
	var dhcpPortTable = swsscommon.NewTable(configDb, "DHCP_SERVER_IPV4_PORT")

	ResetDbSubscriber()
	dbSubscriber := GetDbSubscriber()
	dbSubscriber.InitializeDbSubscriber()
	value, err := dbSubscriber.GetData("MID_PLANE_BRIDGE", "GLOBAL", "bridge")
	if err == nil {
		t.Errorf("Should not have data: %s", value)
	}

	midPlaneTable.Hset("GLOBAL", "bridge", "bridge_midplane")
	midPlaneTable.Hset("GLOBAL", "testfield", "testvalue")
	dpusTable.Hset("dpu0", "midplane_interface", "dpu0")
	dhcpPortTable.Hset("bridge_midplane|dpu0", "invalidfield", "")

	// wait dbSubscriber update
	time.Sleep(1 * time.Second)  
	
	err = VerifyData(t, "MID_PLANE_BRIDGE", "GLOBAL", "bridge", "bridge_midplane", "")
	if err != nil {
		t.Errorf("VerifyData failed: %s", err)
	}
	
	err = VerifyData(t, "MID_PLANE_BRIDGE", "GLOBAL", "testfield", "testvalue", "")
	if err != nil {
		t.Errorf("VerifyData failed: %s", err)
	}

	err = VerifyData(t, "DPUS", "dpu0", "midplane_interface", "dpu0", "")
	if err != nil {
		t.Errorf("VerifyData failed: %s", err)
	}

	err = VerifyData(t, "DHCP_SERVER_IPV4_PORT", "bridge_midplane|dpu0", "invalidfield", "", "")
	if err != nil {
		t.Errorf("VerifyData failed: %s", err)
	}

	// change data and check again
	midPlaneTable.Hset("GLOBAL", "bridge", "bridge_midplane2")
	time.Sleep(1 * time.Second)  
	err = VerifyData(t, "MID_PLANE_BRIDGE", "GLOBAL", "bridge", "bridge_midplane2", "")
	if err != nil {
		t.Errorf("VerifyData failed: %s", err)
	}
	
	err = VerifyData(t, "MID_PLANE_BRIDGE", "GLOBAL", "testfield", "testvalue", "")
	if err != nil {
		t.Errorf("VerifyData failed: %s", err)
	}

	// test delete field but still field remaining
	midPlaneTable.Hdel("GLOBAL", "bridge")
	time.Sleep(1 * time.Second)  
	err = VerifyData(t, "MID_PLANE_BRIDGE", "GLOBAL", "bridge", "", "Field: bridge does not exist in Table: MID_PLANE_BRIDGE, Key: GLOBAL")
	if err != nil {
		t.Errorf("VerifyData failed: %s", err)
	}
	
	err = VerifyData(t, "MID_PLANE_BRIDGE", "GLOBAL", "testfield", "testvalue", "")
	if err != nil {
		t.Errorf("VerifyData failed: %s", err)
	}

	// test delete field and no field remaining
	dpusTable.Hdel("dpu0", "midplane_interface")
	time.Sleep(1 * time.Second)  
	err = VerifyData(t, "DPUS", "dpu0", "midplane_interface", "", "Key: dpu0 does not exist in Table: DPUS")
	if err != nil {
		t.Errorf("VerifyData failed: %s", err)
	}

	dbSubscriber.stopRoutine()
}