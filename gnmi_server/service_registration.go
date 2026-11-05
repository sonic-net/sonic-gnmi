package gnmi

import (
	gnmi "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/openconfig/gnoi/containerz"
	"github.com/openconfig/gnoi/debug"
	"github.com/openconfig/gnoi/factory_reset"
	"github.com/openconfig/gnoi/file"
	"github.com/openconfig/gnoi/healthz"
	"github.com/openconfig/gnoi/os"
	"github.com/openconfig/gnoi/system"
	"github.com/openconfig/gnsi/authz"
	certz "github.com/openconfig/gnsi/certz"
	"github.com/openconfig/gnsi/credentialz"
	"github.com/openconfig/gnsi/pathz"
	sonic "github.com/sonic-net/sonic-gnmi/proto/gnoi"
	jwt "github.com/sonic-net/sonic-gnmi/proto/gnoi/jwt"
	"github.com/sonic-net/sonic-gnmi/proto/gnoi/oras"
	"google.golang.org/grpc"
)

// These are already constructed services. Dependencies and listener lifetimes
// remain with NewServer, not the registration catalog.
type serviceImplementations struct {
	gnmi        gnmi.GNMIServer
	server      *Server
	file        *FileServer
	os          *OSServer
	containerz  *ContainerzServer
	healthz     *HealthzServer
	oras        *OrasServer
	certz       *GNSICertzServer
	authz       *GNSIAuthzServer
	pathz       *GNSIPathzServer
	credentialz *GNSICredentialzServer
	debug       *DebugServer
}

type serviceRegistration struct {
	name         string
	register     func(*grpc.Server)
	methods      map[string]rpcAuthorization
	writeEnabled bool
	translibOnly bool
	udsOnly      bool
}

// Generated descriptors define method names and stream shapes, not access
// intent. Keep each explicit permission next to its generated registration.
// Denied methods remain available to legacy modes and UDS as before.
func serviceRegistrations(impl serviceImplementations) []serviceRegistration {
	return []serviceRegistration{
		{
			name:     "gnmi.gNMI",
			register: func(s *grpc.Server) { gnmi.RegisterGNMIServer(s, impl.gnmi) },
			methods: map[string]rpcAuthorization{
				"Capabilities": {target: "gnmi"},
				"Get":          {target: "gnmi", requestTarget: true},
				"Set":          {target: "gnmi", write: true, requestTarget: true},
				"Subscribe":    {target: "gnmi", requestTarget: true, bufferFirstRequest: true},
			},
		},
		{
			name:     "gnoi.factory_reset.FactoryReset",
			register: func(s *grpc.Server) { factory_reset.RegisterFactoryResetServer(s, impl.server) },
			methods:  map[string]rpcAuthorization{"Start": {target: "gnoi", write: true}},
		},
		{
			name:     "gnsi.certz.v1.Certz",
			register: func(s *grpc.Server) { certz.RegisterCertzServer(s, impl.certz) },
			methods: map[string]rpcAuthorization{
				"AddProfile": {deny: true}, "DeleteProfile": {deny: true},
				"GetProfileList": {deny: true}, "CanGenerateCSR": {deny: true},
				"GetIntegrityManifest": {deny: true}, "Rotate": {deny: true},
			},
		},
		{
			name:     "gnsi.authz.v1.Authz",
			register: func(s *grpc.Server) { authz.RegisterAuthzServer(s, impl.authz) },
			methods: map[string]rpcAuthorization{
				"Probe": {deny: true}, "Get": {deny: true}, "Rotate": {deny: true},
			},
		},
		{
			name:     "gnsi.pathz.v1.Pathz",
			register: func(s *grpc.Server) { pathz.RegisterPathzServer(s, impl.pathz) },
			methods: map[string]rpcAuthorization{
				"Probe": {deny: true}, "Get": {deny: true}, "Rotate": {deny: true},
			},
		},
		{
			name:     "gnsi.credentialz.v1.Credentialz",
			register: func(s *grpc.Server) { credentialz.RegisterCredentialzServer(s, impl.credentialz) },
			methods: map[string]rpcAuthorization{
				"CanGenerateKey": {deny: true}, "GetPublicKeys": {deny: true},
				"RotateAccountCredentials": {deny: true}, "RotateHostParameters": {deny: true},
			},
		},
		{
			name:     "gnoi.sonic_jwt.SonicJwtService",
			register: func(s *grpc.Server) { jwt.RegisterSonicJwtServiceServer(s, impl.server) },
			methods: map[string]rpcAuthorization{
				"Authenticate": {deny: true}, "Refresh": {target: "gnoi", write: true},
			},
		},
		{
			name: "gnoi.system.System", writeEnabled: true,
			register: func(s *grpc.Server) { system.RegisterSystemServer(s, impl.server) },
			methods: map[string]rpcAuthorization{
				"Time": {target: "gnoi"}, "RebootStatus": {target: "gnoi"},
				"SwitchControlProcessor": {target: "gnoi", write: true},
				"Reboot":                 {target: "gnoi", write: true}, "CancelReboot": {target: "gnoi", write: true},
				"KillProcess": {target: "gnoi", write: true}, "Ping": {target: "gnoi", write: true},
				"Traceroute": {target: "gnoi", write: true}, "SetPackage": {target: "gnoi", write: true},
			},
		},
		{
			name: "gnoi.file.File", writeEnabled: true,
			register: func(s *grpc.Server) { file.RegisterFileServer(s, impl.file) },
			methods: map[string]rpcAuthorization{
				"Stat": {target: "gnoi"}, "Get": {target: "gnoi"},
				"Put": {target: "gnoi", write: true}, "Remove": {target: "gnoi", write: true},
				"TransferToRemote": {target: "gnoi", write: true},
			},
		},
		{
			name: "gnoi.os.OS", writeEnabled: true,
			register: func(s *grpc.Server) { os.RegisterOSServer(s, impl.os) },
			methods: map[string]rpcAuthorization{
				"Install": {target: "gnoi", write: true}, "Activate": {target: "gnoi", write: true}, "Verify": {target: "gnoi"},
			},
		},
		{
			name: "gnoi.containerz.Containerz", writeEnabled: true,
			register: func(s *grpc.Server) { containerz.RegisterContainerzServer(s, impl.containerz) },
			methods: map[string]rpcAuthorization{
				"Deploy": {target: "gnoi", write: true}, "Remove": {deny: true},
				"Start": {deny: true}, "Stop": {deny: true}, "List": {deny: true}, "Log": {deny: true},
			},
		},
		{
			name: "gnoi.healthz.Healthz", writeEnabled: true,
			register: func(s *grpc.Server) { healthz.RegisterHealthzServer(s, impl.healthz) },
			methods: map[string]rpcAuthorization{
				"Get": {target: "gnoi"}, "Artifact": {target: "gnoi"}, "Acknowledge": {target: "gnoi", write: true},
				"List": {deny: true}, "Check": {deny: true},
			},
		},
		{
			// ORAS writes only to an allowlisted staging area, independently of gNMI write enablement.
			name:     "sonic.gnoi.oras.v1.Oras",
			register: func(s *grpc.Server) { oras.RegisterOrasServer(s, impl.oras) },
			methods:  map[string]rpcAuthorization{"Pull": {target: "gnoi", write: true}},
		},
		{
			name: "gnoi.sonic.SonicService", translibOnly: true,
			register: func(s *grpc.Server) { sonic.RegisterSonicServiceServer(s, impl.server) },
			methods: map[string]rpcAuthorization{
				"ShowTechsupport": {target: "gnoi"}, "CopyConfig": {target: "gnoi", write: true},
				"ImageInstall": {target: "gnoi", write: true}, "ImageRemove": {target: "gnoi", write: true},
				"ImageDefault": {target: "gnoi", write: true}, "ClearNeighbors": {target: "gnoi", write: true},
			},
		},
		{
			name:     "gnoi.sonic.Debug",
			register: func(s *grpc.Server) { sonic.RegisterDebugServer(s, impl.server) },
			methods:  map[string]rpcAuthorization{"GetSubscribePreferences": {target: "gnmi"}},
		},
		{
			name: "gnoi.debug.Debug", writeEnabled: true, udsOnly: true,
			register: func(s *grpc.Server) { debug.RegisterDebugServer(s, impl.debug) },
			methods:  map[string]rpcAuthorization{"Debug": {deny: true}},
		},
	}
}

func registerServices(s *grpc.Server, config *Config, impl serviceImplementations, uds bool) {
	for _, service := range serviceRegistrations(impl) {
		if service.udsOnly && !uds || service.translibOnly && !config.EnableTranslibWrite || service.writeEnabled && !config.EnableTranslibWrite && !config.EnableNativeWrite {
			continue
		}
		service.register(s)
	}
}

func serviceAuthorizations() map[string]rpcAuthorization {
	methods := make(map[string]rpcAuthorization)
	for _, service := range serviceRegistrations(serviceImplementations{}) {
		for method, permission := range service.methods {
			methods["/"+service.name+"/"+method] = permission
		}
	}
	return methods
}
