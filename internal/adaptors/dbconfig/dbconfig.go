package dbconfig

import (
	"fmt"
	"strconv"
)

const (
	DefaultNamespace = ""
	GlobalConfigFile = "/var/run/redis/sonic-db/database_global.json"
	ConfigFile       = "/var/run/redis/sonic-db/database_config.json"
)

type provider interface {
	initialize() error
	reset() error
	namespaces() ([]string, error)
	dbList(namespace string) ([]string, error)
	dbID(name, namespace string) (int, error)
	dbSeparator(name, namespace string) (string, error)
	dbSocket(name, namespace string) (string, error)
	dbHostname(name, namespace string) (string, error)
	dbPort(name, namespace string) (int, error)
}

var initialized bool

func Init() (err error) {
	defer catchException(&err)
	initialized = false
	return activeProvider.reset()
}

func DbInit() (err error) {
	defer catchException(&err)
	if initialized {
		return nil
	}
	if err := activeProvider.initialize(); err != nil {
		return err
	}
	initialized = true
	return nil
}

func GetDbDefaultNamespace() (string, error) {
	return DefaultNamespace, nil
}

func CheckDbMultiNamespace() (multi bool, err error) {
	defer catchException(&err)
	namespaces, err := GetDbAllNamespaces()
	if err != nil {
		return false, err
	}
	return len(namespaces) > 1, nil
}

func GetDbNonDefaultNamespaces() (nonDefault []string, err error) {
	defer catchException(&err)
	namespaces, err := GetDbAllNamespaces()
	if err != nil {
		return nil, err
	}
	nonDefault = make([]string, 0, len(namespaces))
	for _, namespace := range namespaces {
		if namespace != DefaultNamespace {
			nonDefault = append(nonDefault, namespace)
		}
	}
	return nonDefault, nil
}

func GetDbAllNamespaces() (namespaces []string, err error) {
	defer catchException(&err)
	if err := DbInit(); err != nil {
		return nil, err
	}
	return activeProvider.namespaces()
}

func GetDbNamespaceFromTarget(target string) (namespace string, found bool, err error) {
	defer catchException(&err)
	if target == DefaultNamespace {
		return target, true, nil
	}
	namespaces, err := GetDbAllNamespaces()
	if err != nil {
		return "", false, err
	}
	for _, namespace := range namespaces {
		if target == namespace {
			return target, true, nil
		}
	}
	return "", false, nil
}

func GetDbList(namespace string) (databases []string, err error) {
	defer catchException(&err)
	if err := DbInit(); err != nil {
		return nil, err
	}
	return activeProvider.dbList(namespace)
}

func GetDbId(name, namespace string) (id int, err error) {
	defer catchException(&err)
	if err := DbInit(); err != nil {
		return -1, err
	}
	return activeProvider.dbID(name, namespace)
}

func GetDbSeparator(name, namespace string) (separator string, err error) {
	defer catchException(&err)
	if err := DbInit(); err != nil {
		return "", err
	}
	return activeProvider.dbSeparator(name, namespace)
}

func GetDbSock(name, namespace string) (socket string, err error) {
	defer catchException(&err)
	if err := DbInit(); err != nil {
		return "", err
	}
	return activeProvider.dbSocket(name, namespace)
}

func GetDbHostName(name, namespace string) (hostname string, err error) {
	defer catchException(&err)
	if err := DbInit(); err != nil {
		return "", err
	}
	return activeProvider.dbHostname(name, namespace)
}

func GetDbPort(name, namespace string) (port int, err error) {
	defer catchException(&err)
	if err := DbInit(); err != nil {
		return -1, err
	}
	return activeProvider.dbPort(name, namespace)
}

func GetDbTcpAddr(name, namespace string) (address string, err error) {
	defer catchException(&err)
	hostname, err := GetDbHostName(name, namespace)
	if err != nil {
		return "", err
	}
	port, err := GetDbPort(name, namespace)
	if err != nil {
		return "", err
	}
	return hostname + ":" + strconv.Itoa(port), nil
}

func catchException(err *error) {
	if recovered := recover(); recovered != nil {
		*err = fmt.Errorf("%v", recovered)
	}
}
