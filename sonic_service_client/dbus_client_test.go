package host_service

import (
	"reflect"
	"testing"

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
