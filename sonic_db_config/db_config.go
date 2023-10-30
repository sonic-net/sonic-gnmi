// Package dbconfig provides a generic functions for parsing sonic database config file in system
// package main
package dbconfig

import (
	"fmt"
	"github.com/sonic-net/sonic-gnmi/swsscommon"
	"os"
	"strconv"
)

const (
	SONIC_DB_GLOBAL_CONFIG_FILE string = "/var/run/redis/sonic-db/database_global.json"
	SONIC_DB_CONFIG_FILE        string = "/var/run/redis/sonic-db/database_config.json"
	SONIC_DEFAULT_NAMESPACE     string = ""
)

var sonic_db_init bool

// Convert exception to error
func CatchException(err *error) {
	if r := recover(); r != nil {
		*err = fmt.Errorf("%v", r)
	}
}

func GetDbDefaultNamespace() (ns string, err error) {
	return SONIC_DEFAULT_NAMESPACE, nil
}

func CheckDbMultiNamespace() (ret bool, err error) {
	if !sonic_db_init {
		err = DbInit()
		if err != nil {
			return false, err
		}
	}
	defer CatchException(&err)
	ns_vec := swsscommon.SonicDBConfigGetNamespaces()
	length := int(ns_vec.Size())
	// If there are more than one namespaces, this means that SONiC is using multinamespace
	return length > 1, err
}

func GetDbNonDefaultNamespaces() (ns_list []string, err error) {
	if !sonic_db_init {
		err = DbInit()
		if err != nil {
			return ns_list, err
		}
	}
	defer CatchException(&err)
	ns_vec := swsscommon.SonicDBConfigGetNamespaces()
	// Translate from vector to array
	length := int(ns_vec.Size())
	for i := 0; i < length; i += 1 {
		ns := ns_vec.Get(i)
		if ns == SONIC_DEFAULT_NAMESPACE {
			continue
		}
		ns_list = append(ns_list, ns)
	}
	return ns_list, err
}

func GetDbAllNamespaces() (ns_list []string, err error) {
	if !sonic_db_init {
		err = DbInit()
		if err != nil {
			return ns_list, err
		}
	}
	defer CatchException(&err)
	ns_vec := swsscommon.SonicDBConfigGetNamespaces()
	// Translate from vector to array
	length := int(ns_vec.Size())
	for i := 0; i < length; i += 1 {
		ns := ns_vec.Get(i)
		ns_list = append(ns_list, ns)
	}
	return ns_list, err
}

func GetDbNamespaceFromTarget(target string) (ns string, ret bool, err error) {
	ns, _ = GetDbDefaultNamespace()
	if target == ns {
		return target, true, nil
	}
	ns_list, err := GetDbNonDefaultNamespaces()
	if err != nil {
		return "", false, err
	}
	for _, ns := range ns_list {
		if target == ns {
			return target, true, nil
		}
	}
	return "", false, nil
}

func GetDbList(ns string) (db_list []string, err error) {
	if !sonic_db_init {
		err = DbInit()
		if err != nil {
			return db_list, err
		}
	}
	defer CatchException(&err)
	db_vec := swsscommon.SonicDBConfigGetDbList()
	// Translate from vector to array
	length := int(db_vec.Size())
	for i := 0; i < length; i += 1 {
		ns := db_vec.Get(i)
		db_list = append(db_list, ns)
	}
	return db_list, err
}

func GetDbSeparator(db_name string, ns string) (separator string, err error) {
	if !sonic_db_init {
		err = DbInit()
		if err != nil {
			return "", err
		}
	}
	defer CatchException(&err)
	separator = swsscommon.SonicDBConfigGetSeparator(db_name, ns)
	return separator, err
}

func GetDbId(db_name string, ns string) (id int, err error) {
	if !sonic_db_init {
		err = DbInit()
		if err != nil {
			return -1, err
		}
	}
	defer CatchException(&err)
	id = swsscommon.SonicDBConfigGetDbId(db_name, ns)
	return id, err
}

func GetDbSock(db_name string, ns string) (unix_socket_path string, err error) {
	if !sonic_db_init {
		err = DbInit()
		if err != nil {
			return "", err
		}
	}
	defer CatchException(&err)
	unix_socket_path = swsscommon.SonicDBConfigGetDbSock(db_name, ns)
	return unix_socket_path, err
}

func GetDbHostName(db_name string, ns string) (hostname string, err error) {
	if !sonic_db_init {
		err = DbInit()
		if err != nil {
			return "", err
		}
	}
	defer CatchException(&err)
	hostname = swsscommon.SonicDBConfigGetDbHostname(db_name, ns)
	return hostname, err
}

func GetDbPort(db_name string, ns string) (port int, err error) {
	if !sonic_db_init {
		err = DbInit()
		if err != nil {
			return -1, err
		}
	}
	defer CatchException(&err)
	port = swsscommon.SonicDBConfigGetDbPort(db_name, ns)
	return port, err
}

func GetDbTcpAddr(db_name string, ns string) (addr string, err error) {
	if !sonic_db_init {
		err = DbInit()
		if err != nil {
			return "", err
		}
	}
	hostname, err := GetDbHostName(db_name, ns)
	if err != nil {
		return "", err
	}
	port, err := GetDbPort(db_name, ns)
	if err != nil {
		return "", err
	}
	return hostname + ":" + strconv.Itoa(port), err
}

func DbInit() (err error) {
	if sonic_db_init {
		return nil
	}
	defer CatchException(&err)
	if _, ierr := os.Stat(SONIC_DB_GLOBAL_CONFIG_FILE); ierr == nil || os.IsExist(ierr) {
		// If there's global config file, invoke SonicDBConfigInitializeGlobalConfig
		if !swsscommon.SonicDBConfigIsGlobalInit() {
			swsscommon.SonicDBConfigInitializeGlobalConfig()
		}
	} else {
		// If there's no global config file, invoke SonicDBConfigInitialize
		if !swsscommon.SonicDBConfigIsInit() {
			swsscommon.SonicDBConfigInitialize()
		}
	}
	sonic_db_init = true
	return err
}

func Init() (err error) {
	sonic_db_init = false
	defer CatchException(&err)
	// Clear database configuration
	swsscommon.SonicDBConfigReset()
	return err
}
