// Package dbconfig provides a generic functions for parsing sonic database config file in system
// package main
package dbconfig

import (
	"fmt"
	internaldbconfig "github.com/sonic-net/sonic-gnmi/internal/dbconfig"
	"github.com/sonic-net/sonic-gnmi/swsscommon"
	"strconv"
)

const (
	SONIC_DB_GLOBAL_CONFIG_FILE string = internaldbconfig.GlobalConfigFile
	SONIC_DB_CONFIG_FILE        string = internaldbconfig.ConfigFile
	SONIC_DEFAULT_NAMESPACE     string = internaldbconfig.DefaultNamespace
	SONIC_DEFAULT_CONTAINER     string = ""
)

var sonic_db_init bool

// Convert exception to error
func CatchException(err *error) {
	if r := recover(); r != nil {
		*err = fmt.Errorf("%v", r)
	}
}

func GetDbDefaultNamespace() (ns string, err error) {
	return internaldbconfig.GetDbDefaultNamespace()
}

func CheckDbMultiNamespace() (ret bool, err error) {
	if err := DbInit(); err != nil {
		return false, err
	}
	return internaldbconfig.CheckDbMultiNamespace()
}

func GetDbNonDefaultNamespaces() (ns_list []string, err error) {
	if err := DbInit(); err != nil {
		return nil, err
	}
	return internaldbconfig.GetDbNonDefaultNamespaces()
}

func GetDbAllNamespaces() (ns_list []string, err error) {
	if err := DbInit(); err != nil {
		return nil, err
	}
	return internaldbconfig.GetDbAllNamespaces()
}

func GetDbNamespaceFromTarget(target string) (ns string, ret bool, err error) {
	if err := DbInit(); err != nil {
		return "", false, err
	}
	return internaldbconfig.GetDbNamespaceFromTarget(target)
}

func GetDbList(ns string) (db_list []string, err error) {
	if err := DbInit(); err != nil {
		return nil, err
	}
	return internaldbconfig.GetDbList(ns)
}

func GetDbSeparator(db_name string, ns string) (separator string, err error) {
	if err := DbInit(); err != nil {
		return "", err
	}
	return internaldbconfig.GetDbSeparator(db_name, ns)
}

func GetDbId(db_name string, ns string) (id int, err error) {
	if err := DbInit(); err != nil {
		return -1, err
	}
	return internaldbconfig.GetDbId(db_name, ns)
}

func GetDbSock(db_name string, ns string) (unix_socket_path string, err error) {
	if err := DbInit(); err != nil {
		return "", err
	}
	return internaldbconfig.GetDbSock(db_name, ns)
}

func GetDbHostName(db_name string, ns string) (hostname string, err error) {
	if err := DbInit(); err != nil {
		return "", err
	}
	return internaldbconfig.GetDbHostName(db_name, ns)
}

func GetDbPort(db_name string, ns string) (port int, err error) {
	if err := DbInit(); err != nil {
		return -1, err
	}
	return internaldbconfig.GetDbPort(db_name, ns)
}

func GetDbTcpAddr(db_name string, ns string) (addr string, err error) {
	if err := DbInit(); err != nil {
		return "", err
	}
	return internaldbconfig.GetDbTcpAddr(db_name, ns)
}

func CheckDbMultiInstance() (ret bool, err error) {
	if !sonic_db_init {
		err = DbInit()
		if err != nil {
			return false, err
		}
	}
	defer CatchException(&err)
	dbkey_vec := swsscommon.SonicDBConfigGetDbKeys()
	defer func() {
		swsscommon.DeleteVectorSonicDbKey(dbkey_vec)
	}()
	length := int(dbkey_vec.Size())
	// If there are more than one instances, this means that SONiC is using multi-namespace or multi-container
	return length > 1, err
}

func GetDbNonDefaultInstances() (dbkey_list []swsscommon.SonicDBKey, err error) {
	if !sonic_db_init {
		err = DbInit()
		if err != nil {
			return dbkey_list, err
		}
	}
	defer CatchException(&err)
	dbkey_vec := swsscommon.SonicDBConfigGetDbKeys()
	defer func() {
		swsscommon.DeleteVectorSonicDbKey(dbkey_vec)
	}()
	// Translate from vector to array
	length := int(dbkey_vec.Size())
	for i := 0; i < length; i += 1 {
		dbkey := dbkey_vec.Get(i)
		if dbkey.GetNetns() == SONIC_DEFAULT_NAMESPACE && dbkey.GetContainerName() == SONIC_DEFAULT_CONTAINER {
			continue
		}
		newkey := swsscommon.NewSonicDBKey()
		newkey.SetContainerName(dbkey.GetContainerName())
		newkey.SetNetns(dbkey.GetNetns())
		dbkey_list = append(dbkey_list, newkey)
	}
	return dbkey_list, err
}

func GetDbAllInstances() (dbkey_list []swsscommon.SonicDBKey, err error) {
	if !sonic_db_init {
		err = DbInit()
		if err != nil {
			return dbkey_list, err
		}
	}
	defer CatchException(&err)
	dbkey_vec := swsscommon.SonicDBConfigGetDbKeys()
	defer func() {
		swsscommon.DeleteVectorSonicDbKey(dbkey_vec)
	}()
	// Translate from vector to array
	length := int(dbkey_vec.Size())
	for i := 0; i < length; i += 1 {
		dbkey := dbkey_vec.Get(i)
		newkey := swsscommon.NewSonicDBKey()
		newkey.SetContainerName(dbkey.GetContainerName())
		newkey.SetNetns(dbkey.GetNetns())
		dbkey_list = append(dbkey_list, newkey)
	}
	return dbkey_list, err
}

func GetDbInstanceFromTarget(ns string, container string) (dbkey swsscommon.SonicDBKey, ret bool) {
	dbkey = swsscommon.NewSonicDBKey()
	dbkey.SetNetns(ns)
	dbkey.SetContainerName(container)
	_, err := GetDbListByDBKey(dbkey)
	if err != nil {
		return nil, false
	}
	return dbkey, true
}

func GetDbListByDBKey(dbkey swsscommon.SonicDBKey) (db_list []string, err error) {
	if !sonic_db_init {
		err = DbInit()
		if err != nil {
			return db_list, err
		}
	}
	defer CatchException(&err)
	db_vec := swsscommon.SonicDBConfigGetDbList(dbkey)
	defer func() {
		swsscommon.DeleteVectorString(db_vec)
	}()
	// Translate from vector to array
	length := int(db_vec.Size())
	for i := 0; i < length; i += 1 {
		dbname := db_vec.Get(i)
		db_list = append(db_list, dbname)
	}
	return db_list, err
}

func GetDbSeparatorByDBKey(db_name string, dbkey swsscommon.SonicDBKey) (separator string, err error) {
	if !sonic_db_init {
		err = DbInit()
		if err != nil {
			return "", err
		}
	}
	defer CatchException(&err)
	separator = swsscommon.SonicDBConfigGetSeparator(db_name, dbkey)
	return separator, err
}

func GetDbIdByDBKey(db_name string, dbkey swsscommon.SonicDBKey) (id int, err error) {
	if !sonic_db_init {
		err = DbInit()
		if err != nil {
			return -1, err
		}
	}
	defer CatchException(&err)
	id = swsscommon.SonicDBConfigGetDbId(db_name, dbkey)
	return id, err
}

func GetDbSockByDBKey(db_name string, dbkey swsscommon.SonicDBKey) (unix_socket_path string, err error) {
	if !sonic_db_init {
		err = DbInit()
		if err != nil {
			return "", err
		}
	}
	defer CatchException(&err)
	unix_socket_path = swsscommon.SonicDBConfigGetDbSock(db_name, dbkey)
	return unix_socket_path, err
}

func GetDbHostNameByDBKey(db_name string, dbkey swsscommon.SonicDBKey) (hostname string, err error) {
	if !sonic_db_init {
		err = DbInit()
		if err != nil {
			return "", err
		}
	}
	defer CatchException(&err)
	hostname = swsscommon.SonicDBConfigGetDbHostname(db_name, dbkey)
	return hostname, err
}

func GetDbPortByDBKey(db_name string, dbkey swsscommon.SonicDBKey) (port int, err error) {
	if !sonic_db_init {
		err = DbInit()
		if err != nil {
			return -1, err
		}
	}
	defer CatchException(&err)
	port = swsscommon.SonicDBConfigGetDbPort(db_name, dbkey)
	return port, err
}

func GetDbTcpAddrByDBKey(db_name string, dbkey swsscommon.SonicDBKey) (addr string, err error) {
	if !sonic_db_init {
		err = DbInit()
		if err != nil {
			return "", err
		}
	}
	hostname, err := GetDbHostNameByDBKey(db_name, dbkey)
	if err != nil {
		return "", err
	}
	port, err := GetDbPortByDBKey(db_name, dbkey)
	if err != nil {
		return "", err
	}
	return hostname + ":" + strconv.Itoa(port), err
}

func DbInit() (err error) {
	if sonic_db_init {
		return nil
	}
	if err := internaldbconfig.DbInit(); err != nil {
		return err
	}
	sonic_db_init = true
	return nil
}

func Init() (err error) {
	sonic_db_init = false
	return internaldbconfig.Init()
}
