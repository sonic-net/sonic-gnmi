package gnmi

import (
	"context"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/sonic-net/sonic-gnmi/common_utils"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestCentralAuthorizationWithInjectedAuthentication(t *testing.T) {
	cfg := &Config{UserAuth: AuthTypes{"cert": true}, ConfigTableName: gnmiClientCertTable}
	roles := []string{"gnoi_readonly"}
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
	if _, err := authenticate(cfg, context.Background(), "gnoi", true); status.Code(err) != codes.PermissionDenied || calls != 1 {
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
				if n > 1 {
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
				if credentialCalls.Load() != 1 {
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
				if closed.Load() != 1 || credentialCalls.Load() < 3 {
					t.Fatalf("closes=%d calls=%d", closed.Load(), credentialCalls.Load())
				}
				if rc != initial || rc.ID != initial.ID || rc.BundleVersion != &bundle || !reflect.DeepEqual(rc.Auth.Roles, originalRoles) {
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
	_, err := i.UnaryInterceptor()(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: "/unclassified.Service/Write"},
		func(context.Context, interface{}) (interface{}, error) {
			t.Fatal("unclassified method entered handler")
			return nil, nil
		})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("unknown method = %v", err)
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
