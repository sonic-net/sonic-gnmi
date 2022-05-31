package main

import (
	"fmt"
	"reflect"
	"github.com/sonic-net/sonic-gnmi/common_utils"
)

func main() {
	var counters common_utils.ShareCounters
	err := common_utils.GetMemCounters(&counters)
	if err != nil {
		fmt.Printf("Error: Fail to read counters, %v", err)
		return
	}
	var typeInfo = reflect.TypeOf(counters)
	var valInfo = reflect.ValueOf(counters)
	num := typeInfo.NumField()
	fmt.Printf("Dump GNMI counters\n")
	for i := 0; i < num; i++ {
		key := typeInfo.Field(i).Name
		val := valInfo.Field(i).Interface()
		fmt.Printf("%v --- %v\n", key, val)
	}
}
