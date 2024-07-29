package host_service

import (
	"time"
	"fmt"
	"reflect"
	log "github.com/golang/glog"
	"github.com/godbus/dbus/v5"
	"github.com/sonic-net/sonic-gnmi/common_utils"
)

type Service interface {
	ConfigReload(fileName string) error
	ConfigSave(fileName string) error
	ApplyPatchYang(fileName string) error
	ApplyPatchDb(fileName string) error
	CreateCheckPoint(cpName string)  error
	DeleteCheckPoint(cpName string) error
	StopService(service string) error
	RestartService(service string) error
}

type DbusClient struct {
	busNamePrefix string
	busPathPrefix string
	intNamePrefix string
	channel chan struct{}
}

func NewDbusClient() (Service, error) {
	var client DbusClient
	var err error

	client.busNamePrefix = "org.SONiC.HostService."
	client.busPathPrefix = "/org/SONiC/HostService/"
	client.intNamePrefix = "org.SONiC.HostService."
	err = nil

	return &client, err
}

func DbusApi(busName string, busPath string, intName string, timeout int, args ...interface{}) error {
	common_utils.IncCounter(common_utils.DBUS)
	conn, err := dbus.SystemBus()
	if err != nil {
		log.V(2).Infof("Failed to connect to system bus: %v", err)
		common_utils.IncCounter(common_utils.DBUS_FAIL)
		return err
	}

	ch := make(chan *dbus.Call, 1)
	obj := conn.Object(busName, dbus.ObjectPath(busPath))
	obj.Go(intName, 0, ch, args...)
	select {
	case call := <-ch:
		if call.Err != nil {
			common_utils.IncCounter(common_utils.DBUS_FAIL)
			return call.Err
		}
		result := call.Body
		if len(result) == 0 {
			common_utils.IncCounter(common_utils.DBUS_FAIL)
			return fmt.Errorf("Dbus result is empty %v", result)
		}
		if ret, ok := result[0].(int32); ok {
			if ret == 0 {
				return nil
			} else {
				if len(result) != 2 {
					common_utils.IncCounter(common_utils.DBUS_FAIL)
					return fmt.Errorf("Dbus result is invalid %v", result)
				}
				if msg, check := result[1].(string); check {
					common_utils.IncCounter(common_utils.DBUS_FAIL)
					return fmt.Errorf(msg)
				} else {
					common_utils.IncCounter(common_utils.DBUS_FAIL)
					return fmt.Errorf("Invalid result message type %v %v", result[1], reflect.TypeOf(result[1]))
				}
			}
		} else {
			common_utils.IncCounter(common_utils.DBUS_FAIL)
			return fmt.Errorf("Invalid result type %v %v", result[0], reflect.TypeOf(result[0]))
		}
	case <-time.After(time.Duration(timeout) * time.Second):
		log.V(2).Infof("DbusApi: timeout")
		common_utils.IncCounter(common_utils.DBUS_FAIL)
		return fmt.Errorf("Timeout %v", timeout)
	}
	return nil
}

func (c *DbusClient) ConfigReload(config string) error {
	common_utils.IncCounter(common_utils.DBUS_CONFIG_RELOAD)
	modName := "config"
	busName := c.busNamePrefix + modName
	busPath := c.busPathPrefix + modName
	intName := c.intNamePrefix + modName + ".reload"
	err := DbusApi(busName, busPath, intName, 10, config)
	return err
}

func (c *DbusClient) ConfigSave(fileName string) error {
	common_utils.IncCounter(common_utils.DBUS_CONFIG_SAVE)
	modName := "config"
	busName := c.busNamePrefix + modName
	busPath := c.busPathPrefix + modName
	intName := c.intNamePrefix + modName + ".save"
	err := DbusApi(busName, busPath, intName, 10, fileName)
	return err
}

func (c *DbusClient) ApplyPatchYang(patch string) error {
	common_utils.IncCounter(common_utils.DBUS_APPLY_PATCH_YANG)
	modName := "gcu"
	busName := c.busNamePrefix + modName
	busPath := c.busPathPrefix + modName
	intName := c.intNamePrefix + modName + ".apply_patch_yang"
	err := DbusApi(busName, busPath, intName, 180, patch)
	return err
}

func (c *DbusClient) ApplyPatchDb(patch string) error {
	common_utils.IncCounter(common_utils.DBUS_APPLY_PATCH_DB)
	modName := "gcu"
	busName := c.busNamePrefix + modName
	busPath := c.busPathPrefix + modName
	intName := c.intNamePrefix + modName + ".apply_patch_db"
	err := DbusApi(busName, busPath, intName, 180, patch)
	return err
}

func (c *DbusClient) CreateCheckPoint(fileName string) error {
	common_utils.IncCounter(common_utils.DBUS_CREATE_CHECKPOINT)
	modName := "gcu"
	busName := c.busNamePrefix + modName
	busPath := c.busPathPrefix + modName
	intName := c.intNamePrefix + modName + ".create_checkpoint"
	err := DbusApi(busName, busPath, intName, 10, fileName)
	return err
}

func (c *DbusClient) DeleteCheckPoint(fileName string) error {
	common_utils.IncCounter(common_utils.DBUS_DELETE_CHECKPOINT)
	modName := "gcu"
	busName := c.busNamePrefix + modName
	busPath := c.busPathPrefix + modName
	intName := c.intNamePrefix + modName + ".delete_checkpoint"
	err := DbusApi(busName, busPath, intName, 10, fileName)
	return err
}

func (c *DbusClient) StopService(service string) error {
	common_utils.IncCounter(common_utils.DBUS_STOP_SERVICE)
	modName := "systemd"
	busName := c.busNamePrefix + modName
	busPath := c.busPathPrefix + modName
	intName := c.intNamePrefix + modName + ".stop_service"
	err := DbusApi(busName, busPath, intName, 90, service)
	return err
}

func (c *DbusClient) RestartService(service string) error {
	common_utils.IncCounter(common_utils.DBUS_RESTART_SERVICE)
	modName := "systemd"
	busName := c.busNamePrefix + modName
	busPath := c.busPathPrefix + modName
	intName := c.intNamePrefix + modName + ".restart_service"
	err := DbusApi(busName, busPath, intName, 90, service)
	return err
}
