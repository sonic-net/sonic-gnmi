package main

import (
	"fmt"
	"github.com/sonic-net/sonic-gnmi/common_utils"
)

func main() {
	var counters [len(common_utils.CountersName)]uint64
	err := common_utils.GetMemCounters(&counters)
	if err != nil {
		fmt.Printf("Error: Fail to read counters, %v", err)
		return
	}
	fmt.Printf("Dump GNMI counters\n")
	for i := 0; i < len(common_utils.CountersName); i++ {
		fmt.Printf("%v---%v\n", common_utils.CountersName[i], counters[i])
	}
}
