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
	target       string
	readOnly     []string
	denied       []string
	writeEnabled bool
	translibOnly bool
	udsOnly      bool
}

// Known service targets require readwrite by default. Keep only read-only and
// denied exceptions beside registration; names such as Get/List imply no access
// level. An empty target denies the service in central mode. Legacy modes and
// UDS retain their existing handlers, including explicitly denied methods.
func serviceRegistrations(impl serviceImplementations) []serviceRegistration {
	return []serviceRegistration{
		{
			name:     "gnmi.gNMI",
			register: func(s *grpc.Server) { gnmi.RegisterGNMIServer(s, impl.gnmi) },
			target:   "gnmi", readOnly: []string{"Capabilities", "Get", "Subscribe"},
		},
		{
			name:     "gnoi.factory_reset.FactoryReset",
			register: func(s *grpc.Server) { factory_reset.RegisterFactoryResetServer(s, impl.server) },
			target:   "gnoi",
		},
		{
			name:     "gnsi.certz.v1.Certz",
			register: func(s *grpc.Server) { certz.RegisterCertzServer(s, impl.certz) },
			denied:   []string{"AddProfile", "DeleteProfile", "GetProfileList", "CanGenerateCSR", "GetIntegrityManifest", "Rotate"},
		},
		{
			name:     "gnsi.authz.v1.Authz",
			register: func(s *grpc.Server) { authz.RegisterAuthzServer(s, impl.authz) },
			denied:   []string{"Probe", "Get", "Rotate"},
		},
		{
			name:     "gnsi.pathz.v1.Pathz",
			register: func(s *grpc.Server) { pathz.RegisterPathzServer(s, impl.pathz) },
			denied:   []string{"Probe", "Get", "Rotate"},
		},
		{
			name:     "gnsi.credentialz.v1.Credentialz",
			register: func(s *grpc.Server) { credentialz.RegisterCredentialzServer(s, impl.credentialz) },
			denied:   []string{"CanGenerateKey", "GetPublicKeys", "RotateAccountCredentials", "RotateHostParameters"},
		},
		{
			name:     "gnoi.sonic_jwt.SonicJwtService",
			register: func(s *grpc.Server) { jwt.RegisterSonicJwtServiceServer(s, impl.server) },
			target:   "gnoi", denied: []string{"Authenticate"},
		},
		{
			name: "gnoi.system.System", writeEnabled: true,
			register: func(s *grpc.Server) { system.RegisterSystemServer(s, impl.server) },
			target:   "gnoi", readOnly: []string{"Time", "RebootStatus"},
		},
		{
			name: "gnoi.file.File", writeEnabled: true,
			register: func(s *grpc.Server) { file.RegisterFileServer(s, impl.file) },
			target:   "gnoi", readOnly: []string{"Stat", "Get"},
		},
		{
			name: "gnoi.os.OS", writeEnabled: true,
			register: func(s *grpc.Server) { os.RegisterOSServer(s, impl.os) },
			target:   "gnoi", readOnly: []string{"Verify"},
		},
		{
			name: "gnoi.containerz.Containerz", writeEnabled: true,
			register: func(s *grpc.Server) { containerz.RegisterContainerzServer(s, impl.containerz) },
			target:   "gnoi", denied: []string{"Remove", "Start", "Stop", "List", "Log"},
		},
		{
			name: "gnoi.healthz.Healthz", writeEnabled: true,
			register: func(s *grpc.Server) { healthz.RegisterHealthzServer(s, impl.healthz) },
			target:   "gnoi", readOnly: []string{"Get", "Artifact"}, denied: []string{"List", "Check"},
		},
		{
			// ORAS writes only to an allowlisted staging area, independently of gNMI write enablement.
			name:     "sonic.gnoi.oras.v1.Oras",
			register: func(s *grpc.Server) { oras.RegisterOrasServer(s, impl.oras) },
			target:   "gnoi",
		},
		{
			name: "gnoi.sonic.SonicService", translibOnly: true,
			register: func(s *grpc.Server) { sonic.RegisterSonicServiceServer(s, impl.server) },
			target:   "gnoi", readOnly: []string{"ShowTechsupport"},
		},
		{
			name:     "gnoi.sonic.Debug",
			register: func(s *grpc.Server) { sonic.RegisterDebugServer(s, impl.server) },
			target:   "gnmi", readOnly: []string{"GetSubscribePreferences"},
		},
		{
			name: "gnoi.debug.Debug", writeEnabled: true, udsOnly: true,
			register: func(s *grpc.Server) { debug.RegisterDebugServer(s, impl.debug) },
			denied:   []string{"Debug"},
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

func serviceAuthorizations() map[string]serviceRegistration {
	services := make(map[string]serviceRegistration)
	for _, service := range serviceRegistrations(serviceImplementations{}) {
		services[service.name] = service
	}
	return services
}
