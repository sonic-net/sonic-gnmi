package gnmi

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	log "github.com/golang/glog"
	pb "github.com/openconfig/gnmi/proto/gnmi"
	filepb "github.com/openconfig/gnoi/file"
	healthpb "github.com/openconfig/gnoi/healthz"
	systempb "github.com/openconfig/gnoi/system"
	"github.com/sonic-net/sonic-gnmi/common_utils"
	"github.com/sonic-net/sonic-gnmi/pkg/bypass"
	"github.com/sonic-net/sonic-gnmi/pkg/interceptors"
	"github.com/sonic-net/sonic-gnmi/pkg/interceptors/dpuproxy"
	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestRPCEntry(t *testing.T) {
	var credentials, proxyCalls atomic.Int32
	var readonly atomic.Bool
	completed := make(chan string, 8)
	patches := gomonkey.NewPatches()
	defer patches.Reset()
	patches.ApplyFunc(ClientCertAuthenAndAuthor, func(ctx context.Context, _ string, _ bool) (context.Context, error) {
		credentials.Add(1)
		rc, ctx := common_utils.GetContext(ctx)
		role := "gnoi_readwrite"
		if readonly.Load() {
			role = "gnoi_readonly"
		}
		rc.Auth.User, rc.Auth.Roles = "caller", []string{role}
		return ctx, nil
	})
	patches.ApplyFunc(log.Infof, func(format string, args ...interface{}) {
		line := fmt.Sprintf(format, args...)
		if strings.HasPrefix(line, "RPC_COMPLETION ") {
			completed <- line
		}
	})
	patches.ApplyMethod(reflect.TypeOf(&dpuproxy.DPUProxy{}), "UnaryInterceptor", func(*dpuproxy.DPUProxy) grpc.UnaryServerInterceptor {
		return func(ctx context.Context, req interface{}, _ *grpc.UnaryServerInfo, next grpc.UnaryHandler) (interface{}, error) {
			proxyCalls.Add(1)
			if !isUnixPeer(ctx) && credentials.Load() != 1 {
				t.Error("proxy before initial auth")
			}
			return next(ctx, req)
		}
	})
	for _, uds := range []bool{false, true} {
		t.Run(fmt.Sprintf("UDS=%t", uds), func(t *testing.T) {
			credentials.Store(0)
			proxyCalls.Store(0)
			readonly.Store(false)
			cfg := &Config{BindAddress: "127.0.0.1", EnableNativeWrite: true, UserAuth: AuthTypes{"cert": true}, ConfigTableName: "GNMI_CLIENT_CERT"}
			if uds {
				cfg.UnixSocket = filepath.Join(t.TempDir(), "gnmi.sock")
			}
			chain, err := interceptors.NewServerChain()
			if err != nil {
				t.Fatal(err)
			}
			defer chain.Close()
			srv, err := NewServerWithChain(cfg, nil, nil, chain)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- srv.Serve() }()
			defer func() {
				srv.ForceStop()
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Error("server did not stop")
				}
			}()
			addr := srv.Address()
			if uds {
				addr = "unix://" + cfg.UnixSocket
			}
			conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			checkLog := func(code codes.Code) {
				t.Helper()
				select {
				case line := <-completed:
					if !strings.Contains(line, `"code":"`+code.String()+`"`) {
						t.Fatal(line)
					}
				case <-ctx.Done():
					t.Fatal("missing completion")
				}
			}
			resp, err := systempb.NewSystemClient(conn).Time(ctx, &systempb.TimeRequest{})
			if err != nil || resp.GetTime() == 0 {
				t.Fatalf("Time: %v %v", resp, err)
			}
			checkLog(codes.OK)
			if !uds && credentials.Load() != 1 {
				t.Fatalf("handler repeated credentials: %d", credentials.Load())
			}
			_, err = healthpb.NewHealthzClient(conn).Check(ctx, &healthpb.CheckRequest{})
			want := codes.PermissionDenied
			if uds {
				want = codes.Unimplemented
			}
			if status.Code(err) != want {
				t.Errorf("explicit deny: %v", err)
			}
			checkLog(want)
			if !uds && (credentials.Load() != 1 || proxyCalls.Load() != 1) {
				t.Errorf("denial reached credentials/proxy: %d/%d", credentials.Load(), proxyCalls.Load())
			}
			readonly.Store(true)
			_, err = systempb.NewSystemClient(conn).Time(ctx, &systempb.TimeRequest{})
			want = codes.PermissionDenied
			if uds {
				want = codes.OK
			}
			if status.Code(err) != want {
				t.Fatalf("readonly Time: %v", err)
			}
			// The second UDS Time shares its log bucket and may be suppressed.
			if !uds {
				checkLog(want)
				if proxyCalls.Load() != 1 {
					t.Fatal("readonly call entered proxy")
				}
				stream, err := conn.NewStream(ctx, &grpc.StreamDesc{ServerStreams: true}, "/gnoi.containerz.Containerz/Log")
				if err != nil {
					t.Fatal(err)
				}
				if err := stream.RecvMsg(&healthpb.CheckResponse{}); status.Code(err) != codes.PermissionDenied {
					t.Fatal(err)
				}
				checkLog(codes.PermissionDenied)
			}
		})
	}
}

func rpcTestClient(t *testing.T, roles *atomic.Value, calls *atomic.Int32) *grpc.ClientConn {
	t.Helper()
	patch := gomonkey.ApplyFunc(ClientCertAuthenAndAuthor, func(ctx context.Context, _ string, _ bool) (context.Context, error) {
		rc, ctx := common_utils.GetContext(ctx)
		rc.Auth.User = "caller"
		rc.Auth.Roles = roles.Load().([]string)
		calls.Add(1)
		if rc.Auth.Roles == nil {
			return ctx, status.Error(codes.Unauthenticated, "mapping revoked")
		}
		return ctx, nil
	})
	t.Cleanup(patch.Reset)
	srv, err := NewServer(&Config{BindAddress: "127.0.0.1", Threshold: 100, EnableNativeWrite: true, UserAuth: AuthTypes{"cert": true}, ConfigTableName: "GNMI_CLIENT_CERT"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
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
	conn, err := grpc.NewClient(srv.Address(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestRPCSubscribeRefresh(t *testing.T) {
	var roles atomic.Value
	roles.Store([]string{"gnmi_counters_db_readonly"})
	var calls atomic.Int32
	conn := rpcTestClient(t, &roles, &calls)
	ns, _ := sdcfg.GetDbDefaultNamespace()
	prepareDb(t, ns)
	ctx, cancel := context.WithTimeout(context.Background(), 85*time.Second)
	defer cancel()
	client := pb.NewGNMIClient(conn)
	path := &pb.Path{Elem: []*pb.PathElem{{Name: "COUNTERS"}, {Name: "Ethernet68"}}}
	wrong, err := client.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := wrong.Send(&pb.SubscribeRequest{Request: &pb.SubscribeRequest_Subscribe{Subscribe: &pb.SubscriptionList{Prefix: &pb.Path{Target: "CONFIG_DB"}, Mode: pb.SubscriptionList_ONCE, Subscription: []*pb.Subscription{{Path: path}}}}}); err != nil && err != io.EOF {
		t.Fatal(err)
	}
	if _, err := wrong.Recv(); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("wrong Subscribe target: %v", err)
	}
	calls.Store(0)
	get, err := client.Get(ctx, &pb.GetRequest{Prefix: &pb.Path{Target: "COUNTERS_DB"}, Path: []*pb.Path{path}, Encoding: pb.Encoding_JSON_IETF})
	if err != nil || len(get.GetNotification()) == 0 || calls.Load() != 1 {
		t.Fatalf("scoped Get: %v calls=%d", err, calls.Load())
	}
	stream, err := client.Subscribe(ctx)
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
	if updates == 0 || calls.Load() != 3 {
		t.Fatalf("initial POLL updates=%d credentials=%d", updates, calls.Load())
	}
	roles.Store([]string{"gnmi_noaccess"})
	if _, err = stream.Recv(); status.Code(err) != codes.PermissionDenied || calls.Load() < 4 {
		t.Fatalf("periodic revoke: %v credentials=%d", err, calls.Load())
	}
}

func TestRPCSubscribeDelayedIdentity(t *testing.T) {
	var roles atomic.Value
	roles.Store([]string{"gnmi_config_db_readonly"})
	var calls atomic.Int32
	conn := rpcTestClient(t, &roles, &calls)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := pb.NewGNMIClient(conn).Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for calls.Load() == 0 {
		select {
		case <-time.After(time.Millisecond):
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	roles.Store([]string(nil))
	err = stream.Send(&pb.SubscribeRequest{Request: &pb.SubscribeRequest_Subscribe{Subscribe: &pb.SubscriptionList{Prefix: &pb.Path{Target: "CONFIG_DB"}, Subscription: []*pb.Subscription{{Path: &pb.Path{Elem: []*pb.PathElem{{Name: "TABLE"}}}}}}}})
	if err != nil && err != io.EOF {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); status.Code(err) != codes.Unauthenticated || calls.Load() != 2 {
		t.Fatalf("delayed Subscribe: %v credentials=%d", err, calls.Load())
	}
}

func TestRPCDelayedFilePut(t *testing.T) {
	for _, code := range []codes.Code{codes.OK, codes.Unauthenticated, codes.PermissionDenied} {
		t.Run(code.String(), func(t *testing.T) {
			var roles atomic.Value
			roles.Store([]string{"gnoi_readwrite"})
			var calls atomic.Int32
			conn := rpcTestClient(t, &roles, &calls)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			stream, err := filepb.NewFileClient(conn).Put(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for calls.Load() == 0 {
				select {
				case <-time.After(time.Millisecond):
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			if code == codes.Unauthenticated {
				roles.Store([]string(nil))
			} else if code == codes.PermissionDenied {
				roles.Store([]string{"gnoi_readonly"})
			}
			if err := stream.Send(&filepb.PutRequest{}); err != nil && err != io.EOF {
				t.Fatal(err)
			}
			_, err = stream.CloseAndRecv()
			want := code
			if want == codes.OK {
				want = codes.InvalidArgument
			}
			if status.Code(err) != want || calls.Load() != 2 {
				t.Fatalf("delayed Put: %v credentials=%d", err, calls.Load())
			}
		})
	}
}

func TestRPCDecisionAndTargets(t *testing.T) {
	roles := []string{"gnmi_config_db_readwrite"}
	calls := 0
	patches := gomonkey.NewPatches()
	defer patches.Reset()
	patches.ApplyFunc(ClientCertAuthenAndAuthor, func(ctx context.Context, _ string, _ bool) (context.Context, error) {
		calls++
		rc, ctx := common_utils.GetContext(ctx)
		rc.Auth.User = "writer"
		rc.Auth.Roles = roles
		return ctx, nil
	})
	cfg := &Config{UserAuth: AuthTypes{"cert": true}, ConfigTableName: "GNMI_CLIENT_CERT", EnableNativeWrite: true}
	gate := configuredRPCAuth(cfg)
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(bypass.MetadataKeyBypassValidation, "true"))
	p := func(db, ns, table string) *pb.Path {
		return &pb.Path{Elem: []*pb.PathElem{{Name: db}, {Name: ns}, {Name: table}}}
	}
	for _, tc := range []struct {
		name   string
		paths  []*pb.Path
		target string
		code   codes.Code
	}{
		{"native", []*pb.Path{p("CONFIG_DB", "localhost", "VNET")}, "", codes.OK},
		{"wrong scope", []*pb.Path{p("STATE_DB", "localhost", "TABLE")}, "", codes.PermissionDenied},
		{"collision", []*pb.Path{p("CONFIG_DB", "VNET", "VNET"), p("CONFIG_DB", "ACL_RULE", "ACL_RULE")}, "CONFIG_DB", codes.Unimplemented},
		{"short", []*pb.Path{{Elem: []*pb.PathElem{{Name: "VNET"}, {Name: "blue"}}}}, "CONFIG_DB", codes.OK},
		{"nil path", []*pb.Path{nil}, "", codes.Unimplemented},
		{"empty elems", []*pb.Path{{Elem: []*pb.PathElem{}}}, "", codes.Unimplemented},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := &pb.SetRequest{Prefix: &pb.Path{Origin: "sonic-db", Target: tc.target}}
			for _, p := range tc.paths {
				req.Update = append(req.Update, &pb.Update{Path: p, Val: &pb.TypedValue{Value: &pb.TypedValue_JsonIetfVal{JsonIetfVal: []byte(`{"field":"value"}`)}}})
			}
			effects := 0
			_, err := gate.UnaryInterceptor()(ctx, req, &grpc.UnaryServerInfo{FullMethod: "/gnmi.gNMI/Set"}, func(context.Context, interface{}) (interface{}, error) { effects++; return nil, nil })
			want := 0
			if tc.code == codes.OK {
				want = 1
			}
			if status.Code(err) != tc.code || effects != want {
				t.Fatalf("%v effects=%d", err, effects)
			}
		})
	}
	for _, method := range []string{"/gnmi.gNMI/NewMethod", "/gnoi.healthz.Healthz/Check", "/unknown.Service/Get"} {
		roles = []string{"gnmi_readwrite", "gnoi_readwrite"}
		effects := 0
		_, err := gate.UnaryInterceptor()(ctx, nil, &grpc.UnaryServerInfo{FullMethod: method}, func(context.Context, interface{}) (interface{}, error) { effects++; return nil, nil })
		want := codes.PermissionDenied
		if method == "/gnmi.gNMI/NewMethod" {
			want = codes.OK
		}
		if status.Code(err) != want {
			t.Fatal(method, err)
		}
	}
	roles = []string{"gnmi_readonly"}
	_, err := gate.UnaryInterceptor()(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/gnmi.gNMI/NewMethod"}, func(context.Context, interface{}) (interface{}, error) {
		t.Fatal("new method allowed readonly")
		return nil, nil
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatal(err)
	}
	roles = []string{"gnmi_config_db_readonly"}
	calls = 0
	req := &pb.GetRequest{Prefix: &pb.Path{Target: "CONFIG_DB"}}
	_, err = gate.UnaryInterceptor()(ctx, req, &grpc.UnaryServerInfo{FullMethod: "/gnmi.gNMI/Get"}, func(ctx context.Context, _ interface{}) (interface{}, error) {
		if _, err := authenticate(cfg, ctx, "gnmi_STATE_DB", false); status.Code(err) != codes.PermissionDenied {
			t.Fatal("target mismatch accepted")
		}
		_, err := authenticate(cfg, ctx, "gnmi_CONFIG_DB", true)
		return nil, err
	})
	if status.Code(err) != codes.PermissionDenied || calls != 1 {
		t.Fatalf("reuse stricterwrite: %v calls=%d", err, calls)
	}
	roles = []string{"gnmi_config_db_readwrite"}
	patches.ApplyFunc(bypass.TrySet, func(context.Context, *pb.Path, []*pb.Path, []*pb.Update) (*pb.SetResponse, bool, error) {
		return nil, false, nil
	})
	srv := &Server{config: cfg, ReqFromMaster: ReqFromMasterDisabledMA, SaveStartupConfig: saveOnSetDisabled}
	short := &pb.SetRequest{Prefix: &pb.Path{Origin: "sonic-db", Target: "CONFIG_DB"}, Update: []*pb.Update{{Path: &pb.Path{Elem: []*pb.PathElem{{Name: "VNET"}, {Name: "blue"}}}}}}
	_, err = gate.UnaryInterceptor()(ctx, short, &grpc.UnaryServerInfo{FullMethod: "/gnmi.gNMI/Set"}, func(ctx context.Context, _ interface{}) (interface{}, error) { return srv.Set(ctx, short) })
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("declined bypass fell through: %v", err)
	}
}
