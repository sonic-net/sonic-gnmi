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

func TestServiceDeclarationCoverage(t *testing.T) {
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
		if !ok || len(registered.Methods) != len(service.methods) {
			t.Fatalf("%s: generated methods %v do not match declarations %v", service.name, registered.Methods, service.methods)
		}
		for _, method := range registered.Methods {
			permission, ok := service.methods[method.Name]
			t.Logf("DECLARATION /%s/%s deny=%t target=%q write=%t requestTarget=%t bufferFirstRequest=%t", service.name, method.Name,
				permission.deny, permission.target, permission.write, permission.requestTarget, permission.bufferFirstRequest)
			if !ok {
				t.Errorf("%s/%s has no declaration", service.name, method.Name)
			} else if permission.deny && permission != (rpcAuthorization{deny: true}) || !permission.deny && permission.target == "" {
				t.Errorf("%s/%s has an ambiguous declaration: %+v", service.name, method.Name, permission)
			}
		}
	}
	// Each interceptor owns its map; modifying one cannot change another server.
	first := testRPCAuthorizationInterceptor("gnmi_readonly")
	second := testRPCAuthorizationInterceptor("gnmi_readonly")
	delete(first.methods, "/gnmi.gNMI/Get")
	if _, ok := second.methods["/gnmi.gNMI/Get"]; !ok {
		t.Fatal("authorization declarations are shared across server instances")
	}
}
