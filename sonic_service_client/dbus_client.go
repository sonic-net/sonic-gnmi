package host_service

import (
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/godbus/dbus/v5"
	log "github.com/golang/glog"
	"github.com/sonic-net/sonic-gnmi/common_utils"
)

type Service interface {
	// Close the connection to the D-Bus
	Close() error

	// SONiC Host Service D-Bus API
	ConfigReload(fileName string) error
	ConfigReplace(fileName string) error
	ConfigSave(fileName string) error
	ApplyPatchYang(fileName string) error
	ApplyPatchDb(fileName string) error
	CreateCheckPoint(cpName string) error
	DeleteCheckPoint(cpName string) error
	StopService(service string) error
	RestartService(service string) error
	// File services APIs
	GetFileStat(path string) (map[string]string, error)
	DownloadFile(hostname, username, password, remotePath, localPath, protocol string) error
	RemoveFile(path string) error
	// Image services APIs
	DownloadImage(url string, save_as string) error
	InstallImage(where string) error
	ListImages() (string, error)
	ActivateImage(image string) error
	// Docker services APIs
	LoadDockerImage(image string) error
}

func (f *FakeDbusClient) Close() error                                  { return nil }
func (f *FakeDbusClient) ConfigReload(string) error                     { return nil }
func (f *FakeDbusClient) ConfigReplace(string) error                    { return nil }
func (f *FakeDbusClient) ConfigSave(string) error                       { return nil }
func (f *FakeDbusClient) ApplyPatchYang(string) error                   { return nil }
func (f *FakeDbusClient) ApplyPatchDb(string) error                     { return nil }
func (f *FakeDbusClient) CreateCheckPoint(string) error                 { return nil }
func (f *FakeDbusClient) DeleteCheckPoint(string) error                 { return nil }
func (f *FakeDbusClient) StopService(string) error                      { return nil }
func (f *FakeDbusClient) RestartService(string) error                   { return nil }
func (f *FakeDbusClient) GetFileStat(string) (map[string]string, error) { return nil, nil }
func (f *FakeDbusClient) DownloadFile(string, string, string, string, string, string) error {
	return nil
}
func (f *FakeDbusClient) RemoveFile(string) error            { return nil }
func (f *FakeDbusClient) DownloadImage(string, string) error { return nil }
func (f *FakeDbusClient) InstallImage(string) error          { return nil }
func (f *FakeDbusClient) ListImages() (string, error)        { return "", nil }
func (f *FakeDbusClient) ActivateImage(string) error         { return nil }
func (f *FakeDbusClient) LoadDockerImage(string) error       { return nil }
func (f *FakeDbusClient) FactoryReset(cmd string) (string, error) {
	if f.Msg == "" {
		return "", errors.New("Previous reset is ongoing")
	}
	return f.Msg, nil
}

type FakeDbusClient struct {
	Msg string
}

// Make sure it implements the interface
var _ Service = (*FakeDbusClient)(nil)

type Caller interface {
	DbusApi(busName string, busPath string, intName string, timeout int, args ...interface{}) (interface{}, error)
}

type FakeDbusCaller struct {
}

type FailDbusCaller struct{}

type DbusClient struct {
	busNamePrefix string
	busPathPrefix string
	intNamePrefix string
	caller        Caller
	channel       chan struct{}
}

type DbusCaller struct{}

var NewDbusClient = func(caller Caller) (Service, error) {
	var client DbusClient
	if caller == nil {
		return nil, fmt.Errorf("You must supply a DbusCaller")
	}
	client.busNamePrefix = "org.SONiC.HostService."
	client.busPathPrefix = "/org/SONiC/HostService/"
	client.intNamePrefix = "org.SONiC.HostService."
	client.caller = caller

	return &client, nil
}

// Close the connection to the D-Bus.
func (c *DbusClient) Close() error {
	log.Infof("DbusClient: Close")
	if c.channel != nil {
		close(c.channel)
	}
	return nil
}

func (c *FakeDbusCaller) DbusApi(busName string, busPath string, intName string, timeout int, args ...interface{}) (interface{}, error) {
	return fmt.Sprintf("%v %v", intName, args), nil
}

func (_ *FailDbusCaller) DbusApi(busName string, busPath string, intName string, timeout int, args ...interface{}) (interface{}, error) {
	return "", fmt.Errorf("%v %v", intName, args)
}

func (_ *DbusCaller) DbusApi(busName string, busPath string, intName string, timeout int, args ...interface{}) (interface{}, error) {
	common_utils.IncCounter(common_utils.DBUS)
	conn, err := dbus.SystemBus()
	if err != nil {
		log.V(2).Infof("Failed to connect to system bus: %v", err)
		common_utils.IncCounter(common_utils.DBUS_FAIL)
		return nil, err
	}

	ch := make(chan *dbus.Call, 1)
	obj := conn.Object(busName, dbus.ObjectPath(busPath))
	obj.Go(intName, 0, ch, args...)

	select {
	case call := <-ch:
		if call.Err != nil {
			common_utils.IncCounter(common_utils.DBUS_FAIL)
			return nil, call.Err
		}
		result := call.Body
		if len(result) == 0 {
			common_utils.IncCounter(common_utils.DBUS_FAIL)
			return nil, fmt.Errorf("Dbus result is empty %v", result)
		}
		if ret, ok := result[0].(int32); ok {
			if ret == 0 {
				if len(result) != 2 {
					common_utils.IncCounter(common_utils.DBUS_FAIL)
					return nil, fmt.Errorf("Dbus result is invalid %v", result)
				}
				return result[1], nil
			} else {
				if len(result) != 2 {
					common_utils.IncCounter(common_utils.DBUS_FAIL)
					return nil, fmt.Errorf("Dbus result is invalid %v", result)
				}
				if msg, check := result[1].(string); check {
					common_utils.IncCounter(common_utils.DBUS_FAIL)
					return nil, fmt.Errorf(msg)
				} else if msg, check := result[1].(map[string]string); check {
					common_utils.IncCounter(common_utils.DBUS_FAIL)
					return nil, fmt.Errorf(msg["error"])
				} else {
					common_utils.IncCounter(common_utils.DBUS_FAIL)
					return nil, fmt.Errorf("Invalid result message type %v %v", result[1], reflect.TypeOf(result[1]))
				}
			}
		} else {
			common_utils.IncCounter(common_utils.DBUS_FAIL)
			return nil, fmt.Errorf("Invalid result type %v %v", result[0], reflect.TypeOf(result[0]))
		}
	case <-time.After(time.Duration(timeout) * time.Second):
		log.V(2).Infof("DbusApi: timeout")
		common_utils.IncCounter(common_utils.DBUS_FAIL)
		return nil, fmt.Errorf("Timeout %v", timeout)
	}
}

func (c *DbusClient) ConfigReload(config string) error {
	common_utils.IncCounter(common_utils.DBUS_CONFIG_RELOAD)
	modName := "config"
	busName := c.busNamePrefix + modName
	busPath := c.busPathPrefix + modName
	intName := c.intNamePrefix + modName + ".reload"
	_, err := c.caller.DbusApi(busName, busPath, intName, 60, config)
	return err
}

func (c *DbusClient) ConfigReplace(config string) error {
	common_utils.IncCounter(common_utils.DBUS_CONFIG_REPLACE)
	modName := "gcu"
	busName := c.busNamePrefix + modName
	busPath := c.busPathPrefix + modName
	intName := c.intNamePrefix + modName + ".replace_db"
	_, err := c.caller.DbusApi(busName, busPath, intName, 600, config)
	return err
}

func (c *DbusClient) ConfigSave(fileName string) error {
	common_utils.IncCounter(common_utils.DBUS_CONFIG_SAVE)
	modName := "config"
	busName := c.busNamePrefix + modName
	busPath := c.busPathPrefix + modName
	intName := c.intNamePrefix + modName + ".save"
	_, err := c.caller.DbusApi(busName, busPath, intName, 60, fileName)
	return err
}

func (c *DbusClient) ApplyPatchYang(patch string) error {
	common_utils.IncCounter(common_utils.DBUS_APPLY_PATCH_YANG)
	modName := "gcu"
	busName := c.busNamePrefix + modName
	busPath := c.busPathPrefix + modName
	intName := c.intNamePrefix + modName + ".apply_patch_yang"
	_, err := c.caller.DbusApi(busName, busPath, intName, 600, patch)
	return err
}

func (c *DbusClient) ApplyPatchDb(patch string) error {
	common_utils.IncCounter(common_utils.DBUS_APPLY_PATCH_DB)
	modName := "gcu"
	busName := c.busNamePrefix + modName
	busPath := c.busPathPrefix + modName
	intName := c.intNamePrefix + modName + ".apply_patch_db"
	_, err := c.caller.DbusApi(busName, busPath, intName, 600, patch)
	return err
}

func (c *DbusClient) CreateCheckPoint(fileName string) error {
	common_utils.IncCounter(common_utils.DBUS_CREATE_CHECKPOINT)
	modName := "gcu"
	busName := c.busNamePrefix + modName
	busPath := c.busPathPrefix + modName
	intName := c.intNamePrefix + modName + ".create_checkpoint"
	_, err := c.caller.DbusApi(busName, busPath, intName, 60, fileName)
	return err
}

func (c *DbusClient) DeleteCheckPoint(fileName string) error {
	common_utils.IncCounter(common_utils.DBUS_DELETE_CHECKPOINT)
	modName := "gcu"
	busName := c.busNamePrefix + modName
	busPath := c.busPathPrefix + modName
	intName := c.intNamePrefix + modName + ".delete_checkpoint"
	_, err := c.caller.DbusApi(busName, busPath, intName, 60, fileName)
	return err
}

func (c *DbusClient) StopService(service string) error {
	common_utils.IncCounter(common_utils.DBUS_STOP_SERVICE)
	modName := "systemd"
	busName := c.busNamePrefix + modName
	busPath := c.busPathPrefix + modName
	intName := c.intNamePrefix + modName + ".stop_service"
	_, err := c.caller.DbusApi(busName, busPath, intName, 240, service)
	return err
}

func (c *DbusClient) RestartService(service string) error {
	common_utils.IncCounter(common_utils.DBUS_RESTART_SERVICE)
	modName := "systemd"
	busName := c.busNamePrefix + modName
	busPath := c.busPathPrefix + modName
	intName := c.intNamePrefix + modName + ".restart_service"
	_, err := c.caller.DbusApi(busName, busPath, intName, 240, service)
	return err
}

func (c *DbusClient) GetFileStat(path string) (map[string]string, error) {
	common_utils.IncCounter(common_utils.DBUS_FILE_STAT)
	modName := "file"
	busName := c.busNamePrefix + modName
	busPath := c.busPathPrefix + modName
	intName := c.intNamePrefix + modName + ".get_file_stat"
	result, err := c.caller.DbusApi(busName, busPath, intName, 60, path)
	if err != nil {
		return nil, err
	}
	data, _ := result.(map[string]string)
	return data, nil
}

func (c *DbusClient) DownloadFile(hostname, username, password, remotePath, localPath, protocol string) error {
	common_utils.IncCounter(common_utils.DBUS_FILE_DOWNLOAD)
	modName := "file"
	busName := c.busNamePrefix + modName
	busPath := c.busPathPrefix + modName
	intName := c.intNamePrefix + modName + ".download"
	_, err := c.caller.DbusApi(busName, busPath, intName, 900, hostname, username, password, remotePath, localPath, protocol)
	return err
}

func (c *DbusClient) RemoveFile(path string) error {
	common_utils.IncCounter(common_utils.DBUS_FILE_REMOVE)
	modName := "file"
	busName := c.busNamePrefix + modName
	busPath := c.busPathPrefix + modName
	intName := c.intNamePrefix + modName + ".remove"
	_, err := c.caller.DbusApi(busName, busPath, intName, 60, path)
	return err
}

func (c *DbusClient) DownloadImage(url string, save_as string) error {
	common_utils.IncCounter(common_utils.DBUS_IMAGE_DOWNLOAD)
	modName := "image_service"
	busName := c.busNamePrefix + modName
	busPath := c.busPathPrefix + modName
	intName := c.intNamePrefix + modName + ".download"
	_, err := c.caller.DbusApi(busName, busPath, intName /*timeout=*/, 900, url, save_as)
	return err
}

func (c *DbusClient) InstallImage(where string) error {
	common_utils.IncCounter(common_utils.DBUS_IMAGE_INSTALL)
	modName := "image_service"
	busName := c.busNamePrefix + modName
	busPath := c.busPathPrefix + modName
	intName := c.intNamePrefix + modName + ".install"
	_, err := c.caller.DbusApi(busName, busPath, intName /*timeout=*/, 900, where)
	return err
}

func (c *DbusClient) ListImages() (string, error) {
	common_utils.IncCounter(common_utils.DBUS_IMAGE_LIST)
	modName := "image_service"
	busName := c.busNamePrefix + modName
	busPath := c.busPathPrefix + modName
	intName := c.intNamePrefix + modName + ".list_images"
	result, err := c.caller.DbusApi(busName, busPath, intName /*timeout=*/, 60)
	if err != nil {
		return "", err
	}
	strResult, ok := result.(string)
	if !ok {
		return "", fmt.Errorf("Invalid result type %v %v", result, reflect.TypeOf(result))
	}
	log.V(2).Infof("ListImages: %v", result)
	return strResult, nil
}

func (c *DbusClient) ActivateImage(image string) error {
	common_utils.IncCounter(common_utils.DBUS_IMAGE_ACTIVATE)
	modName := "image_service"
	busName := c.busNamePrefix + modName
	busPath := c.busPathPrefix + modName
	intName := c.intNamePrefix + modName + ".set_next_boot"
	_, err := c.caller.DbusApi(busName, busPath, intName, 60, image)
	return err
}

func (c *DbusClient) LoadDockerImage(image string) error {
	common_utils.IncCounter(common_utils.DBUS_DOCKER_LOAD)
	modName := "docker_service"
	busName := c.busNamePrefix + modName
	busPath := c.busPathPrefix + modName
	intName := c.intNamePrefix + modName + ".load"
	_, err := c.caller.DbusApi(busName, busPath, intName /*timeout=*/, 180, image)
	return err
}
