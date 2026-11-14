package gnmi

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	pb "github.com/openconfig/gnmi/proto/gnmi"
	filepb "github.com/openconfig/gnoi/file"
	healthpb "github.com/openconfig/gnoi/healthz"
	systempb "github.com/openconfig/gnoi/system"
	"github.com/sonic-net/sonic-gnmi/common_utils"
	"github.com/sonic-net/sonic-gnmi/pkg/bypass"
	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
	"github.com/sonic-net/sonic-gnmi/swsscommon"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestRPCDefaultAuthorization(t *testing.T) {
	var role atomic.Value
	role.Store("gnoi_readonly")
	var credentials, effects atomic.Int32
	patch := gomonkey.ApplyFunc(ClientCertAuthenAndAuthor, func(ctx context.Context, _ string, _ bool) (context.Context, error) {
		credentials.Add(1)
		rc, ctx := common_utils.GetContext(ctx)
		rc.Auth.User, rc.Auth.Roles = "caller", []string{role.Load().(string)}
		return ctx, nil
	})
	defer patch.Reset()
	for _, uds := range []bool{false, true} {
		t.Run(map[bool]string{false: "TCP", true: "UDS"}[uds], func(t *testing.T) {
			cfg := &Config{BindAddress: "127.0.0.1", EnableNativeWrite: true, UserAuth: AuthTypes{"cert": true}, ConfigTableName: "GNMI_CLIENT_CERT"}
			if uds {
				cfg.UnixSocket = filepath.Join(t.TempDir(), "gnmi.sock")
			}
			srv, err := NewServer(cfg, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			transport := srv.s
			address := srv.Address()
			if uds {
				transport, address = srv.udsServer, "unix://"+cfg.UnixSocket
			}
			// A registered unknown service must not inherit the gnoi role target.
			transport.RegisterService(&grpc.ServiceDesc{ServiceName: "gnoi.future.Service", HandlerType: (*interface{})(nil), Methods: []grpc.MethodDesc{{MethodName: "Get", Handler: func(s interface{}, ctx context.Context, decode func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
				r := new(systempb.TimeRequest)
				if err := decode(r); err != nil {
					return nil, err
				}
				h := func(context.Context, interface{}) (interface{}, error) {
					effects.Add(1)
					return &systempb.TimeResponse{}, nil
				}
				if interceptor == nil {
					return h(ctx, r)
				}
				return interceptor(ctx, r, &grpc.UnaryServerInfo{Server: s, FullMethod: "/gnoi.future.Service/Get"}, h)
			}}}}, &struct{}{})
			// SonicService is disabled by the production translib flag here. Add a
			// future method in that known scope to exercise the default over gRPC.
			transport.RegisterService(&grpc.ServiceDesc{ServiceName: "gnoi.sonic.SonicService", HandlerType: (*interface{})(nil), Streams: []grpc.StreamDesc{{StreamName: "ListFuture", ServerStreams: true, Handler: func(_ interface{}, ss grpc.ServerStream) error {
				if err := ss.RecvMsg(new(systempb.TimeRequest)); err != nil {
					return err
				}
				effects.Add(1)
				return ss.SendMsg(&systempb.TimeResponse{})
			}}}}, &struct{}{})
			done := make(chan error, 1)
			go func() { done <- srv.Serve() }()
			t.Cleanup(func() {
				srv.ForceStop()
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Error("server did not stop")
				}
			})
			conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			role.Store("gnoi_readonly")
			if _, err := systempb.NewSystemClient(conn).Time(ctx, &systempb.TimeRequest{}); err != nil {
				t.Fatal(err)
			}
			before := credentials.Load()
			for _, method := range []string{"/gnoi.future.Service/Get", "/gnoi.healthz.Healthz/Check"} {
				role.Store("gnoi_readwrite")
				err := conn.Invoke(ctx, method, &healthpb.CheckRequest{}, &healthpb.CheckResponse{})
				want := codes.PermissionDenied
				if uds {
					if method == "/gnoi.future.Service/Get" {
						want = codes.OK
					} else {
						want = codes.Unimplemented
					}
				}
				if status.Code(err) != want {
					t.Errorf("%s: %v, want %v", method, err, want)
				}
			}
			if credentials.Load() != before {
				t.Error("denied/UDS RPC touched credential backend")
			}
			wantEffects := int32(0)
			if uds {
				wantEffects = 1
			}
			if effects.Load() != wantEffects {
				t.Errorf("unknown service effects=%d want %d", effects.Load(), wantEffects)
			}
			for _, r := range []string{"gnoi_readonly", "gnoi_readwrite", "gnmi_readwrite"} {
				role.Store(r)
				before := effects.Load()
				stream, err := conn.NewStream(ctx, &grpc.StreamDesc{ServerStreams: true}, "/gnoi.sonic.SonicService/ListFuture")
				if err != nil {
					t.Fatal(err)
				}
				if err := stream.SendMsg(&systempb.TimeRequest{}); err != nil && err != io.EOF {
					t.Fatal(err)
				}
				err = stream.RecvMsg(&systempb.TimeResponse{})
				want, n := codes.PermissionDenied, int32(0)
				if uds || r == "gnoi_readwrite" {
					want, n = codes.OK, 1
				}
				if status.Code(err) != want || effects.Load()-before != n {
					t.Errorf("new method role=%s status=%v effects=%d", r, err, effects.Load()-before)
				}
			}
			effects.Store(0)
		})
	}
}

func TestRPCNativeSetAuthorization(t *testing.T) {
	cfg := &Config{UserAuth: AuthTypes{"cert": true}, ConfigTableName: "GNMI_CLIENT_CERT"}
	patch := gomonkey.ApplyFunc(ClientCertAuthenAndAuthor, func(ctx context.Context, _ string, _ bool) (context.Context, error) {
		rc, ctx := common_utils.GetContext(ctx)
		rc.Auth.User = "writer"
		rc.Auth.Roles = []string{"gnmi_config_db_readwrite"}
		return ctx, nil
	})
	defer patch.Reset()
	path := func(db, instance, table string) *pb.Path {
		return &pb.Path{Elem: []*pb.PathElem{{Name: db}, {Name: instance}, {Name: table}}}
	}
	for _, tc := range []struct {
		name   string
		prefix *pb.Path
		paths  []*pb.Path
		code   codes.Code
	}{
		{"native", &pb.Path{Origin: "sonic-db"}, []*pb.Path{path("CONFIG_DB", "localhost", "VNET")}, codes.OK},
		{"wrong target", &pb.Path{Origin: "sonic-db"}, []*pb.Path{path("APPL_DB", "localhost", "TABLE")}, codes.PermissionDenied},
		{"conflict", &pb.Path{Origin: "sonic-db"}, []*pb.Path{path("CONFIG_DB", "localhost", "VNET"), path("APPL_DB", "localhost", "TABLE")}, codes.Unimplemented},
		{"namespace conflict", &pb.Path{Origin: "sonic-db"}, []*pb.Path{path("CONFIG_DB", "asic0", "VNET"), path("CONFIG_DB", "asic1", "VNET")}, codes.Unimplemented},
		{"bypass instance table collision", &pb.Path{Origin: "sonic-db", Target: "CONFIG_DB"}, []*pb.Path{path("CONFIG_DB", "VNET", "VNET"), path("CONFIG_DB", "ACL_RULE", "ACL_RULE")}, codes.Unimplemented},
		{"unknown database", &pb.Path{Origin: "sonic-db"}, []*pb.Path{path("UNRECOGNIZED", "localhost", "TABLE")}, codes.PermissionDenied},
		{"prefix conflict", &pb.Path{Origin: "sonic-db", Target: "CONFIG_DB"}, []*pb.Path{path("APPL_DB", "localhost", "TABLE")}, codes.InvalidArgument},
		{"bypass", &pb.Path{Origin: "sonic-db", Target: "CONFIG_DB"}, []*pb.Path{{Elem: []*pb.PathElem{{Name: "VNET"}, {Name: "blue"}}}}, codes.OK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(bypass.MetadataKeyBypassValidation, "true"))
			req := &pb.SetRequest{Prefix: tc.prefix}
			for _, p := range tc.paths {
				req.Update = append(req.Update, &pb.Update{Path: p})
			}
			effects := 0
			_, err := rpcUnaryAuth(cfg)(ctx, req, &grpc.UnaryServerInfo{FullMethod: "/gnmi.gNMI/Set"}, func(context.Context, interface{}) (interface{}, error) { effects++; return nil, nil })
			want := 0
			if tc.code == codes.OK {
				want = 1
			}
			if status.Code(err) != tc.code || effects != want {
				t.Fatalf("status=%v effects=%d want %v/%d", err, effects, tc.code, want)
			}
		})
	}
	// An authorized table-relative request must not reach native setup if the
	// real bypass router declines it (for example an ineligible device SKU).
	p := gomonkey.ApplyFunc(bypass.TrySet, func(context.Context, *pb.Path, []*pb.Path, []*pb.Update) (*pb.SetResponse, bool, error) {
		return nil, false, nil
	})
	defer p.Reset()
	srv := &Server{config: &Config{EnableNativeWrite: true, UserAuth: cfg.UserAuth, ConfigTableName: cfg.ConfigTableName}, ReqFromMaster: ReqFromMasterDisabledMA, SaveStartupConfig: saveOnSetDisabled}
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(bypass.MetadataKeyBypassValidation, "true"))
	req := &pb.SetRequest{Prefix: &pb.Path{Origin: "sonic-db", Target: "CONFIG_DB"}, Update: []*pb.Update{{Path: &pb.Path{Elem: []*pb.PathElem{{Name: "VNET"}, {Name: "blue"}}}}}}
	_, err := rpcUnaryAuth(cfg)(ctx, req, &grpc.UnaryServerInfo{FullMethod: "/gnmi.gNMI/Set"}, func(ctx context.Context, _ interface{}) (interface{}, error) { return srv.Set(ctx, req) })
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("bypass fallback: %v", err)
	}
}

func TestRPCConfiguredNativeDatabase(t *testing.T) {
	cfg := &Config{UserAuth: AuthTypes{"cert": true}, ConfigTableName: "GNMI_CLIENT_CERT"}
	patches := gomonkey.NewPatches()
	defer patches.Reset()
	// The native backend accepts configured database names, not only proto.Target.
	patches.ApplyFunc(sdcfg.GetDbListByDBKey, func(swsscommon.SonicDBKey) ([]string, error) { return []string{"DPU_APPL_DB", "PLATFORM_DB"}, nil })
	for _, db := range []string{"DPU_APPL_DB", "PLATFORM_DB", "UNRECOGNIZED"} {
		t.Run(db, func(t *testing.T) {
			p := gomonkey.ApplyFunc(ClientCertAuthenAndAuthor, func(ctx context.Context, _ string, _ bool) (context.Context, error) {
				rc, ctx := common_utils.GetContext(ctx)
				rc.Auth.User = "writer"
				rc.Auth.Roles = []string{"gnmi_" + strings.ToLower(db) + "_readwrite"}
				return ctx, nil
			})
			defer p.Reset()
			req := &pb.SetRequest{Prefix: &pb.Path{Origin: "sonic-db"}, Update: []*pb.Update{{Path: &pb.Path{Elem: []*pb.PathElem{{Name: db}, {Name: "dpu0"}, {Name: "DASH_QOS"}}}}}}
			effects := 0
			_, err := rpcUnaryAuth(cfg)(context.Background(), req, &grpc.UnaryServerInfo{FullMethod: "/gnmi.gNMI/Set"}, func(context.Context, interface{}) (interface{}, error) { effects++; return nil, nil })
			want, n := codes.OK, 1
			if db == "UNRECOGNIZED" {
				want, n = codes.PermissionDenied, 0
			}
			if status.Code(err) != want || effects != n {
				t.Fatalf("configuredDB=%s status=%v effects=%d want %v/%d", db, err, effects, want, n)
			}
		})
	}
	patches.Reset()
	reads := 0
	patches.ApplyFunc(sdcfg.GetDbListByDBKey, func(swsscommon.SonicDBKey) ([]string, error) { reads++; return nil, errors.New("config unavailable") })
	req := &pb.SetRequest{Prefix: &pb.Path{Origin: "sonic-db"}, Update: []*pb.Update{{Path: &pb.Path{Elem: []*pb.PathElem{{Name: "DPU_APPL_DB"}, {Name: "dpu0"}, {Name: "DASH_QOS"}}}}}}
	for _, authenticated := range []bool{true, false} {
		reads = 0
		p := gomonkey.ApplyFunc(ClientCertAuthenAndAuthor, func(ctx context.Context, _ string, _ bool) (context.Context, error) {
			if !authenticated {
				return ctx, status.Error(codes.Unauthenticated, "not mapped")
			}
			rc, ctx := common_utils.GetContext(ctx)
			rc.Auth.Roles = []string{"gnmi_dpu_appl_db_readwrite"}
			return ctx, nil
		})
		_, err := rpcUnaryAuth(cfg)(context.Background(), req, &grpc.UnaryServerInfo{FullMethod: "/gnmi.gNMI/Set"}, func(context.Context, interface{}) (interface{}, error) {
			t.Fatal("configuration failure reached handler")
			return nil, nil
		})
		p.Reset()
		want := codes.Unavailable
		if !authenticated {
			want = codes.Unauthenticated
		}
		if status.Code(err) != want || (!authenticated && reads != 0) {
			t.Fatalf("authenticated=%t status=%v configreads=%d", authenticated, err, reads)
		}
	}
}

func TestRPCDelayedPutAuthorization(t *testing.T) {
	for _, revoked := range []codes.Code{codes.OK, codes.Unauthenticated, codes.PermissionDenied} {
		t.Run(revoked.String(), func(t *testing.T) {
			var received atomic.Bool
			var calls atomic.Int32
			entered := make(chan struct{}, 1)
			patch := gomonkey.ApplyFunc(ClientCertAuthenAndAuthor, func(ctx context.Context, _ string, _ bool) (context.Context, error) {
				if received.Load() && revoked == codes.Unauthenticated {
					return ctx, status.Error(revoked, "mapping revoked")
				}
				rc, ctx := common_utils.GetContext(ctx)
				rc.Auth.User, rc.Auth.Roles = "writer", []string{"gnoi_readwrite"}
				if received.Load() && revoked == codes.PermissionDenied {
					rc.Auth.Roles = []string{"gnoi_readonly"}
				}
				if calls.Add(1) == 2 {
					entered <- struct{}{}
				}
				return ctx, nil
			})
			defer patch.Reset()
			srv, err := NewServer(&Config{BindAddress: "127.0.0.1", EnableNativeWrite: true, UserAuth: AuthTypes{"cert": true}, ConfigTableName: "GNMI_CLIENT_CERT"}, nil, nil)
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
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			stream, err := filepb.NewFileClient(conn).Put(ctx)
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			received.Store(true)
			if err = stream.Send(&filepb.PutRequest{}); err != nil && err != io.EOF {
				t.Fatal(err)
			}
			_, err = stream.CloseAndRecv()
			want := revoked
			if want == codes.OK {
				want = codes.InvalidArgument
			}
			if status.Code(err) != want {
				t.Fatalf("wire=%v want %v", err, want)
			}
		})
	}
}

// Wrong-target reads must be denied before a database client is constructed.
func TestRPCScopedGetBeforeEffects(t *testing.T) {
	var role atomic.Value
	role.Store("gnmi_state_db_readonly")
	patch := gomonkey.ApplyFunc(ClientCertAuthenAndAuthor, func(ctx context.Context, _ string, _ bool) (context.Context, error) {
		rc, ctx := common_utils.GetContext(ctx)
		rc.Auth.User = "reader"
		rc.Auth.Roles = []string{role.Load().(string)}
		return ctx, nil
	})
	defer patch.Reset()
	srv, err := NewServer(&Config{BindAddress: "127.0.0.1", Threshold: 100, UserAuth: AuthTypes{"cert": true}, ConfigTableName: "GNMI_CLIENT_CERT"}, nil, nil)
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
	ctx, cancel := context.WithTimeout(context.Background(), 85*time.Second)
	defer cancel()
	_, err = pb.NewGNMIClient(conn).Get(ctx, &pb.GetRequest{Prefix: &pb.Path{Target: "CONFIG_DB"}, Path: []*pb.Path{{Elem: []*pb.PathElem{{Name: "NONEXISTENT"}}}}})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("wrong-target Get=%v", err)
	}
	stream, err := pb.NewGNMIClient(conn).Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&pb.SubscribeRequest{Request: &pb.SubscribeRequest_Subscribe{Subscribe: &pb.SubscriptionList{Prefix: &pb.Path{Target: "CONFIG_DB"}, Mode: pb.SubscriptionList_ONCE, Subscription: []*pb.Subscription{{Path: &pb.Path{Elem: []*pb.PathElem{{Name: "NONEXISTENT"}}}}}}}}); err != nil && err != io.EOF {
		t.Fatal(err)
	}
	if _, err = stream.Recv(); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("wrong-target Subscribe=%v", err)
	}
	// The same production server must also accept scoped reads, replay the
	// initial POLL once and terminate on the real periodic credential refresh.
	ns, _ := sdcfg.GetDbDefaultNamespace()
	prepareDb(t, ns)
	role.Store("gnmi_counters_db_readonly")
	path := &pb.Path{Elem: []*pb.PathElem{{Name: "COUNTERS"}, {Name: "Ethernet68"}}}
	client := pb.NewGNMIClient(conn)
	get, err := client.Get(ctx, &pb.GetRequest{Prefix: &pb.Path{Target: "COUNTERS_DB"}, Path: []*pb.Path{path}, Encoding: pb.Encoding_JSON_IETF})
	if err != nil || len(get.GetNotification()) == 0 {
		t.Fatalf("allowed scoped Get: %v %v", get, err)
	}
	stream, err = client.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&pb.SubscribeRequest{Request: &pb.SubscribeRequest_Subscribe{Subscribe: &pb.SubscriptionList{Prefix: &pb.Path{Target: "COUNTERS_DB"}, Mode: pb.SubscriptionList_POLL, Subscription: []*pb.Subscription{{Path: path}}, Encoding: pb.Encoding_JSON_IETF}}}); err != nil {
		t.Fatal(err)
	}
	updates := 0
	for {
		r, err := stream.Recv()
		if err != nil {
			t.Fatal(err)
		}
		updates += len(r.GetUpdate().GetUpdate())
		if r.GetSyncResponse() {
			break
		}
	}
	if updates == 0 {
		t.Fatal("POLL returned no updates")
	}
	role.Store("gnmi_noaccess")
	if _, err = stream.Recv(); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("periodic revocation=%v", err)
	}
	t.Log("TCP scoped Get/POLL returned data; periodic revocation returned PermissionDenied")
}
