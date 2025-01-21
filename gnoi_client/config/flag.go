package config

import (
	"flag"
)

var (
	Module     = flag.String("module", "System", "gNOI Module")
	Rpc        = flag.String("rpc", "Time", "rpc call in specified module to call")
	Target     = flag.String("target", "localhost:8080", "Address:port of gNOI Server")
	Args       = flag.String("jsonin", "", "RPC Arguments in json format")
	JwtToken   = flag.String("jwt_token", "", "JWT Token if required")
	TargetName = flag.String("target_name", "hostname.com", "The target name use to verify the hostname returned by TLS handshake")
)

func ParseFlag() {
	flag.Parse()
}