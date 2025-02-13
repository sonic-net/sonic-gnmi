package config

import (
	"flag"
)

// The set of global flags that are used by all gNOI modules.

var (
	Args = flag.String("jsonin", "", "RPC Arguments in json format")
)

func ParseFlag() {
	flag.Parse()
}
