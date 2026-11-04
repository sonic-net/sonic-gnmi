//go:build !pure

package dbconfig

import (
	"os"

	"github.com/sonic-net/sonic-gnmi/swsscommon"
)

var activeProvider provider = swssProvider{}

type swssProvider struct{}

func (swssProvider) initialize() error {
	globalConfig := GlobalConfigFilePath()
	if _, err := os.Stat(globalConfig); err == nil || os.IsExist(err) {
		if !swsscommon.SonicDBConfigIsGlobalInit() {
			swsscommon.SonicDBConfigInitializeGlobalConfig(globalConfig, false)
		}
	} else if !swsscommon.SonicDBConfigIsInit() {
		swsscommon.SonicDBConfigInitialize(ConfigFilePath())
	}
	return nil
}

func (swssProvider) reset() error {
	swsscommon.SonicDBConfigReset()
	return nil
}

func (swssProvider) namespaces() ([]string, error) {
	values := swsscommon.SonicDBConfigGetNamespaces()
	defer swsscommon.DeleteVectorString(values)
	var namespaces []string
	length := int(values.Size())
	for i := 0; i < length; i++ {
		namespaces = append(namespaces, values.Get(i))
	}
	return namespaces, nil
}

func (swssProvider) dbList(string) ([]string, error) {
	values := swsscommon.SonicDBConfigGetDbList()
	defer swsscommon.DeleteVectorString(values)
	var databases []string
	length := int(values.Size())
	for i := 0; i < length; i++ {
		databases = append(databases, values.Get(i))
	}
	return databases, nil
}

func (swssProvider) dbID(name, namespace string) (int, error) {
	return swsscommon.SonicDBConfigGetDbId(name, namespace), nil
}

func (swssProvider) dbSeparator(name, namespace string) (string, error) {
	return swsscommon.SonicDBConfigGetSeparator(name, namespace), nil
}

func (swssProvider) dbSocket(name, namespace string) (string, error) {
	return swsscommon.SonicDBConfigGetDbSock(name, namespace), nil
}

func (swssProvider) dbHostname(name, namespace string) (string, error) {
	return swsscommon.SonicDBConfigGetDbHostname(name, namespace), nil
}

func (swssProvider) dbPort(name, namespace string) (int, error) {
	return swsscommon.SonicDBConfigGetDbPort(name, namespace), nil
}
