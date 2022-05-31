//Package dbconfig provides a generic functions for parsing sonic database config file in system
//package main
package dbconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	io "io/ioutil"
	"os"
	"path/filepath"
	"strconv"
)

const (
	SONIC_DB_GLOBAL_CONFIG_FILE string = "/var/run/redis/sonic-db/database_global.json"
	SONIC_DB_CONFIG_FILE        string = "/var/run/redis/sonic-db/database_config.json"
	SONIC_DEFAULT_NAMESPACE     string = ""
)

var sonic_db_config = make(map[string]map[string]interface{})
var sonic_db_init bool
var sonic_db_multi_namespace bool

func GetDbDefaultNamespace() string {
	return SONIC_DEFAULT_NAMESPACE
}
func CheckDbMultiNamespace() bool {
	if !sonic_db_init {
		DbInit()
	}
	return sonic_db_multi_namespace
}
func GetDbNonDefaultNamespaces() []string {
	if !sonic_db_init {
		DbInit()
	}
	ns_list := make([]string, 0, len(sonic_db_config))
	for ns := range sonic_db_config {
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
	ns_list := make([]string, len(sonic_db_config))
	i := 0
	for ns := range sonic_db_config {
		ns_list[i] = ns
		i++
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
func GetDbList(ns string) map[string]interface{} {
	if !sonic_db_init {
		DbInit()
	}
	db_list, ok := sonic_db_config[ns]["DATABASES"].(map[string]interface{})
	if !ok {
		panic(fmt.Errorf("DATABASES' is not valid key in database_config.json file for namespace `%v` !", ns))
	}
	return db_list
}

func GetDbInst(db_name string, ns string) map[string]interface{} {
	if !sonic_db_init {
		DbInit()
	}
	db, ok := sonic_db_config[ns]["DATABASES"].(map[string]interface{})[db_name]
	if !ok {
		panic(fmt.Errorf("database name '%v' is not valid in database_config.json file for namespace `%v`!", db_name, ns))
	}
	inst_name, ok := db.(map[string]interface{})["instance"]
	if !ok {
		panic(fmt.Errorf("'instance' is not a valid field in database_config.json file for namespace `%v`!", ns))
	}
	inst, ok := sonic_db_config[ns]["INSTANCES"].(map[string]interface{})[inst_name.(string)]
	if !ok {
		panic(fmt.Errorf("instance name '%v' is not valid in database_config.json file for namespace `%v`!", inst_name, ns))
	}
	return inst.(map[string]interface{})
}

func GetDbSeparator(db_name string, ns string) string {
	if !sonic_db_init {
		DbInit()
	}
	db_list := GetDbList(ns)
	separator, ok := db_list[db_name].(map[string]interface{})["separator"]
	if !ok {
		panic(fmt.Errorf("'separator' is not a valid field in database_config.json file!"))
	}
	return separator.(string)
}

func GetDbId(db_name string, ns string) int {
	if !sonic_db_init {
		DbInit()
	}
	db_list := GetDbList(ns)
	id, ok := db_list[db_name].(map[string]interface{})["id"]
	if !ok {
		panic(fmt.Errorf("'id' is not a valid field in database_config.json file!"))
	}
	return int(id.(float64))
}

func GetDbSock(db_name string, ns string) string {
	if !sonic_db_init {
		DbInit()
	}
	inst := GetDbInst(db_name, ns)
	unix_socket_path, ok := inst["unix_socket_path"]
	if !ok {
		panic(fmt.Errorf("'unix_socket_path' is not a valid field in database_config.json file!"))
	}
	return unix_socket_path.(string)
}

func GetDbHostName(db_name string, ns string) string {
	if !sonic_db_init {
		DbInit()
	}
	inst := GetDbInst(db_name, ns)
	hostname, ok := inst["hostname"]
	if !ok {
		panic(fmt.Errorf("'hostname' is not a valid field in database_config.json file!"))
	}
	return hostname.(string)
}

func GetDbPort(db_name string, ns string) int {
	if !sonic_db_init {
		DbInit()
	}
	inst := GetDbInst(db_name, ns)
	port, ok := inst["port"]
	if !ok {
		panic(fmt.Errorf("'port' is not a valid field in database_config.json file!"))
	}
	return int(port.(float64))
}

func GetDbTcpAddr(db_name string, ns string) string {
	if !sonic_db_init {
		DbInit()
	}
	hostname := GetDbHostName(db_name, ns)
	port := GetDbPort(db_name, ns)
	return hostname + ":" + strconv.Itoa(port)
}

func DbGetNamespaceAndConfigFile(ns_to_cfgfile_map map[string]string) {
	data, err := io.ReadFile(SONIC_DB_GLOBAL_CONFIG_FILE)
	if err == nil {
		//Ref:https://stackoverflow.com/questions/18537257/how-to-get-the-directory-of-the-currently-running-file
		dir, err := filepath.Abs(filepath.Dir(SONIC_DB_GLOBAL_CONFIG_FILE))
		if err != nil {
			panic(err)
		}
		sonic_db_global_config := make(map[string]interface{})
		err = json.Unmarshal([]byte(data), &sonic_db_global_config)
		if err != nil {
			panic(err)
		}
		for _, entry := range sonic_db_global_config["INCLUDES"].([]interface{}) {
			ns, ok := entry.(map[string]interface{})["namespace"]
			if !ok {
				ns = SONIC_DEFAULT_NAMESPACE
			}
			_, ok = ns_to_cfgfile_map[ns.(string)]
			if ok {
				panic(fmt.Errorf("Global Database config file is not valid(multiple include for same namespace!"))
			}
			//Ref:https://www.geeksforgeeks.org/filepath-join-function-in-golang-with-examples/
			db_include_file := filepath.Join(dir, entry.(map[string]interface{})["include"].(string))
			ns_to_cfgfile_map[ns.(string)] = db_include_file
		}
		if len(ns_to_cfgfile_map) > 1 {
			sonic_db_multi_namespace = true
		} else {
			sonic_db_multi_namespace = false
		}

	} else if errors.Is(err, os.ErrNotExist) {
		// Ref: https://stackoverflow.com/questions/23452157/how-do-i-check-for-specific-types-of-error-among-those-returned-by-ioutil-readfi
		ns_to_cfgfile_map[SONIC_DEFAULT_NAMESPACE] = SONIC_DB_CONFIG_FILE
		sonic_db_multi_namespace = false
	} else {
		panic(err)
	}
}

func DbInit() {
	if sonic_db_init {
		return
	}
	ns_to_cfgfile_map := make(map[string]string)
	// Ref: https://stackoverflow.com/questions/14928826/passing-pointers-to-maps-in-golang
	DbGetNamespaceAndConfigFile(ns_to_cfgfile_map)
	for ns, db_cfg_file := range ns_to_cfgfile_map {
		data, err := io.ReadFile(db_cfg_file)
		if err != nil {
			panic(err)
		}
		db_config := make(map[string]interface{})
		err = json.Unmarshal([]byte(data), &db_config)
		if err != nil {
			panic(err)
		}
		sonic_db_config[ns] = db_config
	}
	sonic_db_init = true
}

func Init() {
	sonic_db_init = false
}
