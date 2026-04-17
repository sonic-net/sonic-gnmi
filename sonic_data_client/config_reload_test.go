package client

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/sonic-net/sonic-gnmi/common_utils"
	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"
	"github.com/stretchr/testify/assert"
)

func getFullConfigUpdateMessage(jsonContent string) []*gnmipb.Update {
	return []*gnmipb.Update{{
		Val: &gnmipb.TypedValue{
			Value: &gnmipb.TypedValue_JsonIetfVal{
				JsonIetfVal: []byte(jsonContent),
			},
		},
	}}
}

func TestSetFullConfig(t *testing.T) {
	tmpDir := t.TempDir()

	mock := gomonkey.ApplyFunc(RunPyCode, func(text string) error {
		return nil
	})
	defer mock.Reset()

	mock2 := gomonkey.ApplyMethod(reflect.TypeOf(&ssc.DbusClient{}), "ConfigReload", func(dbus *ssc.DbusClient, config string) error {
		common_utils.IncCounter(common_utils.DBUS_CONFIG_RELOAD)
		return nil
	})
	defer mock2.Reset()

	c := &MixedDbClient{workPath: tmpDir}
	update := getFullConfigUpdateMessage(`{"DEVICE_METADATA": {}}`)

	// technically we should pass in a delete path as well,
	// but that's only for determining the specific config
	// operation to invoke from the SetConfigDB() level.
	// delete isn't used internally in SetFullConfig
	err := c.SetFullConfig(nil, nil, update)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !c.ConfigReloadRequested() {
		t.Fatal("expected configReloadCallback to be registered after successful SetFullConfig")
	}

	var counts [int(common_utils.COUNTER_SIZE)]uint64
	preCount := counts[int(common_utils.DBUS_CONFIG_RELOAD)]
	if err = common_utils.GetMemCounters(&counts); err != nil {
		t.Fatalf("failed to get memory counters: %v", err)
	}
	c.MaybeRunCallback(true)
	if err = common_utils.GetMemCounters(&counts); err != nil {
		t.Fatalf("failed to get memory counters: %v", err)
	}

	actualCount := counts[int(common_utils.DBUS_CONFIG_RELOAD)]
	if actualCount != preCount+1 {
		t.Fatalf("expected DBUS_CONFIG_RELOAD to be %d, got: %v", preCount+1, actualCount)
	}
}

func TestSetFullConfigReloadFail(t *testing.T) {
	mock := gomonkey.ApplyFunc(RunPyCode, func(text string) error {
		return nil
	})
	defer mock.Reset()

	mock2 := gomonkey.ApplyMethod(reflect.TypeOf(&ssc.DbusClient{}), "ConfigReload", func(dbus *ssc.DbusClient, config string) error {
		return fmt.Errorf("config reload failed")
	})
	defer mock2.Reset()

	verifyClientSetup := func() *MixedDbClient {
		tmpDir := t.TempDir()

		c := &MixedDbClient{workPath: tmpDir}
		update := getFullConfigUpdateMessage(`{"DEVICE_METADATA": {}}`)

		err := c.SetFullConfig(nil, nil, update)
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		if !c.ConfigReloadRequested() {
			t.Fatal("expected configReloadCallback to be registered after successful SetFullConfig")
		}

		return c
	}

	c := verifyClientSetup()
	assert.NotPanics(t, func() { c.MaybeRunCallback(false) }) // gNMI auto restart disabled
	c = verifyClientSetup()
	assert.Panics(t, func() { c.MaybeRunCallback(true) }) // gNMI auto restart enabled
}

func TestSetFullConfigInvalidJson(t *testing.T) {
	c := &MixedDbClient{}
	update := getFullConfigUpdateMessage("")

	err := c.SetFullConfig(nil, nil, update)
	if err == nil {
		t.Fatal("expected error for empty IETF JSON value")
	}
	if err.Error() != "Value encoding is not IETF JSON" {
		t.Fatalf("unexpected error: '%v'", err)
	}
	if c.ConfigReloadRequested() {
		t.Fatal("callback shouldn't be registered when JSON validation fails")
	}
}

func TestSetFullConfigYangValidationFail(t *testing.T) {
	tmpDir := t.TempDir()

	mock := gomonkey.ApplyFunc(RunPyCode, func(text string) error {
		return fmt.Errorf("YANG model mismatch")
	})
	defer mock.Reset()

	c := &MixedDbClient{workPath: tmpDir}
	update := getFullConfigUpdateMessage(`{"BAD_TABLE": {}}`)

	err := c.SetFullConfig(nil, nil, update)
	if err == nil {
		t.Fatal("expected error when YANG validation fails")
	}
	if err.Error() != "Yang validation failed!" {
		t.Fatalf("unexpected error message: %v", err)
	}
	if c.ConfigReloadRequested() {
		t.Fatal("callback should NOT be registered when YANG validation fails")
	}
}
