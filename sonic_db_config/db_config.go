// Package dbconfig provides a generic functions for parsing sonic database config file in system
package dbconfig

import (
    "encoding/json"
    "fmt"
    "strconv"
    io "io/ioutil"
)

const (
    SONIC_DB_CONFIG_FILE string = "/var/run/redis/sonic-db/database_config.json"
)

var sonic_db_config = make(map[string]interface{})
var sonic_db_init bool

func GetDbList()(map[string]interface{}) {
    if !sonic_db_init {
        DbInit()
    }
    db_list, ok := sonic_db_config["DATABASES"].(map[string]interface{})
    if !ok {
        panic(fmt.Errorf("DATABASES' is not valid key in database_config.json file!"))
    }
    return db_list
}

func GetDbInst(db_name string)(map[string]interface{}) {
    if !sonic_db_init {
        DbInit()
    }
    db, ok := sonic_db_config["DATABASES"].(map[string]interface{})[db_name]
    if !ok {
        panic(fmt.Errorf("database name '%v' is not valid in database_config.json file!", db_name))
    }
    inst_name, ok := db.(map[string]interface{})["instance"]
    if !ok {
        panic(fmt.Errorf("'instance' is not a valid field in database_config.json file!"))
    }
    inst, ok := sonic_db_config["INSTANCES"].(map[string]interface{})[inst_name.(string)]
    if !ok {
        panic(fmt.Errorf("instance name '%v' is not valid in database_config.json file!", inst_name))
    }
    return inst.(map[string]interface{})
}

func GetDbSeparator(db_name string)(string) {
    if !sonic_db_init {
        DbInit()
    }
    db_list := GetDbList()
    separator, ok := db_list[db_name].(map[string]interface{})["separator"]
    if !ok {
        panic(fmt.Errorf("'separator' is not a valid field in database_config.json file!"))
    }
    return separator.(string)
}

func GetDbId(db_name string)(int) {
    if !sonic_db_init {
        DbInit()
    }
    db_list := GetDbList()
    id, ok := db_list[db_name].(map[string]interface{})["id"]
    if !ok {
        panic(fmt.Errorf("'id' is not a valid field in database_config.json file!"))
    }
    return int(id.(float64))
}

func GetDbSock(db_name string)(string) {
    if !sonic_db_init {
        DbInit()
    }
    inst := GetDbInst(db_name)
    unix_socket_path, ok := inst["unix_socket_path"]
    if !ok {
        panic(fmt.Errorf("'unix_socket_path' is not a valid field in database_config.json file!"))
    }
    return unix_socket_path.(string)
}

func GetDbHostName(db_name string)(string) {
    if !sonic_db_init {
        DbInit()
    }
    inst := GetDbInst(db_name)
    hostname, ok := inst["hostname"]
    if !ok {
        panic(fmt.Errorf("'hostname' is not a valid field in database_config.json file!"))
    }
    return hostname.(string)
}

func GetDbPort(db_name string)(int) {
    if !sonic_db_init {
        DbInit()
    }
    inst := GetDbInst(db_name)
    port, ok := inst["port"]
    if !ok {
        panic(fmt.Errorf("'port' is not a valid field in database_config.json file!"))
    }
    return int(port.(float64))
}

func GetDbTcpAddr(db_name string)(string) {
    if !sonic_db_init {
        DbInit()
    }
    hostname := GetDbHostName(db_name)
    port := GetDbPort(db_name)
    return hostname + ":" + strconv.Itoa(port)
}

func DbInit() {
    if sonic_db_init {
        return
    }
    data, err := io.ReadFile(SONIC_DB_CONFIG_FILE)
    if err != nil {
        panic(err)
    } else {
        err = json.Unmarshal([]byte(data), &sonic_db_config)
        if err != nil {
            panic(err)
        }
        sonic_db_init = true
    }
}

func init() {
    sonic_db_init = false
}
