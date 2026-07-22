//go:build !pure

package dbconfig

import (
	"errors"
	"fmt"
	"os"

	"github.com/sonic-net/sonic-gnmi/swsscommon"
)

var activeProvider provider = swssProvider{}

type swssProvider struct{}

func (swssProvider) initialize() error {
	if _, err := os.Stat(GlobalConfigFile); err == nil {
		if !swsscommon.SonicDBConfigIsGlobalInit() {
			swsscommon.SonicDBConfigInitializeGlobalConfig()
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect global database configuration: %w", err)
	} else if !swsscommon.SonicDBConfigIsInit() {
		swsscommon.SonicDBConfigInitialize()
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

	namespaces := make([]string, 0, int(values.Size()))
	for i := 0; i < int(values.Size()); i++ {
		namespaces = append(namespaces, values.Get(i))
	}
	return namespaces, nil
}

func (swssProvider) dbList(string) ([]string, error) {
	values := swsscommon.SonicDBConfigGetDbList()
	defer swsscommon.DeleteVectorString(values)

	databases := make([]string, 0, int(values.Size()))
	for i := 0; i < int(values.Size()); i++ {
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
