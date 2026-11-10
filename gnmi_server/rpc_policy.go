package gnmi

import "strings"

type rpcPermission uint8

const (
	rpcReadWrite rpcPermission = iota // Missing method exceptions require RW.
	rpcReadOnly
	rpcDeny
)

type rpcServicePolicy struct {
	target     string
	exceptions map[string]rpcPermission
}

// Only known services have a trusted role target. Unlisted services are denied.
// Ordinary RW methods need no declaration. These maps are read-only.
var rpcPolicies = map[string]rpcServicePolicy{
	"gnmi.gNMI":                       {"gnmi", map[string]rpcPermission{"Get": rpcReadOnly, "Subscribe": rpcReadOnly}},
	"gnoi.sonic.Debug":                {target: "gnmi"},
	"gnoi.factory_reset.FactoryReset": {target: "gnoi"},
	"gnoi.system.System":              {target: "gnoi"},
	"gnoi.file.File":                  {target: "gnoi"},
	"gnoi.os.OS":                      {target: "gnoi"},
	"gnoi.containerz.Containerz":      {"gnoi", map[string]rpcPermission{"Remove": rpcDeny, "Start": rpcDeny, "Stop": rpcDeny, "List": rpcDeny, "Log": rpcDeny}},
	"gnoi.healthz.Healthz":            {"gnoi", map[string]rpcPermission{"List": rpcDeny, "Check": rpcDeny}},
	"gnoi.sonic_jwt.SonicJwtService":  {"gnoi", map[string]rpcPermission{"Authenticate": rpcDeny}},
	"gnoi.sonic.SonicService":         {target: "gnoi"},
	"sonic.gnoi.oras.v1.Oras":         {target: "gnoi"},
}

func rpcAccess(method string) (target string, write bool) {
	service, name, ok := strings.Cut(strings.TrimPrefix(method, "/"), "/")
	if !strings.HasPrefix(method, "/") || !ok || name == "" || strings.Contains(name, "/") {
		return "", true
	}
	policy := rpcPolicies[service]
	permission := policy.exceptions[name]
	if policy.target == "" || permission == rpcDeny {
		return "", true
	}
	return policy.target, permission != rpcReadOnly
}
