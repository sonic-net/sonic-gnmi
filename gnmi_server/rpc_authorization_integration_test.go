package gnmi

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	log "github.com/golang/glog"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	healthzpb "github.com/openconfig/gnoi/healthz"
	systempb "github.com/openconfig/gnoi/system"
	"github.com/sonic-net/sonic-gnmi/common_utils"
	"github.com/sonic-net/sonic-gnmi/pkg/interceptors"
	"github.com/sonic-net/sonic-gnmi/pkg/interceptors/dpuproxy"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestNewServerRegistrationAuthorization(t *testing.T) {
	var credentials, inner, proxy atomic.Int32
	var readonly atomic.Bool
	completed := make(chan string, 16)
	patch := gomonkey.ApplyFunc(ClientCertAuthenAndAuthor, func(ctx context.Context, _ string, _ bool) (context.Context, error) {
		credentials.Add(1)
		rc, ctx := common_utils.GetContext(ctx)
		role := "gnoi_readwrite"
		if readonly.Load() {
			role = "gnoi_readonly"
		}
		rc.Auth.User, rc.Auth.Roles = "reader", []string{role, "gnmi_readonly"}
		return ctx, nil
	})
	defer patch.Reset()
	patch.ApplyFunc(log.Infof, func(format string, args ...interface{}) {
		line := fmt.Sprintf(format, args...)
		if strings.HasPrefix(line, "RPC_COMPLETION ") {
			completed <- line
		}
	})
	patch.ApplyMethod(reflect.TypeOf(&dpuproxy.DPUProxy{}), "UnaryInterceptor", func(*dpuproxy.DPUProxy) grpc.UnaryServerInterceptor {
		return func(ctx context.Context, req interface{}, _ *grpc.UnaryServerInfo, h grpc.UnaryHandler) (interface{}, error) {
			proxy.Add(1)
			if !isUnixPeer(ctx) && (!rpcAuthorizationCompleted(ctx) || credentials.Load() != 1) {
				t.Error("proxy entered before exactly one central authentication")
			}
			return h(ctx, req)
		}
	})
	for _, uds := range []bool{false, true} {
		t.Run(map[bool]string{false: "TCP", true: "UDS"}[uds], func(t *testing.T) {
			credentials.Store(0)
			readonly.Store(false)
			inner.Store(0)
			proxy.Store(0)
			cfg := &Config{BindAddress: "127.0.0.1", EnableNativeWrite: true, UserAuth: AuthTypes{"cert": true}, ConfigTableName: gnmiClientCertTable}
			if uds {
				cfg.UnixSocket = filepath.Join(t.TempDir(), "gnmi.sock")
			}
			chain, err := interceptors.NewServerChain()
			if err != nil {
				t.Fatal(err)
			}
			defer chain.Close()
			srv, err := NewServerWithChain(cfg, nil, []grpc.ServerOption{grpc.ChainUnaryInterceptor(
				func(ctx context.Context, req interface{}, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
					inner.Add(1)
					return handler(ctx, req)
				}), grpc.ChainStreamInterceptor(
				func(srv interface{}, stream grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
					inner.Add(1)
					return handler(srv, stream)
				})}, chain)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- srv.Serve() }()
			t.Cleanup(func() {
				srv.ForceStop()
				select {
				case err := <-done:
					if err != nil {
						t.Error(err)
					}
				case <-time.After(time.Second):
					t.Error("server did not stop")
				}
			})
			address := srv.Address()
			if uds {
				address = "unix://" + cfg.UnixSocket
			}
			conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			response, err := systempb.NewSystemClient(conn).Time(ctx, &systempb.TimeRequest{})
			if err != nil || response.GetTime() == 0 {
				t.Fatalf("production Time: response=%v err=%v", response, err)
			}
			select {
			case line := <-completed:
				if !strings.Contains(line, `"code":"OK"`) {
					t.Fatal(line)
				}
			case <-ctx.Done():
				t.Fatal("missing allowed completion")
			}
			_, err = healthzpb.NewHealthzClient(conn).Check(ctx, &healthzpb.CheckRequest{})
			select {
			case line := <-completed:
				want := `"code":"PermissionDenied"`
				if uds {
					want = `"code":"Unimplemented"`
				}
				if !strings.Contains(line, want) {
					t.Fatal(line)
				}
			case <-ctx.Done():
				t.Fatal("missing denied completion")
			}
			if uds {
				if status.Code(err) != codes.Unimplemented || credentials.Load() != 0 || inner.Load() != 2 {
					t.Fatalf("UDS bypass: %v credentials=%d inner=%d", err, credentials.Load(), inner.Load())
				}
				return
			}
			if status.Code(err) != codes.PermissionDenied || credentials.Load() != 1 || inner.Load() != 1 || proxy.Load() != 1 {
				t.Fatalf("explicit deny before credentials/inner: %v credentials=%d inner=%d", err, credentials.Load(), inner.Load())
			}
			readonly.Store(true)
			_, err = systempb.NewSystemClient(conn).Reboot(ctx, &systempb.RebootRequest{})
			if status.Code(err) != codes.PermissionDenied || inner.Load() != 1 || proxy.Load() != 1 {
				t.Fatalf("readonly reboot: %v inner=%d", err, inner.Load())
			}
			stream, err := gnmipb.NewGNMIClient(conn).Subscribe(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := stream.CloseSend(); err != nil {
				t.Fatal(err)
			}
			if _, err := stream.Recv(); status.Code(err) != codes.Aborted || inner.Load() != 1 {
				t.Fatalf("production Subscribe initial EOF: %v inner=%d", err, inner.Load())
			}
			if credentials.Load() != 3 {
				t.Fatalf("credential calls=%d, want Time/Reboot/Subscribe only", credentials.Load())
			}
			for i := 0; i < 2; i++ {
				select {
				case <-completed:
				case <-ctx.Done():
					t.Fatal("missing completion")
				}
			}
		})
	}
	// The original three-argument constructor still creates its own gate even
	// without a logging/proxy chain supplied by the production caller.
	srv, err := NewServer(&Config{BindAddress: "127.0.0.1", EnableNativeWrite: true, UserAuth: AuthTypes{"cert": true}, ConfigTableName: gnmiClientCertTable}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve()
	defer srv.ForceStop()
	conn, err := grpc.NewClient(srv.Address(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	before := credentials.Load()
	_, err = healthzpb.NewHealthzClient(conn).Check(ctx, &healthzpb.CheckRequest{})
	if status.Code(err) != codes.PermissionDenied || credentials.Load() != before {
		t.Fatalf("no-chain early denial: %v", err)
	}
}

func TestCentralAuthorizationWithInjectedAuthentication(t *testing.T) {
	cfg := &Config{UserAuth: AuthTypes{"cert": true}, ConfigTableName: gnmiClientCertTable}
	roles := []string{"gnoi_readwrite"}
	calls := 0
	patch := gomonkey.ApplyFunc(ClientCertAuthenAndAuthor, func(ctx context.Context, table string, crl bool) (context.Context, error) {
		calls++
		if table != gnmiClientCertTable || crl {
			t.Fatalf("certificate config changed: %q %v", table, crl)
		}
		rc, ctx := common_utils.GetContext(ctx)
		rc.Auth.User, rc.Auth.Roles = "reader", roles
		return ctx, nil
	})
	defer patch.Reset()
	interceptor, reason := configuredRPCAuthorizationInterceptor(cfg)
	if interceptor == nil || reason != "" {
		t.Fatalf("central mode unavailable: %q", reason)
	}
	var authorized context.Context
	_, err := interceptor.UnaryInterceptor()(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: "/gnoi.file.File/Stat"},
		func(ctx context.Context, _ interface{}) (interface{}, error) {
			authorized = ctx
			_, err := authorizeFileRead(cfg, ctx)
			return nil, err
		})
	if err != nil || calls != 1 || authorized == nil {
		t.Fatalf("central to strict File read: err=%v credential calls=%d", err, calls)
	}
	if _, err := authenticate(cfg, authorized, "gnmi", false); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("completed authorization accepted different target: %v", err)
	}
	rc, _ := common_utils.GetContext(authorized)
	rc.Auth.Roles = []string{"gnoi_noaccess"}
	if _, err := authorizeFileRead(cfg, authorized); status.Code(err) != codes.PermissionDenied || calls != 1 {
		t.Fatalf("completed authorization did not revalidate roles: err=%v calls=%d", err, calls)
	}
	// A completed read decision must not authorize a stricter write handler.
	roles = []string{"gnmi_config_db_readonly"}
	_, err = interceptor.UnaryInterceptor()(context.Background(), testGetRequest("CONFIG_DB"), &grpc.UnaryServerInfo{FullMethod: "/gnmi.gNMI/Get"}, func(ctx context.Context, _ interface{}) (interface{}, error) {
		_, err := authenticate(cfg, ctx, "gnmi_CONFIG_DB", true)
		return nil, err
	})
	if status.Code(err) != codes.PermissionDenied || calls != 2 {
		t.Fatalf("completed read escalated to write: %v calls=%d", err, calls)
	}
	// Mixed mode must still use the legacy credential orchestration and its status mapping.
	cfg.UserAuth["password"] = true
	if i, _ := configuredRPCAuthorizationInterceptor(cfg); i != nil {
		t.Fatal("central authorization enabled for mixed mode")
	}
	patch.ApplyFunc(BasicAuthenAndAuthor, func(ctx context.Context) (context.Context, error) {
		rc, ctx := common_utils.GetContext(ctx)
		rc.Auth.User, rc.Auth.Roles = "legacy", []string{"gnoi_readonly"}
		return ctx, nil
	})
	if _, err := authenticate(cfg, context.Background(), "gnoi", true); status.Code(err) != codes.PermissionDenied || calls != 2 {
		t.Fatalf("mixed mode changed fallback/denial: err=%v cert calls=%d", err, calls)
	}
}

func TestCentralSubscribeReauthenticationRefreshesSource(t *testing.T) {
	for _, change := range []struct {
		name string
		code codes.Code
	}{
		{"role revoked", codes.PermissionDenied},
		{"mapping removed", codes.Unauthenticated},
		{"CRL rejects certificate", codes.Unauthenticated},
	} {
		t.Run(change.name, func(t *testing.T) {
			var revoked atomic.Bool
			var credentialCalls atomic.Int32
			refreshed := make(chan struct{}, 1)
			patch := gomonkey.ApplyFunc(ClientCertAuthenAndAuthor, func(ctx context.Context, _ string, crl bool) (context.Context, error) {
				n := credentialCalls.Add(1)
				rc, ctx := common_utils.GetContext(ctx)
				rc.Auth.User = "subscriber"
				rc.Auth.Roles = []string{"gnmi_counters_db_readonly"}
				if !crl {
					t.Fatal("CRL setting lost")
				}
				if revoked.Load() {
					if change.code == codes.Unauthenticated {
						return ctx, status.Error(change.code, change.name)
					}
					rc.Auth.Roles = []string{"gnmi_noaccess", "gnmi_counters_db_readonly"}
				}
				if n > 2 {
					select {
					case refreshed <- struct{}{}:
					default:
					}
				}
				return ctx, nil
			})
			defer patch.Reset()
			cfg := &Config{UserAuth: AuthTypes{"cert": true}, ConfigTableName: gnmiClientCertTable, EnableCrl: true}
			i, _ := configuredRPCAuthorizationInterceptor(cfg)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			initial, ctx := common_utils.GetContext(ctx)
			bundle := "bundle"
			initial.BundleVersion = &bundle
			stream := &testAuthorizationServerStream{ctx: ctx, messages: []proto.Message{testSubscribeRequest("COUNTERS_DB")}}
			err := i.StreamInterceptor()(nil, stream, &grpc.StreamServerInfo{FullMethod: "/gnmi.gNMI/Subscribe"}, func(_ interface{}, stream grpc.ServerStream) error {
				ctx := stream.Context()
				if _, err := authenticate(cfg, ctx, "gnmi_COUNTERS_DB", false); err != nil {
					return err
				}
				if credentialCalls.Load() != 2 {
					t.Fatal("initial handler repeated credentials")
				}
				rc, _ := common_utils.GetContext(ctx)
				originalRoles := append([]string(nil), rc.Auth.Roles...)
				stop, done := make(chan struct{}), make(chan struct{})
				authErr := make(chan error, 1)
				var closed atomic.Int32
				go func() {
					defer close(done)
					monitorAuthentication(ctx, time.Millisecond, func() error {
						return reauthenticate(cfg, ctx, "gnmi_COUNTERS_DB")
					}, func() { closed.Add(1) }, stop, authErr)
				}()
				defer func() {
					close(stop)
					select {
					case <-done:
					case <-time.After(time.Second):
						t.Error("monitor cleanup timed out")
					}
				}()
				select {
				case <-refreshed:
				case <-time.After(time.Second):
					t.Fatal("periodic authentication did not refresh credential source")
				}
				// Change only the external source; the initial request identity stays intact.
				revoked.Store(true)
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("revoked source did not close subscription")
				}
				select {
				case err := <-authErr:
					if status.Code(err) != change.code {
						t.Fatalf("terminal status=%v", err)
					}
				default:
					t.Fatal("missing terminal authentication error")
				}
				if closed.Load() != 1 || credentialCalls.Load() < 4 {
					t.Fatalf("closes=%d calls=%d", closed.Load(), credentialCalls.Load())
				}
				if rc == initial || rc.ID != initial.ID || rc.BundleVersion != &bundle || !reflect.DeepEqual(rc.Auth.Roles, originalRoles) {
					t.Fatalf("periodic check mutated stream identity: %+v", rc)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCentralAuthorizationRejectsUnknownMethodBeforeCredentials(t *testing.T) {
	cfg := &Config{UserAuth: AuthTypes{"cert": true}, ConfigTableName: gnmiClientCertTable}
	patch := gomonkey.ApplyFunc(ClientCertAuthenAndAuthor, func(ctx context.Context, _ string, _ bool) (context.Context, error) {
		t.Fatal("unclassified method attempted credential backend")
		return ctx, nil
	})
	defer patch.Reset()
	i, reason := configuredRPCAuthorizationInterceptor(cfg)
	if i == nil {
		t.Fatalf("central mode unavailable: %q", reason)
	}
	// These were unclassified before declarations moved beside registration.
	// Explicit declarations must still reject before touching credentials.
	for _, method := range []string{
		"/unclassified.Service/Write",
		"/gnsi.certz.v1.Certz/AddProfile", "/gnsi.certz.v1.Certz/DeleteProfile",
		"/gnsi.certz.v1.Certz/GetProfileList", "/gnsi.certz.v1.Certz/CanGenerateCSR",
		"/gnsi.certz.v1.Certz/GetIntegrityManifest", "/gnsi.certz.v1.Certz/Rotate",
		"/gnsi.authz.v1.Authz/Probe", "/gnsi.authz.v1.Authz/Get", "/gnsi.authz.v1.Authz/Rotate",
		"/gnsi.pathz.v1.Pathz/Probe", "/gnsi.pathz.v1.Pathz/Get", "/gnsi.pathz.v1.Pathz/Rotate",
		"/gnsi.credentialz.v1.Credentialz/CanGenerateKey", "/gnsi.credentialz.v1.Credentialz/GetPublicKeys",
		"/gnsi.credentialz.v1.Credentialz/RotateAccountCredentials", "/gnsi.credentialz.v1.Credentialz/RotateHostParameters",
		"/gnoi.sonic_jwt.SonicJwtService/Authenticate",
		"/gnoi.healthz.Healthz/List", "/gnoi.healthz.Healthz/Check",
		"/gnoi.containerz.Containerz/Remove", "/gnoi.containerz.Containerz/Start", "/gnoi.containerz.Containerz/Stop",
		"/gnoi.containerz.Containerz/List", "/gnoi.containerz.Containerz/Log", "/gnoi.debug.Debug/Debug",
	} {
		t.Run(method, func(t *testing.T) {
			_, err := i.UnaryInterceptor()(context.Background(), nil,
				&grpc.UnaryServerInfo{FullMethod: method},
				func(context.Context, interface{}) (interface{}, error) {
					t.Fatal("unclassified method entered handler")
					return nil, nil
				})
			if status.Code(err) != codes.PermissionDenied || status.Convert(err).Message() != "RPC access denied" {
				t.Fatalf("denied method = %v", err)
			}
		})
	}
}

func TestReauthenticateLegacyAndUDS(t *testing.T) {
	calls := 0
	patch := gomonkey.ApplyFunc(BasicAuthenAndAuthor, func(ctx context.Context) (context.Context, error) {
		calls++
		rc, ctx := common_utils.GetContext(ctx)
		rc.Auth.User, rc.Auth.Roles = "reader", []string{"gnmi_readonly"}
		return ctx, nil
	})
	defer patch.Reset()
	cfg := &Config{UserAuth: AuthTypes{"password": true, "cert": true}}
	if err := reauthenticate(cfg, context.Background(), "gnmi"); err != nil || calls != 1 {
		t.Fatalf("legacy reauthentication changed: %v calls=%d", err, calls)
	}
	uds, cancel := createUDSCtx()
	defer cancel()
	if err := reauthenticate(cfg, uds, "gnmi"); err != nil || calls != 1 {
		t.Fatalf("UDS called credentials: %v calls=%d", err, calls)
	}
}
