package main

import (
	"flag"
	"fmt"
	"github.com/sonic-net/sonic-gnmi/common_utils"
)

const help = `
gnmi_dump is used to dump internal counters for debugging purpose, including GNMI request counter, GNOI request counter and DBUS request counter.
`

func main() {
	flag.Usage = func() {
		fmt.Print(help)
	}
	flag.Parse()
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
