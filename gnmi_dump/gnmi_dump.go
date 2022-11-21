package main

import (
	"flag"
	"fmt"
	"github.com/sonic-net/sonic-gnmi/common_utils"
)

const help = `
gnmi_dump is used to dump internal counters for debugging purpose,
including GNMI request counter, GNOI request counter and DBUS request counter.
`

func main() {
	flag.Usage = func() {
		fmt.Print(help)
	}
	flag.Parse()
	var counters [common_utils.COUNTER_SIZE]uint64
	err := common_utils.GetMemCounters(&counters)
	if err != nil {
		fmt.Printf("Error: Fail to read counters, %v", err)
		return
	}
	fmt.Printf("Dump GNMI counters\n")
	for i := 0; i < int(common_utils.COUNTER_SIZE); i++ {
		cnt := common_utils.CounterType(i)
		fmt.Printf("%v---%v\n", cnt.String(), counters[i])
	}
}
