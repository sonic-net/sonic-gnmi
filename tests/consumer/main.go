package main

import (
	"fmt"
	server "github.com/sonic-net/sonic-gnmi/gnmi_server"
	dbconfig "github.com/sonic-net/sonic-gnmi/sonic_db_config"
)

func main() {
	config := server.Config{Port: 8080}
	namespace, err := dbconfig.GetDbDefaultNamespace()
	if err != nil {
		panic(err)
	}
	fmt.Printf("public gNMI config port=%d default_namespace=%q\n", config.Port, namespace)
}
