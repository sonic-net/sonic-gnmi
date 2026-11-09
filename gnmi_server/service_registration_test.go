package gnmi

import (
	"encoding/json"
	"fmt"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

// Capture the actual generated method names and stream shapes for each existing
// transport/write combination. Reflection is infrastructure, reported separately.
func TestServiceRegistrationInventory(t *testing.T) {
	for _, uds := range []bool{false, true} {
		for _, native := range []bool{false, true} {
			for _, translib := range []bool{false, true} {
				t.Run(fmt.Sprintf("uds=%t/native=%t/translib=%t", uds, native, translib), func(t *testing.T) {
					srv := &Server{config: &Config{EnableNativeWrite: native, EnableTranslibWrite: translib}}
					s := grpc.NewServer()
					defer s.Stop()
					registerServices(s, srv.config, serviceImplementations{gnmi: srv, server: srv,
						file: &FileServer{}, os: &OSServer{}, containerz: &ContainerzServer{}, healthz: &HealthzServer{},
						oras: &OrasServer{}, certz: &GNSICertzServer{}, authz: &GNSIAuthzServer{}, pathz: &GNSIPathzServer{},
						credentialz: &GNSICredentialzServer{}, debug: &DebugServer{}}, uds)
					wantServices := map[string]bool{
						"gnmi.gNMI": true, "gnoi.factory_reset.FactoryReset": true,
						"gnsi.certz.v1.Certz": true, "gnsi.authz.v1.Authz": true,
						"gnsi.pathz.v1.Pathz": true, "gnsi.credentialz.v1.Credentialz": true,
						"gnoi.sonic_jwt.SonicJwtService": true, "sonic.gnoi.oras.v1.Oras": true,
						"gnoi.sonic.Debug": true, "gnoi.sonic.SonicService": translib,
						"gnoi.system.System": native || translib, "gnoi.file.File": native || translib,
						"gnoi.os.OS": native || translib, "gnoi.containerz.Containerz": native || translib,
						"gnoi.healthz.Healthz": native || translib, "gnoi.debug.Debug": uds && (native || translib),
					}
					actual := s.GetServiceInfo()
					for name, want := range wantServices {
						if _, got := actual[name]; got != want {
							t.Errorf("%s registered=%t, want %t", name, got, want)
						}
					}
					for name := range actual {
						if !wantServices[name] {
							t.Errorf("unexpected service %s", name)
						}
					}
					product, err := json.Marshal(s.GetServiceInfo())
					if err != nil {
						t.Fatal(err)
					}
					t.Logf("PRODUCT_INVENTORY %s", product)
					reflection.Register(s)
					infra := map[string]grpc.ServiceInfo{}
					for name, info := range s.GetServiceInfo() {
						if name == "grpc.reflection.v1.ServerReflection" || name == "grpc.reflection.v1alpha.ServerReflection" {
							infra[name] = info
						}
					}
					infrastructure, err := json.Marshal(infra)
					if err != nil {
						t.Fatal(err)
					}
					t.Logf("REFLECTION_INVENTORY %s", infrastructure)
				})
			}
		}
	}
}

func TestServiceAuthorizationExceptions(t *testing.T) {
	seen := map[string]bool{}
	for _, service := range serviceRegistrations(serviceImplementations{
		gnmi: &Server{}, server: &Server{}, file: &FileServer{}, os: &OSServer{},
		containerz: &ContainerzServer{}, healthz: &HealthzServer{}, oras: &OrasServer{},
		certz: &GNSICertzServer{}, authz: &GNSIAuthzServer{}, pathz: &GNSIPathzServer{},
		credentialz: &GNSICredentialzServer{}, debug: &DebugServer{},
	}) {
		s := grpc.NewServer()
		service.register(s)
		info := s.GetServiceInfo()
		s.Stop()
		if seen[service.name] || len(info) != 1 {
			t.Fatalf("duplicate or mismatched registration: %s: %v", service.name, info)
		}
		seen[service.name] = true
		registered, ok := info[service.name]
		if !ok {
			t.Fatalf("%s: generated service does not match registration", service.name)
		}
		methods := map[string]bool{}
		for _, method := range registered.Methods {
			methods[method.Name] = true
		}
		if service.target != "" && service.target != "gnmi" && service.target != "gnoi" {
			t.Errorf("%s has unknown target %q", service.name, service.target)
		}
		exceptions := map[string]bool{}
		for _, method := range append(append([]string{}, service.readOnly...), service.denied...) {
			if !methods[method] || exceptions[method] {
				t.Errorf("%s/%s: missing method or duplicate/conflicting exception", service.name, method)
			}
			exceptions[method] = true
		}
	}
	// Each interceptor owns its map; modifying one cannot change another server.
	first := testRPCAuthorizationInterceptor("gnmi_readonly")
	second := testRPCAuthorizationInterceptor("gnmi_readonly")
	first.services["gnmi.gNMI"].readOnly[0] = "Changed"
	delete(first.services, "gnoi.file.File")
	if second.services["gnmi.gNMI"].readOnly[0] != "Capabilities" || second.services["gnoi.file.File"].target != "gnoi" {
		t.Fatal("authorization declarations are shared across server instances")
	}
}
