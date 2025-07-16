package host_service

import (
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/godbus/dbus/v5"
)

func TestNewDbusClient(t *testing.T) {
	client, err := NewDbusClient(&DbusCaller{})
	if err != nil {
		t.Errorf("NewDbusClient failed: %v", err)
	}
	if client == nil {
		t.Errorf("NewDbusClient failed: %v", client)
	}
}

func TestCloseClient(t *testing.T) {
	client, err := NewDbusClient(&DbusCaller{})
	if err != nil {
		t.Errorf("NewDbusClient failed: %v", err)
	}
	err = client.Close()
	if err != nil {
		t.Errorf("Close should pass: %v", err)
	}
}

func TestCloseClientWithChannel(t *testing.T) {
	client, err := NewDbusClient(&DbusCaller{})
	if err != nil {
		t.Errorf("NewDbusClient failed: %v", err)
	}
	client.(*DbusClient).channel = make(chan struct{})
	err = client.Close()
	if err != nil {
		t.Errorf("Close should pass: %v", err)
	}

	select {
	case <-client.(*DbusClient).channel:
		// Channel is closed
	default:
		t.Errorf("Channel should be closed")
	}
}

func TestSystemBusNegative(t *testing.T) {
	client, err := NewDbusClient(&DbusCaller{})
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

	client, err := NewDbusClient(&DbusCaller{})
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

	client, err := NewDbusClient(&DbusCaller{})
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

func TestDownloadSuccess(t *testing.T) {
	hostname := "host"
	username := "user"
	password := "pass"
	remotePath := "/remote/file"
	localPath := "/local/file"
	protocol := "SFTP"

	mock1 := gomonkey.ApplyFunc(dbus.SystemBus, func() (conn *dbus.Conn, err error) {
		return &dbus.Conn{}, nil
	})
	defer mock1.Reset()

	mock2 := gomonkey.ApplyMethod(reflect.TypeOf(&dbus.Object{}), "Go", func(obj *dbus.Object, method string, flags dbus.Flags, ch chan *dbus.Call, args ...interface{}) *dbus.Call {
		if method != "org.SONiC.HostService.file.download" {
			t.Errorf("Wrong method: %v", method)
		}
		if len(args) != 6 {
			t.Errorf("Wrong number of arguments: %v", len(args))
		}
		if args[0] != hostname || args[1] != username || args[2] != password ||
			args[3] != remotePath || args[4] != localPath || args[5] != protocol {
			t.Errorf("Wrong arguments: %v", args)
		}
		ret := &dbus.Call{}
		ret.Err = nil
		ret.Body = make([]interface{}, 2)
		ret.Body[0] = int32(0)
		ch <- ret
		return &dbus.Call{}
	})
	defer mock2.Reset()

	client, err := NewDbusClient(&DbusCaller{})
	if err != nil {
		t.Errorf("NewDbusClient failed: %v", err)
	}
	err = client.DownloadFile(hostname, username, password, remotePath, localPath, protocol)
	if err != nil {
		t.Errorf("Download should pass: %v", err)
	}
}

func TestDownloadFail(t *testing.T) {
	hostname := "host"
	username := "user"
	password := "pass"
	remotePath := "/remote/file"
	localPath := "/local/file"
	protocol := "SFTP"
	errMsg := "This is the mock error message"

	mock1 := gomonkey.ApplyFunc(dbus.SystemBus, func() (conn *dbus.Conn, err error) {
		return &dbus.Conn{}, nil
	})
	defer mock1.Reset()

	mock2 := gomonkey.ApplyMethod(reflect.TypeOf(&dbus.Object{}), "Go", func(obj *dbus.Object, method string, flags dbus.Flags, ch chan *dbus.Call, args ...interface{}) *dbus.Call {
		if method != "org.SONiC.HostService.file.download" {
			t.Errorf("Wrong method: %v", method)
		}
		ret := &dbus.Call{}
		ret.Err = nil
		ret.Body = make([]interface{}, 2)
		ret.Body[0] = int32(1)
		ret.Body[1] = errMsg
		ch <- ret
		return &dbus.Call{}
	})
	defer mock2.Reset()

	client, err := NewDbusClient(&DbusCaller{})
	if err != nil {
		t.Errorf("NewDbusClient failed: %v", err)
	}
	err = client.DownloadFile(hostname, username, password, remotePath, localPath, protocol)
	if err == nil {
		t.Errorf("Download should fail")
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

	client, err := NewDbusClient(&DbusCaller{})
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

	client, err := NewDbusClient(&DbusCaller{})
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

	client, err := NewDbusClient(&DbusCaller{})
	if err != nil {
		t.Errorf("NewDbusClient failed: %v", err)
	}
	err = client.ConfigReload("abc")
	if err == nil {
		t.Errorf("ConfigReload should timeout: %v", err)
	}
}

func TestConfigReplace(t *testing.T) {
	mock1 := gomonkey.ApplyFunc(dbus.SystemBus, func() (conn *dbus.Conn, err error) {
		return &dbus.Conn{}, nil
	})
	defer mock1.Reset()
	mock2 := gomonkey.ApplyMethod(reflect.TypeOf(&dbus.Object{}), "Go", func(obj *dbus.Object, method string, flags dbus.Flags, ch chan *dbus.Call, args ...interface{}) *dbus.Call {
		if method != "org.SONiC.HostService.gcu.replace_db" {
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

	client, err := NewDbusClient(&DbusCaller{})
	if err != nil {
		t.Errorf("NewDbusClient failed: %v", err)
	}
	err = client.ConfigReplace("abc")
	if err != nil {
		t.Errorf("ConfigReplace should pass: %v", err)
	}
}

func TestConfigReplaceNegative(t *testing.T) {
	err_msg := "This is the mock error message"
	mock1 := gomonkey.ApplyFunc(dbus.SystemBus, func() (conn *dbus.Conn, err error) {
		return &dbus.Conn{}, nil
	})
	defer mock1.Reset()
	mock2 := gomonkey.ApplyMethod(reflect.TypeOf(&dbus.Object{}), "Go", func(obj *dbus.Object, method string, flags dbus.Flags, ch chan *dbus.Call, args ...interface{}) *dbus.Call {
		if method != "org.SONiC.HostService.gcu.replace_db" {
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

	client, err := NewDbusClient(&DbusCaller{})
	if err != nil {
		t.Errorf("NewDbusClient failed: %v", err)
	}
	err = client.ConfigReplace("abc")
	if err == nil {
		t.Errorf("ConfigReplace should fail")
	}
	if err.Error() != err_msg {
		t.Errorf("Wrong error: %v", err)
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

	client, err := NewDbusClient(&DbusCaller{})
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

	client, err := NewDbusClient(&DbusCaller{})
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

	client, err := NewDbusClient(&DbusCaller{})
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

	client, err := NewDbusClient(&DbusCaller{})
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

	client, err := NewDbusClient(&DbusCaller{})
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

	client, err := NewDbusClient(&DbusCaller{})
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

	client, err := NewDbusClient(&DbusCaller{})
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

	client, err := NewDbusClient(&DbusCaller{})
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

	client, err := NewDbusClient(&DbusCaller{})
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

	client, err := NewDbusClient(&DbusCaller{})
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

func TestDownloadImageSuccess(t *testing.T) {
	url := "http://example/sonic-img"
	save_as := "/tmp/sonic-img"
	mock1 := gomonkey.ApplyFunc(dbus.SystemBus, func() (conn *dbus.Conn, err error) {
		return &dbus.Conn{}, nil
	})
	defer mock1.Reset()
	mock2 := gomonkey.ApplyMethod(reflect.TypeOf(&dbus.Object{}), "Go", func(obj *dbus.Object, method string, flags dbus.Flags, ch chan *dbus.Call, args ...interface{}) *dbus.Call {
		if method != "org.SONiC.HostService.image_service.download" {
			t.Errorf("Wrong method: %v", method)
		}
		if len(args) != 2 {
			t.Errorf("Wrong number of arguments: %v", len(args))
		}
		if args[0] != url {
			t.Errorf("Wrong URL: %v", args[0])
		}
		if args[1] != save_as {
			t.Errorf("Wrong save_as: %v", args[1])
		}
		ret := &dbus.Call{}
		ret.Err = nil
		ret.Body = make([]interface{}, 2)
		ret.Body[0] = int32(0)
		ch <- ret
		return &dbus.Call{}
	})
	defer mock2.Reset()

	client, err := NewDbusClient(&DbusCaller{})
	if err != nil {
		t.Errorf("NewDbusClient failed: %v", err)
	}
	err = client.DownloadImage(url, save_as)
	if err != nil {
		t.Errorf("Download should pass: %v", err)
	}
}

func TestDownloadImageFail(t *testing.T) {
	url := "http://example/sonic-img"
	save_as := "/tmp/sonic-img"
	err_msg := "This is the mock error message"

	mock1 := gomonkey.ApplyFunc(dbus.SystemBus, func() (conn *dbus.Conn, err error) {
		return &dbus.Conn{}, nil
	})
	defer mock1.Reset()
	mock2 := gomonkey.ApplyMethod(reflect.TypeOf(&dbus.Object{}), "Go", func(obj *dbus.Object, method string, flags dbus.Flags, ch chan *dbus.Call, args ...interface{}) *dbus.Call {
		if method != "org.SONiC.HostService.image_service.download" {
			t.Errorf("Wrong method: %v", method)
		}
		if len(args) != 2 {
			t.Errorf("Wrong number of arguments: %v", len(args))
		}
		if args[0] != url {
			t.Errorf("Wrong URL: %v", args[0])
		}
		if args[1] != save_as {
			t.Errorf("Wrong save_as: %v", args[1])
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

	client, err := NewDbusClient(&DbusCaller{})
	if err != nil {
		t.Errorf("NewDbusClient failed: %v", err)
	}
	err = client.DownloadImage(url, save_as)
	if err == nil {
		t.Errorf("Download should fail")
	}
	if err.Error() != err_msg {
		t.Errorf("Expected error message '%s' but got '%v'", err_msg, err)
	}
}

func TestInstallImageSuccess(t *testing.T) {
	where := "/tmp/sonic-img"
	mock1 := gomonkey.ApplyFunc(dbus.SystemBus, func() (conn *dbus.Conn, err error) {
		return &dbus.Conn{}, nil
	})
	defer mock1.Reset()
	mock2 := gomonkey.ApplyMethod(reflect.TypeOf(&dbus.Object{}), "Go", func(obj *dbus.Object, method string, flags dbus.Flags, ch chan *dbus.Call, args ...interface{}) *dbus.Call {
		if method != "org.SONiC.HostService.image_service.install" {
			t.Errorf("Wrong method: %v", method)
		}
		if len(args) != 1 {
			t.Errorf("Wrong number of arguments: %v", len(args))
		}
		if args[0] != where {
			t.Errorf("Wrong where: %v", args[0])
		}
		ret := &dbus.Call{}
		ret.Err = nil
		ret.Body = make([]interface{}, 2)
		ret.Body[0] = int32(0)
		ch <- ret
		return &dbus.Call{}
	})
	defer mock2.Reset()

	client, err := NewDbusClient(&DbusCaller{})
	if err != nil {
		t.Errorf("NewDbusClient failed: %v", err)
	}
	err = client.InstallImage(where)
	if err != nil {
		t.Errorf("InstallImage should pass: %v", err)
	}
}

func TestInstallImageFail(t *testing.T) {
	where := "/tmp/sonic-img"
	err_msg := "This is the mock error message"

	mock1 := gomonkey.ApplyFunc(dbus.SystemBus, func() (conn *dbus.Conn, err error) {
		return &dbus.Conn{}, nil
	})
	defer mock1.Reset()
	mock2 := gomonkey.ApplyMethod(reflect.TypeOf(&dbus.Object{}), "Go", func(obj *dbus.Object, method string, flags dbus.Flags, ch chan *dbus.Call, args ...interface{}) *dbus.Call {
		if method != "org.SONiC.HostService.image_service.install" {
			t.Errorf("Wrong method: %v", method)
		}
		if len(args) != 1 {
			t.Errorf("Wrong number of arguments: %v", len(args))
		}
		if args[0] != where {
			t.Errorf("Wrong where: %v", args[0])
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

	client, err := NewDbusClient(&DbusCaller{})
	if err != nil {
		t.Errorf("NewDbusClient failed: %v", err)
	}
	err = client.InstallImage(where)
	if err == nil {
		t.Errorf("InstallImage should fail")
	}
	if err.Error() != err_msg {
		t.Errorf("Expected error message '%s' but got '%v'", err_msg, err)
	}
}

func TestListImagesSuccess(t *testing.T) {
	expectedDbusOut := `{
		"current": "current_image",
		"next": "next_image",
		"available": ["image1", "image2"]
	}`
	mock1 := gomonkey.ApplyFunc(dbus.SystemBus, func() (conn *dbus.Conn, err error) {
		return &dbus.Conn{}, nil
	})
	defer mock1.Reset()
	mock2 := gomonkey.ApplyMethod(reflect.TypeOf(&dbus.Object{}), "Go", func(obj *dbus.Object, method string, flags dbus.Flags, ch chan *dbus.Call, args ...interface{}) *dbus.Call {
		if method != "org.SONiC.HostService.image_service.list_images" {
			t.Errorf("Wrong method: %v", method)
		}
		ret := &dbus.Call{}
		ret.Err = nil
		ret.Body = make([]interface{}, 2)
		ret.Body[0] = int32(0)
		ret.Body[1] = expectedDbusOut
		ch <- ret
		return &dbus.Call{}
	})
	defer mock2.Reset()

	client, err := NewDbusClient(&DbusCaller{})
	if err != nil {
		t.Errorf("NewDbusClient failed: %v", err)
	}
	result, err := client.ListImages()
	if err != nil {
		t.Errorf("ListImages should pass: %v", err)
	}
	if result != expectedDbusOut {
		t.Errorf("Expected %s but got %s", expectedDbusOut, result)
	}
}

func TestListImagesFailDBusError(t *testing.T) {
	err_msg := "This is the mock error message"
	mock1 := gomonkey.ApplyFunc(dbus.SystemBus, func() (conn *dbus.Conn, err error) {
		return &dbus.Conn{}, nil
	})
	defer mock1.Reset()
	mock2 := gomonkey.ApplyMethod(reflect.TypeOf(&dbus.Object{}), "Go", func(obj *dbus.Object, method string, flags dbus.Flags, ch chan *dbus.Call, args ...interface{}) *dbus.Call {
		ret := &dbus.Call{}
		ret.Err = nil
		ret.Body = make([]interface{}, 2)
		ret.Body[0] = int32(1)
		ret.Body[1] = err_msg
		ch <- ret
		return &dbus.Call{}
	})
	defer mock2.Reset()

	client, err := NewDbusClient(&DbusCaller{})
	if err != nil {
		t.Errorf("NewDbusClient failed: %v", err)
	}
	_, err = client.ListImages()
	if err == nil {
		t.Errorf("ListImages should fail")
	}
	if err.Error() != err_msg {
		t.Errorf("Expected error message '%s' but got '%v'", err_msg, err)
	}
}

func TestListImagesFailWrongType(t *testing.T) {
	mock1 := gomonkey.ApplyFunc(dbus.SystemBus, func() (conn *dbus.Conn, err error) {
		return &dbus.Conn{}, nil
	})
	defer mock1.Reset()
	mock2 := gomonkey.ApplyMethod(reflect.TypeOf(&dbus.Object{}), "Go", func(obj *dbus.Object, method string, flags dbus.Flags, ch chan *dbus.Call, args ...interface{}) *dbus.Call {
		ret := &dbus.Call{}
		ret.Err = nil
		ret.Body = make([]interface{}, 2)
		ret.Body[0] = int32(0)
		ret.Body[1] = 12345 // Wrong type, should be a string
		ch <- ret
		return &dbus.Call{}
	})
	defer mock2.Reset()

	client, err := NewDbusClient(&DbusCaller{})
	if err != nil {
		t.Errorf("NewDbusClient failed: %v", err)
	}
	_, err = client.ListImages()
	if err == nil {
		t.Errorf("ListImages should fail due to wrong type")
	}
}

func TestActivateImageSuccess(t *testing.T) {
	image := "next_image"
	mock1 := gomonkey.ApplyFunc(dbus.SystemBus, func() (conn *dbus.Conn, err error) {
		return &dbus.Conn{}, nil
	})
	defer mock1.Reset()
	mock2 := gomonkey.ApplyMethod(reflect.TypeOf(&dbus.Object{}), "Go", func(obj *dbus.Object, method string, flags dbus.Flags, ch chan *dbus.Call, args ...interface{}) *dbus.Call {
		if method != "org.SONiC.HostService.image_service.set_next_boot" {
			t.Errorf("Wrong method: %v", method)
		}
		if len(args) != 1 {
			t.Errorf("Wrong number of arguments: %v", len(args))
		}
		if args[0] != image {
			t.Errorf("Wrong image: %v", args[0])
		}
		ret := &dbus.Call{}
		ret.Err = nil
		ret.Body = make([]interface{}, 2)
		ret.Body[0] = int32(0)
		ch <- ret
		return &dbus.Call{}
	})
	defer mock2.Reset()

	client, err := NewDbusClient(&DbusCaller{})
	if err != nil {
		t.Errorf("NewDbusClient failed: %v", err)
	}
	err = client.ActivateImage(image)
	if err != nil {
		t.Errorf("ActivateImage should pass: %v", err)
	}
}

func TestActivateImageFail(t *testing.T) {
	image := "next_image"
	err_msg := "This is the mock error message"

	mock1 := gomonkey.ApplyFunc(dbus.SystemBus, func() (conn *dbus.Conn, err error) {
		return &dbus.Conn{}, nil
	})
	defer mock1.Reset()
	mock2 := gomonkey.ApplyMethod(reflect.TypeOf(&dbus.Object{}), "Go", func(obj *dbus.Object, method string, flags dbus.Flags, ch chan *dbus.Call, args ...interface{}) *dbus.Call {
		if method != "org.SONiC.HostService.image_service.set_next_boot" {
			t.Errorf("Wrong method: %v", method)
		}
		if len(args) != 1 {
			t.Errorf("Wrong number of arguments: %v", len(args))
		}
		if args[0] != image {
			t.Errorf("Wrong image: %v", args[0])
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

	client, err := NewDbusClient(&DbusCaller{})
	if err != nil {
		t.Errorf("NewDbusClient failed: %v", err)
	}
	err = client.ActivateImage(image)
	if err == nil {
		t.Errorf("ActivateImage should fail")
	}
	if err.Error() != err_msg {
		t.Errorf("Expected error message '%s' but got '%v'", err_msg, err)
	}
}

func TestLoadDockerImageSuccess(t *testing.T) {
	imagePath := "/tmp/docker-image.tar"

	// Mocking the DBus API to simulate a successful image load
	mock1 := gomonkey.ApplyFunc(dbus.SystemBus, func() (conn *dbus.Conn, err error) {
		return &dbus.Conn{}, nil
	})
	defer mock1.Reset()

	mock2 := gomonkey.ApplyMethod(reflect.TypeOf(&dbus.Object{}), "Go", func(obj *dbus.Object, method string, flags dbus.Flags, ch chan *dbus.Call, args ...interface{}) *dbus.Call {
		if method != "org.SONiC.HostService.docker_service.load" {
			t.Errorf("Wrong method: %v", method)
		}
		if len(args) != 1 {
			t.Errorf("Wrong number of arguments: %v", len(args))
		}
		if args[0] != imagePath {
			t.Errorf("Wrong image path: %v", args[0])
		}
		ret := &dbus.Call{}
		ret.Err = nil
		ret.Body = make([]interface{}, 2)
		ret.Body[0] = int32(0) // Indicating success
		ch <- ret
		return &dbus.Call{}
	})
	defer mock2.Reset()

	client, err := NewDbusClient(&DbusCaller{})
	if err != nil {
		t.Errorf("NewDbusClient failed: %v", err)
	}

	err = client.LoadDockerImage(imagePath)
	if err != nil {
		t.Errorf("LoadDockerImage should pass: %v", err)
	}
}

func TestLoadDockerImageFail(t *testing.T) {
	imagePath := "/tmp/docker-image.tar"
	errMsg := "This is the mock error message"

	// Mocking the DBus API to simulate a failure
	mock1 := gomonkey.ApplyFunc(dbus.SystemBus, func() (conn *dbus.Conn, err error) {
		return &dbus.Conn{}, nil
	})
	defer mock1.Reset()

	mock2 := gomonkey.ApplyMethod(reflect.TypeOf(&dbus.Object{}), "Go", func(obj *dbus.Object, method string, flags dbus.Flags, ch chan *dbus.Call, args ...interface{}) *dbus.Call {
		if method != "org.SONiC.HostService.docker_service.load" {
			t.Errorf("Wrong method: %v", method)
		}
		if len(args) != 1 {
			t.Errorf("Wrong number of arguments: %v", len(args))
		}
		if args[0] != imagePath {
			t.Errorf("Wrong image path: %v", args[0])
		}
		ret := &dbus.Call{}
		ret.Err = nil
		ret.Body = make([]interface{}, 2)
		ret.Body[0] = int32(1) // Indicating failure
		ret.Body[1] = errMsg
		ch <- ret
		return &dbus.Call{}
	})
	defer mock2.Reset()

	client, err := NewDbusClient(&DbusCaller{})
	if err != nil {
		t.Errorf("NewDbusClient failed: %v", err)
	}

	err = client.LoadDockerImage(imagePath)
	if err == nil {
		t.Errorf("LoadDockerImage should fail")
	}
	if err.Error() != errMsg {
		t.Errorf("Expected error message '%s' but got '%v'", errMsg, err)
	}
}

func TestRemoveFileSuccess(t *testing.T) {
	path := "/tmp/testfile"
	mock1 := gomonkey.ApplyFunc(dbus.SystemBus, func() (conn *dbus.Conn, err error) {
		return &dbus.Conn{}, nil
	})
	defer mock1.Reset()
	mock2 := gomonkey.ApplyMethod(reflect.TypeOf(&dbus.Object{}), "Go", func(obj *dbus.Object, method string, flags dbus.Flags, ch chan *dbus.Call, args ...interface{}) *dbus.Call {
		if method != "org.SONiC.HostService.file.remove" {
			t.Errorf("Wrong method: %v", method)
		}
		if len(args) != 1 {
			t.Errorf("Wrong number of arguments: %v", len(args))
		}
		if args[0] != path {
			t.Errorf("Wrong path: %v", args[0])
		}
		ret := &dbus.Call{}
		ret.Err = nil
		ret.Body = make([]interface{}, 2)
		ret.Body[0] = int32(0)
		ch <- ret
		return &dbus.Call{}
	})
	defer mock2.Reset()

	client, err := NewDbusClient(&DbusCaller{})
	if err != nil {
		t.Errorf("NewDbusClient failed: %v", err)
	}
	err = client.RemoveFile(path)
	if err != nil {
		t.Errorf("RemoveFile should pass: %v", err)
	}
}

func TestRemoveFileFail(t *testing.T) {
	path := "/tmp/testfile"
	errMsg := "This is the mock error message"
	mock1 := gomonkey.ApplyFunc(dbus.SystemBus, func() (conn *dbus.Conn, err error) {
		return &dbus.Conn{}, nil
	})
	defer mock1.Reset()
	mock2 := gomonkey.ApplyMethod(reflect.TypeOf(&dbus.Object{}), "Go", func(obj *dbus.Object, method string, flags dbus.Flags, ch chan *dbus.Call, args ...interface{}) *dbus.Call {
		if method != "org.SONiC.HostService.file.remove" {
			t.Errorf("Wrong method: %v", method)
		}
		ret := &dbus.Call{}
		ret.Err = nil
		ret.Body = make([]interface{}, 2)
		ret.Body[0] = int32(1)
		ret.Body[1] = errMsg
		ch <- ret
		return &dbus.Call{}
	})
	defer mock2.Reset()

	client, err := NewDbusClient(&DbusCaller{})
	if err != nil {
		t.Errorf("NewDbusClient failed: %v", err)
	}
	err = client.RemoveFile(path)
	if err == nil {
		t.Errorf("RemoveFile should fail")
	}
	if err.Error() != errMsg {
		t.Errorf("Expected error message '%s' but got '%v'", errMsg, err)
	}
}
