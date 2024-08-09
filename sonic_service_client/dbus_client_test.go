package host_service

import (
	"testing"
	"reflect"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/godbus/dbus/v5"
)

func TestSystemBusNegative(t *testing.T) {
	client, err := NewDbusClient()
	if err != nil {
		t.Errorf("NewDbusClient failed: %v", err)
	}
	err = client.ConfigReload("abc")
	if err == nil {
		t.Errorf("SystemBus should fail")
	}
}

func TestGetFileStat(t *testing.T) {
	expectedResult := map[string]string{
		"path":          "/etc/sonic",
		"last_modified": "1609459200000000000", // Example timestamp
		"permissions":   "644",
		"size":          "1024",
		"umask":         "022",
	}
	
	// Mocking the DBus API to return the expected result
	mock1 := gomonkey.ApplyFunc(dbus.SystemBus, func() (conn *dbus.Conn, err error) {
		return &dbus.Conn{}, nil
	})
	defer mock1.Reset()
	mock2 := gomonkey.ApplyMethod(reflect.TypeOf(&dbus.Object{}), "Go", func(obj *dbus.Object, method string, flags dbus.Flags, ch chan *dbus.Call, args ...interface{}) *dbus.Call {
		if method != "org.SONiC.HostService.file.get_file_stat" {
			t.Errorf("Wrong method: %v", method)
		}
		ret := &dbus.Call{}
		ret.Err = nil
		ret.Body = make([]interface{}, 2)
		ret.Body[0] = int32(0) // Indicating success
		ret.Body[1] = expectedResult
		ch <- ret
		return &dbus.Call{}
	})
	defer mock2.Reset()

	client, err := NewDbusClient()
	if err != nil {
		t.Errorf("NewDbusClient failed: %v", err)
	}
	result, err := client.GetFileStat("/etc/sonic")
	if err != nil {
		t.Errorf("GetFileStat should pass: %v", err)
	}
	for key, value := range expectedResult {
		if result[key] != value {
			t.Errorf("Expected %s for key %s but got %s", value, key, result[key])
		}
	}
}

func TestGetFileStatNegative(t *testing.T) {
	errMsg := "This is the mock error message"

    // Mocking the DBus API to return an error
	mock1 := gomonkey.ApplyFunc(dbus.SystemBus, func() (conn *dbus.Conn, err error) {
		return &dbus.Conn{}, nil
	})
	defer mock1.Reset()

	mock2 := gomonkey.ApplyMethod(reflect.TypeOf(&dbus.Object{}), "Go", func(obj *dbus.Object, method string, flags dbus.Flags, ch chan *dbus.Call, args ...interface{}) *dbus.Call {
		if method != "org.SONiC.HostService.file.get_file_stat" {
			t.Errorf("Wrong method: %v", method)
		}
		ret := &dbus.Call{}
		ret.Err = nil
		ret.Body = make([]interface{}, 2)
		ret.Body[0] = int32(1) // Indicating failure
		ret.Body[1] = map[string]string{"error": errMsg}
		ch <- ret
		return &dbus.Call{}
	})
	defer mock2.Reset()

	client, err := NewDbusClient()
	if err != nil {
		t.Errorf("NewDbusClient failed: %v", err)
	}

	_, err = client.GetFileStat("/invalid/path")
	if err == nil {
		t.Errorf("GetFileStat should fail")
	}
	if err.Error() != errMsg {
		t.Errorf("Expected error message '%s' but got '%v'", errMsg, err)
	}
}

func TestConfigReload(t *testing.T) {
	mock1 := gomonkey.ApplyFunc(dbus.SystemBus, func() (conn *dbus.Conn, err error) {
		return &dbus.Conn{}, nil
	})
	defer mock1.Reset()
	mock2 := gomonkey.ApplyMethod(reflect.TypeOf(&dbus.Object{}), "Go", func(obj *dbus.Object, method string, flags dbus.Flags, ch chan *dbus.Call, args ...interface{}) *dbus.Call {
		if method != "org.SONiC.HostService.config.reload" {
			t.Errorf("Wrong method: %v", method)
		}
		ret := &dbus.Call{}
		ret.Err = nil
		ret.Body = make([]interface{}, 2)
		ret.Body[0] = int32(0)
		ch <- ret
		return &dbus.Call{}
	})
	defer mock2.Reset()

	client, err := NewDbusClient()
	if err != nil {
		t.Errorf("NewDbusClient failed: %v", err)
	}
	err = client.ConfigReload("abc")
	if err != nil {
		t.Errorf("ConfigReload should pass: %v", err)
	}
}

func TestConfigReloadNegative(t *testing.T) {
	err_msg := "This is the mock error message"
	mock1 := gomonkey.ApplyFunc(dbus.SystemBus, func() (conn *dbus.Conn, err error) {
		return &dbus.Conn{}, nil
	})
	defer mock1.Reset()
	mock2 := gomonkey.ApplyMethod(reflect.TypeOf(&dbus.Object{}), "Go", func(obj *dbus.Object, method string, flags dbus.Flags, ch chan *dbus.Call, args ...interface{}) *dbus.Call {
		if method != "org.SONiC.HostService.config.reload" {
			t.Errorf("Wrong method: %v", method)
		}
		ret := &dbus.Call{}
		ret.Err = nil
		ret.Body = make([]interface{}, 2)
		ret.Body[0] = int32(1)
		ret.Body[1] = err_msg
		ch <- ret
		return &dbus.Call{}
	})
	defer mock2.Reset()

	client, err := NewDbusClient()
	if err != nil {
		t.Errorf("NewDbusClient failed: %v", err)
	}
	err = client.ConfigReload("abc")
	if err == nil {
		t.Errorf("ConfigReload should fail")
	}
	if err.Error() != err_msg {
		t.Errorf("Wrong error: %v", err)
	}
}

func TestConfigReloadTimeout(t *testing.T) {
	mock1 := gomonkey.ApplyFunc(dbus.SystemBus, func() (conn *dbus.Conn, err error) {
		return &dbus.Conn{}, nil
	})
	defer mock1.Reset()
	mock2 := gomonkey.ApplyMethod(reflect.TypeOf(&dbus.Object{}), "Go", func(obj *dbus.Object, method string, flags dbus.Flags, ch chan *dbus.Call, args ...interface{}) *dbus.Call {
		if method != "org.SONiC.HostService.config.reload" {
			t.Errorf("Wrong method: %v", method)
		}
		return &dbus.Call{}
	})
	defer mock2.Reset()

	client, err := NewDbusClient()
	if err != nil {
		t.Errorf("NewDbusClient failed: %v", err)
	}
	err = client.ConfigReload("abc")
	if err == nil {
		t.Errorf("ConfigReload should timeout: %v", err)
	}
}

func TestConfigSave(t *testing.T) {
	mock1 := gomonkey.ApplyFunc(dbus.SystemBus, func() (conn *dbus.Conn, err error) {
		return &dbus.Conn{}, nil
	})
	defer mock1.Reset()
	mock2 := gomonkey.ApplyMethod(reflect.TypeOf(&dbus.Object{}), "Go", func(obj *dbus.Object, method string, flags dbus.Flags, ch chan *dbus.Call, args ...interface{}) *dbus.Call {
		if method != "org.SONiC.HostService.config.save" {
			t.Errorf("Wrong method: %v", method)
		}
		ret := &dbus.Call{}
		ret.Err = nil
		ret.Body = make([]interface{}, 2)
		ret.Body[0] = int32(0)
		ch <- ret
		return &dbus.Call{}
	})
	defer mock2.Reset()

	client, err := NewDbusClient()
	if err != nil {
		t.Errorf("NewDbusClient failed: %v", err)
	}
	err = client.ConfigSave("abc")
	if err != nil {
		t.Errorf("ConfigSave should pass: %v", err)
	}
}

func TestConfigSaveNegative(t *testing.T) {
	err_msg := "This is the mock error message"
	mock1 := gomonkey.ApplyFunc(dbus.SystemBus, func() (conn *dbus.Conn, err error) {
		return &dbus.Conn{}, nil
	})
	defer mock1.Reset()
	mock2 := gomonkey.ApplyMethod(reflect.TypeOf(&dbus.Object{}), "Go", func(obj *dbus.Object, method string, flags dbus.Flags, ch chan *dbus.Call, args ...interface{}) *dbus.Call {
		if method != "org.SONiC.HostService.config.save" {
			t.Errorf("Wrong method: %v", method)
		}
		ret := &dbus.Call{}
		ret.Err = nil
		ret.Body = make([]interface{}, 2)
		ret.Body[0] = int32(1)
		ret.Body[1] = err_msg
		ch <- ret
		return &dbus.Call{}
	})
	defer mock2.Reset()

	client, err := NewDbusClient()
	if err != nil {
		t.Errorf("NewDbusClient failed: %v", err)
	}
	err = client.ConfigSave("abc")
	if err == nil {
		t.Errorf("ConfigSave should fail")
	}
	if err.Error() != err_msg {
		t.Errorf("Wrong error: %v", err)
	}
}

func TestApplyPatchYang(t *testing.T) {
	mock1 := gomonkey.ApplyFunc(dbus.SystemBus, func() (conn *dbus.Conn, err error) {
		return &dbus.Conn{}, nil
	})
	defer mock1.Reset()
	mock2 := gomonkey.ApplyMethod(reflect.TypeOf(&dbus.Object{}), "Go", func(obj *dbus.Object, method string, flags dbus.Flags, ch chan *dbus.Call, args ...interface{}) *dbus.Call {
		if method != "org.SONiC.HostService.gcu.apply_patch_yang" {
			t.Errorf("Wrong method: %v", method)
		}
		ret := &dbus.Call{}
		ret.Err = nil
		ret.Body = make([]interface{}, 2)
		ret.Body[0] = int32(0)
		ch <- ret
		return &dbus.Call{}
	})
	defer mock2.Reset()

	client, err := NewDbusClient()
	if err != nil {
		t.Errorf("NewDbusClient failed: %v", err)
	}
	err = client.ApplyPatchYang("abc")
	if err != nil {
		t.Errorf("ApplyPatchYang should pass: %v", err)
	}
}

func TestApplyPatchYangNegative(t *testing.T) {
	err_msg := "This is the mock error message"
	mock1 := gomonkey.ApplyFunc(dbus.SystemBus, func() (conn *dbus.Conn, err error) {
		return &dbus.Conn{}, nil
	})
	defer mock1.Reset()
	mock2 := gomonkey.ApplyMethod(reflect.TypeOf(&dbus.Object{}), "Go", func(obj *dbus.Object, method string, flags dbus.Flags, ch chan *dbus.Call, args ...interface{}) *dbus.Call {
		if method != "org.SONiC.HostService.gcu.apply_patch_yang" {
			t.Errorf("Wrong method: %v", method)
		}
		ret := &dbus.Call{}
		ret.Err = nil
		ret.Body = make([]interface{}, 2)
		ret.Body[0] = int32(1)
		ret.Body[1] = err_msg
		ch <- ret
		return &dbus.Call{}
	})
	defer mock2.Reset()

	client, err := NewDbusClient()
	if err != nil {
		t.Errorf("NewDbusClient failed: %v", err)
	}
	err = client.ApplyPatchYang("abc")
	if err == nil {
		t.Errorf("ApplyPatchYang should fail")
	}
	if err.Error() != err_msg {
		t.Errorf("Wrong error: %v", err)
	}
}

func TestApplyPatchDb(t *testing.T) {
	mock1 := gomonkey.ApplyFunc(dbus.SystemBus, func() (conn *dbus.Conn, err error) {
		return &dbus.Conn{}, nil
	})
	defer mock1.Reset()
	mock2 := gomonkey.ApplyMethod(reflect.TypeOf(&dbus.Object{}), "Go", func(obj *dbus.Object, method string, flags dbus.Flags, ch chan *dbus.Call, args ...interface{}) *dbus.Call {
		if method != "org.SONiC.HostService.gcu.apply_patch_db" {
			t.Errorf("Wrong method: %v", method)
		}
		ret := &dbus.Call{}
		ret.Err = nil
		ret.Body = make([]interface{}, 2)
		ret.Body[0] = int32(0)
		ch <- ret
		return &dbus.Call{}
	})
	defer mock2.Reset()

	client, err := NewDbusClient()
	if err != nil {
		t.Errorf("NewDbusClient failed: %v", err)
	}
	err = client.ApplyPatchDb("abc")
	if err != nil {
		t.Errorf("ApplyPatchDb should pass: %v", err)
	}
}

func TestApplyPatchDbNegative(t *testing.T) {
	err_msg := "This is the mock error message"
	mock1 := gomonkey.ApplyFunc(dbus.SystemBus, func() (conn *dbus.Conn, err error) {
		return &dbus.Conn{}, nil
	})
	defer mock1.Reset()
	mock2 := gomonkey.ApplyMethod(reflect.TypeOf(&dbus.Object{}), "Go", func(obj *dbus.Object, method string, flags dbus.Flags, ch chan *dbus.Call, args ...interface{}) *dbus.Call {
		if method != "org.SONiC.HostService.gcu.apply_patch_db" {
			t.Errorf("Wrong method: %v", method)
		}
		ret := &dbus.Call{}
		ret.Err = nil
		ret.Body = make([]interface{}, 2)
		ret.Body[0] = int32(1)
		ret.Body[1] = err_msg
		ch <- ret
		return &dbus.Call{}
	})
	defer mock2.Reset()

	client, err := NewDbusClient()
	if err != nil {
		t.Errorf("NewDbusClient failed: %v", err)
	}
	err = client.ApplyPatchDb("abc")
	if err == nil {
		t.Errorf("ApplyPatchDb should fail")
	}
	if err.Error() != err_msg {
		t.Errorf("Wrong error: %v", err)
	}
}

func TestCreateCheckPoint(t *testing.T) {
	mock1 := gomonkey.ApplyFunc(dbus.SystemBus, func() (conn *dbus.Conn, err error) {
		return &dbus.Conn{}, nil
	})
	defer mock1.Reset()
	mock2 := gomonkey.ApplyMethod(reflect.TypeOf(&dbus.Object{}), "Go", func(obj *dbus.Object, method string, flags dbus.Flags, ch chan *dbus.Call, args ...interface{}) *dbus.Call {
		if method != "org.SONiC.HostService.gcu.create_checkpoint" {
			t.Errorf("Wrong method: %v", method)
		}
		ret := &dbus.Call{}
		ret.Err = nil
		ret.Body = make([]interface{}, 2)
		ret.Body[0] = int32(0)
		ch <- ret
		return &dbus.Call{}
	})
	defer mock2.Reset()

	client, err := NewDbusClient()
	if err != nil {
		t.Errorf("NewDbusClient failed: %v", err)
	}
	err = client.CreateCheckPoint("abc")
	if err != nil {
		t.Errorf("CreateCheckPoint should pass: %v", err)
	}
}

func TestCreateCheckPointNegative(t *testing.T) {
	err_msg := "This is the mock error message"
	mock1 := gomonkey.ApplyFunc(dbus.SystemBus, func() (conn *dbus.Conn, err error) {
		return &dbus.Conn{}, nil
	})
	defer mock1.Reset()
	mock2 := gomonkey.ApplyMethod(reflect.TypeOf(&dbus.Object{}), "Go", func(obj *dbus.Object, method string, flags dbus.Flags, ch chan *dbus.Call, args ...interface{}) *dbus.Call {
		if method != "org.SONiC.HostService.gcu.create_checkpoint" {
			t.Errorf("Wrong method: %v", method)
		}
		ret := &dbus.Call{}
		ret.Err = nil
		ret.Body = make([]interface{}, 2)
		ret.Body[0] = int32(1)
		ret.Body[1] = err_msg
		ch <- ret
		return &dbus.Call{}
	})
	defer mock2.Reset()

	client, err := NewDbusClient()
	if err != nil {
		t.Errorf("NewDbusClient failed: %v", err)
	}
	err = client.CreateCheckPoint("abc")
	if err == nil {
		t.Errorf("CreateCheckPoint should fail")
	}
	if err.Error() != err_msg {
		t.Errorf("Wrong error: %v", err)
	}
}

func TestDeleteCheckPoint(t *testing.T) {
	mock1 := gomonkey.ApplyFunc(dbus.SystemBus, func() (conn *dbus.Conn, err error) {
		return &dbus.Conn{}, nil
	})
	defer mock1.Reset()
	mock2 := gomonkey.ApplyMethod(reflect.TypeOf(&dbus.Object{}), "Go", func(obj *dbus.Object, method string, flags dbus.Flags, ch chan *dbus.Call, args ...interface{}) *dbus.Call {
		if method != "org.SONiC.HostService.gcu.delete_checkpoint" {
			t.Errorf("Wrong method: %v", method)
		}
		ret := &dbus.Call{}
		ret.Err = nil
		ret.Body = make([]interface{}, 2)
		ret.Body[0] = int32(0)
		ch <- ret
		return &dbus.Call{}
	})
	defer mock2.Reset()

	client, err := NewDbusClient()
	if err != nil {
		t.Errorf("NewDbusClient failed: %v", err)
	}
	err = client.DeleteCheckPoint("abc")
	if err != nil {
		t.Errorf("DeleteCheckPoint should pass: %v", err)
	}
}

func TestDeleteCheckPointNegative(t *testing.T) {
	err_msg := "This is the mock error message"
	mock1 := gomonkey.ApplyFunc(dbus.SystemBus, func() (conn *dbus.Conn, err error) {
		return &dbus.Conn{}, nil
	})
	defer mock1.Reset()
	mock2 := gomonkey.ApplyMethod(reflect.TypeOf(&dbus.Object{}), "Go", func(obj *dbus.Object, method string, flags dbus.Flags, ch chan *dbus.Call, args ...interface{}) *dbus.Call {
		if method != "org.SONiC.HostService.gcu.delete_checkpoint" {
			t.Errorf("Wrong method: %v", method)
		}
		ret := &dbus.Call{}
		ret.Err = nil
		ret.Body = make([]interface{}, 2)
		ret.Body[0] = int32(1)
		ret.Body[1] = err_msg
		ch <- ret
		return &dbus.Call{}
	})
	defer mock2.Reset()

	client, err := NewDbusClient()
	if err != nil {
		t.Errorf("NewDbusClient failed: %v", err)
	}
	err = client.DeleteCheckPoint("abc")
	if err == nil {
		t.Errorf("DeleteCheckPoint should fail")
	}
	if err.Error() != err_msg {
		t.Errorf("Wrong error: %v", err)
	}
}
