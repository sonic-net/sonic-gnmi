//Package dbconfig provides a generic functions for parsing sonic database config file in system
//package main
package dbconfig

import (
	"os"
	"strconv"
	"github.com/sonic-net/sonic-gnmi/swsscommon"
)

const (
	SONIC_DB_GLOBAL_CONFIG_FILE string = "/var/run/redis/sonic-db/database_global.json"
	SONIC_DB_CONFIG_FILE        string = "/var/run/redis/sonic-db/database_config.json"
	SONIC_DEFAULT_NAMESPACE     string = ""
)

var sonic_db_init bool

func GetDbDefaultNamespace() string {
	return SONIC_DEFAULT_NAMESPACE
}
func CheckDbMultiNamespace() bool {
	if !sonic_db_init {
		DbInit()
	}
	ns_vec := swsscommon.SonicDBConfigGetNamespaces()
	length := int(ns_vec.Size())
	// If there are more than one namespaces, this means that SONiC is using multinamespace
	return length > 1
}
func GetDbNonDefaultNamespaces() []string {
	if !sonic_db_init {
		DbInit()
	}
	ns_vec := swsscommon.SonicDBConfigGetNamespaces()
	// Translate from vector to array
	length := int(ns_vec.Size())
	var ns_list []string
	for i := 0; i < length; i += 1 {
		ns := ns_vec.Get(i)
		if ns == SONIC_DEFAULT_NAMESPACE {
			continue
		}
		ns_list = append(ns_list, ns)
	}
	return ns_list
}

func GetDbAllNamespaces() []string {
	if !sonic_db_init {
		DbInit()
	}
	ns_vec := swsscommon.SonicDBConfigGetNamespaces()
	// Translate from vector to array
	length := int(ns_vec.Size())
	var ns_list []string
	for i := 0; i < length; i += 1 {
		ns := ns_vec.Get(i)
		ns_list = append(ns_list, ns)
	}
	return ns_list
}

func GetDbNamespaceFromTarget(target string) (string, bool) {
	if target == GetDbDefaultNamespace() {
		return target, true
	}
	ns_list := GetDbNonDefaultNamespaces()
	for _, ns := range ns_list {
		if target == ns {
			return target, true
		}
	}
	return "", false
}

func GetDbList(ns string) []string {
	if !sonic_db_init {
		DbInit()
	}
	db_vec := swsscommon.SonicDBConfigGetDbList()
	// Translate from vector to array
	length := int(db_vec.Size())
	var db_list []string
	for i := 0; i < length; i += 1 {
		ns := db_vec.Get(i)
		db_list = append(db_list, ns)
	}
	return db_list
}

func GetDbSeparator(db_name string, ns string) string {
	if !sonic_db_init {
		DbInit()
	}
	separator := swsscommon.SonicDBConfigGetSeparator(db_name, ns)
	return separator
}

func GetDbId(db_name string, ns string) int {
	if !sonic_db_init {
		DbInit()
	}
	id := swsscommon.SonicDBConfigGetDbId(db_name, ns)
	return id
}

func GetDbSock(db_name string, ns string) string {
	if !sonic_db_init {
		DbInit()
	}
	unix_socket_path := swsscommon.SonicDBConfigGetDbSock(db_name, ns)
	return unix_socket_path
}

func GetDbHostName(db_name string, ns string) string {
	if !sonic_db_init {
		DbInit()
	}
	hostname := swsscommon.SonicDBConfigGetDbHostname(db_name, ns)
	return hostname
}

func GetDbPort(db_name string, ns string) int {
	if !sonic_db_init {
		DbInit()
	}
	port := swsscommon.SonicDBConfigGetDbPort(db_name, ns)
	return port
}

func GetDbTcpAddr(db_name string, ns string) string {
	if !sonic_db_init {
		DbInit()
	}
	hostname := GetDbHostName(db_name, ns)
	port := GetDbPort(db_name, ns)
	return hostname + ":" + strconv.Itoa(port)
}

func DbInit() {
	if sonic_db_init {
		return
	}
	if _, err := os.Stat(SONIC_DB_GLOBAL_CONFIG_FILE); err == nil || os.IsExist(err) {
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
}

func Init() {
	sonic_db_init = false
	// Clear database configuration
	swsscommon.SonicDBConfigUninitialize()
}

