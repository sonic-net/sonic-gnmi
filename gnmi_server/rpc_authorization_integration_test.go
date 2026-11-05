package gnmi

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	healthzpb "github.com/openconfig/gnoi/healthz"
	systempb "github.com/openconfig/gnoi/system"
	"github.com/sonic-net/sonic-gnmi/common_utils"
	"github.com/sonic-net/sonic-gnmi/pkg/bypass"
	sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
	dbconfig "github.com/sonic-net/sonic-gnmi/sonic_db_config"
	"github.com/sonic-net/sonic-gnmi/swsscommon"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestNewServerRegistrationAuthorization(t *testing.T) {
	var credentials, inner atomic.Int32
	patch := gomonkey.ApplyFunc(ClientCertAuthenAndAuthor, func(ctx context.Context, _ string, _ bool) (context.Context, error) {
		credentials.Add(1)
		rc, ctx := common_utils.GetContext(ctx)
		rc.Auth.User, rc.Auth.Roles = "reader", []string{"gnoi_readonly", "gnmi_readonly"}
		return ctx, nil
	})
	defer patch.Reset()
	for _, uds := range []bool{false, true} {
		t.Run(map[bool]string{false: "TCP", true: "UDS"}[uds], func(t *testing.T) {
			credentials.Store(0)
			inner.Store(0)
			cfg := &Config{BindAddress: "127.0.0.1", EnableNativeWrite: true, UserAuth: AuthTypes{"cert": true}, ConfigTableName: gnmiClientCertTable}
			if uds {
				cfg.UnixSocket = filepath.Join(t.TempDir(), "gnmi.sock")
			}
			srv, err := NewServer(cfg, nil, []grpc.ServerOption{grpc.ChainUnaryInterceptor(
				func(ctx context.Context, req interface{}, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
					inner.Add(1)
					return handler(ctx, req)
				}), grpc.ChainStreamInterceptor(
				func(srv interface{}, stream grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
					inner.Add(1)
					return handler(srv, stream)
				})})
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
			_, err = healthzpb.NewHealthzClient(conn).Check(ctx, &healthzpb.CheckRequest{})
			if uds {
				if status.Code(err) != codes.Unimplemented || credentials.Load() != 0 || inner.Load() != 2 {
					t.Fatalf("UDS bypass: %v credentials=%d inner=%d", err, credentials.Load(), inner.Load())
				}
				return
			}
			if status.Code(err) != codes.PermissionDenied || credentials.Load() != 1 || inner.Load() != 1 {
				t.Fatalf("explicit deny before credentials/inner: %v credentials=%d inner=%d", err, credentials.Load(), inner.Load())
			}
			_, err = systempb.NewSystemClient(conn).Reboot(ctx, &systempb.RebootRequest{})
			if status.Code(err) != codes.PermissionDenied || inner.Load() != 1 {
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
		})
	}
}

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

func TestAuthorizedRequestRejectsChangedOrMissingRoute(t *testing.T) {
	backendCalls := 0
	patch := gomonkey.ApplyFunc(sdc.NewDbClientForGet, func([]*gnmipb.Path, *gnmipb.Path) (sdc.Client, error) {
		backendCalls++
		return nil, status.Error(codes.Internal, "unexpected legacy backend setup")
	})
	defer patch.Reset()
	patch.ApplyFunc(sdc.NewResolvedDbClient, func([]*gnmipb.Path, *gnmipb.Path, bool, sdc.DatabaseRoute) (sdc.Client, error) {
		backendCalls++
		return nil, status.Error(codes.Internal, "unexpected resolved backend setup")
	})
	cfg := &Config{UserAuth: AuthTypes{"cert": true}, ConfigTableName: gnmiClientCertTable}
	srv := &Server{config: cfg}
	request := testGetRequest("CONFIG_DB")
	// A directly called central handler must not silently use legacy routing.
	if _, err := srv.Get(context.Background(), request); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("missing route: %v", err)
	}
	i := testRPCAuthorizationInterceptor("gnmi_config_db_readonly")
	_, err := i.UnaryInterceptor()(context.Background(), request,
		&grpc.UnaryServerInfo{FullMethod: "/gnmi.gNMI/Get"},
		func(ctx context.Context, req interface{}) (interface{}, error) {
			// Simulate an inner interceptor changing the request after authorization.
			request.Prefix.Target = "STATE_DB"
			_, err := srv.Get(ctx, request)
			return nil, err
		})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("changed target: %v", err)
	}
	request = testGetRequest("CONFIG_DB")
	_, err = i.UnaryInterceptor()(context.Background(), request,
		&grpc.UnaryServerInfo{FullMethod: "/gnmi.gNMI/Get"},
		func(ctx context.Context, _ interface{}) (interface{}, error) {
			return srv.Set(ctx, testSetRequest("CONFIG_DB"))
		})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("wrong method: %v", err)
	}
	if backendCalls != 0 {
		t.Fatalf("rejected request created %d backend clients", backendCalls)
	}
}

func TestAuthorizedRequestNativeGetAndBypassWrite(t *testing.T) {
	ctx := context.Background()
	state := getRedisClientN(t, 6, "")
	t.Cleanup(func() { state.Close() })
	config := getConfigDbClient(t, "")
	t.Cleanup(func() { config.Close() })
	t.Setenv("REDIS_ADDR", "127.0.0.1:6379")
	const key = "AUTHORIZED_ROUTE_TEST|entry"
	if err := state.HSet(ctx, key, "value", "bound-state").Err(); err != nil {
		t.Fatal(err)
	}
	oldSKU, skuErr := config.HGet(ctx, "DEVICE_METADATA|localhost", "hwsku").Result()
	if err := config.HSet(ctx, "DEVICE_METADATA|localhost", "hwsku", "Cisco-8102-test").Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		state.Del(ctx, key)
		config.Del(ctx, "VNET|authorized-route-test")
		if skuErr == nil {
			config.HSet(ctx, "DEVICE_METADATA|localhost", "hwsku", oldSKU)
		} else {
			config.HDel(ctx, "DEVICE_METADATA|localhost", "hwsku")
		}
	})
	patch := gomonkey.ApplyFunc(ClientCertAuthenAndAuthor, func(ctx context.Context, _ string, _ bool) (context.Context, error) {
		rc, ctx := common_utils.GetContext(ctx)
		rc.Auth.User, rc.Auth.Roles = "writer", []string{"gnmi_state_db_readonly", "gnmi_config_db_readwrite"}
		return ctx, nil
	})
	defer patch.Reset()
	srv, err := NewServer(&Config{BindAddress: "127.0.0.1", EnableNativeWrite: true, UserAuth: AuthTypes{"cert": true}, ConfigTableName: gnmiClientCertTable}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv.SaveStartupConfig = saveOnSetDisabled
	done := make(chan error, 1)
	go func() { done <- srv.Serve() }()
	defer func() {
		srv.ForceStop()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(time.Second):
			t.Error("server did not stop")
		}
	}()
	conn, err := grpc.NewClient(srv.Address(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	rpcCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	client := gnmipb.NewGNMIClient(conn)
	response, err := client.Get(rpcCtx, &gnmipb.GetRequest{
		Prefix:   &gnmipb.Path{Origin: "sonic-db"},
		Path:     []*gnmipb.Path{{Elem: []*gnmipb.PathElem{{Name: "STATE_DB"}, {Name: "localhost"}, {Name: "AUTHORIZED_ROUTE_TEST"}, {Name: "entry"}}}},
		Encoding: gnmipb.Encoding_JSON_IETF,
	})
	if err != nil || len(response.GetNotification()) == 0 {
		t.Fatalf("native Get response=%v err=%v", response, err)
	}
	if !strings.Contains(response.String(), "bound-state") {
		t.Fatalf("native Get did not return selected database value: %v", response)
	}
	writeCtx := metadata.NewOutgoingContext(rpcCtx, metadata.Pairs("x-sonic-ss-bypass-validation", "true"))
	_, err = client.Set(writeCtx, &gnmipb.SetRequest{
		Prefix: &gnmipb.Path{Origin: "sonic-db", Target: "CONFIG_DB"},
		Update: []*gnmipb.Update{{Path: &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "VNET"}, {Name: "authorized-route-test"}}}, Val: &gnmipb.TypedValue{Value: &gnmipb.TypedValue_JsonIetfVal{JsonIetfVal: []byte(`{"vni":"1000"}`)}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if value, err := config.HGet(ctx, "VNET|authorized-route-test", "vni").Result(); err != nil || value != "1000" {
		t.Fatalf("bypass write value=%q err=%v", value, err)
	}
}

func TestAuthorizedRequestNondefaultBypassUsesNativeBinding(t *testing.T) {
	patch := gomonkey.NewPatches()
	defer patch.Reset()
	patch.ApplyFunc(dbconfig.GetDbAllInstances, func() ([]swsscommon.SonicDBKey, error) {
		key := swsscommon.NewSonicDBKey()
		key.SetNetns("VNET")
		return []swsscommon.SonicDBKey{key}, nil
	})
	patch.ApplyFunc(sdc.IsTargetDbByDBKey, func(database string, key swsscommon.SonicDBKey) bool {
		return database == "CONFIG_DB" && key.GetNetns() == "VNET" && key.GetContainerName() == ""
	})
	bypassCalls, nativeCalls := 0, 0
	patch.ApplyFunc(bypass.TrySet, func(context.Context, *gnmipb.Path, []*gnmipb.Path, []*gnmipb.Update) (*gnmipb.SetResponse, bool, error) {
		bypassCalls++
		return &gnmipb.SetResponse{}, true, nil
	})
	patch.ApplyMethod(reflect.TypeOf(&sdc.MixedDbClient{}), "Set", func(_ *sdc.MixedDbClient, _ []*gnmipb.Path, _ []*gnmipb.Update, updates []*gnmipb.Update) error {
		nativeCalls++
		if updates[0].GetPath().GetElem()[1].GetName() != "VNET" {
			t.Fatal("native operation lost instance")
		}
		return nil
	})
	srv := &Server{config: &Config{EnableNativeWrite: true, UserAuth: AuthTypes{"cert": true}, ConfigTableName: gnmiClientCertTable}, ReqFromMaster: ReqFromMasterDisabledMA, SaveStartupConfig: saveOnSetDisabled}
	req := &gnmipb.SetRequest{Prefix: &gnmipb.Path{Origin: "sonic-db", Target: "CONFIG_DB"}, Update: []*gnmipb.Update{{Path: &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "CONFIG_DB"}, {Name: "VNET"}, {Name: "VNET"}, {Name: "blue"}}}, Val: &gnmipb.TypedValue{Value: &gnmipb.TypedValue_JsonIetfVal{JsonIetfVal: []byte(`{"vni":"1000"}`)}}}}}
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(bypass.MetadataKeyBypassValidation, "true"))
	_, err := testRPCAuthorizationInterceptor("gnmi_config_db_readwrite").UnaryInterceptor()(ctx, req,
		&grpc.UnaryServerInfo{FullMethod: "/gnmi.gNMI/Set"}, func(ctx context.Context, req interface{}) (interface{}, error) {
			return srv.Set(ctx, req.(*gnmipb.SetRequest))
		})
	if err != nil || bypassCalls != 0 || nativeCalls != 1 {
		t.Fatalf("nondefault execution err=%v bypass=%d native=%d", err, bypassCalls, nativeCalls)
	}
}

func TestAuthorizedRequestEmptyDirectTargetPreservesNoLookup(t *testing.T) {
	lookups := 0
	patch := gomonkey.ApplyFunc(dbconfig.CheckDbMultiNamespace, func() (bool, error) { return true, nil })
	defer patch.Reset()
	patch.ApplyFunc(dbconfig.GetDbNamespaceFromTarget, func(string) (string, bool, error) {
		lookups++
		return "", false, nil
	})
	req := &gnmipb.GetRequest{Prefix: &gnmipb.Path{Target: "STATE_DB/missing"}, Encoding: gnmipb.Encoding_JSON_IETF}
	legacy := &Server{config: &Config{}}
	want, err := legacy.Get(context.Background(), req)
	if err != nil {
		t.Fatalf("legacy empty Get: %v", err)
	}
	central := &Server{config: &Config{UserAuth: AuthTypes{"cert": true}, ConfigTableName: gnmiClientCertTable}}
	got, err := testRPCAuthorizationInterceptor("gnmi_state_db_readonly").UnaryInterceptor()(context.Background(), req,
		&grpc.UnaryServerInfo{FullMethod: "/gnmi.gNMI/Get"}, func(ctx context.Context, req interface{}) (interface{}, error) {
			return central.Get(ctx, req.(*gnmipb.GetRequest))
		})
	if err != nil || !proto.Equal(want, got.(*gnmipb.GetResponse)) || lookups != 0 {
		t.Fatalf("empty Get got=%v want=%v err=%v lookups=%d", got, want, err, lookups)
	}
	// An explicitly empty SubscriptionList is also accepted without instance lookup.
	sub := testSubscribeRequest("STATE_DB/missing")
	sub.GetSubscribe().Subscription = []*gnmipb.Subscription{}
	target, err := rpcAuthorizationRequestTarget(context.Background(), "/gnmi.gNMI/Subscribe", sub)
	if err != nil {
		t.Fatal(err)
	}
	client, err := target.execution.newClient(context.Background(), central.config, sub.GetSubscribe().Prefix, nil, gnmipb.Encoding_JSON_IETF, 0)
	if err != nil {
		t.Fatal(err)
	}
	client.Close()
	if lookups != 0 {
		t.Fatalf("empty Subscribe performed %d lookups", lookups)
	}
}
