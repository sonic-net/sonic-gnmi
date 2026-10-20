package gnmi

import (
	"context"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/sonic-net/sonic-gnmi/pkg/bypass"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

type testAuthorizationContextKey struct{}

func testRPCAuthorizationInterceptor(roles ...string) *rpcAuthorizationInterceptor {
	return newRPCAuthorizationInterceptor(
		func(ctx context.Context) (context.Context, *rpcIdentity, error) {
			ctx = context.WithValue(ctx, testAuthorizationContextKey{}, "test-user")
			return ctx, &rpcIdentity{
				user:  "test-user",
				roles: roles,
			}, nil
		},
		nil,
	)
}

func testGetRequest(target string) *gnmipb.GetRequest {
	return &gnmipb.GetRequest{
		Prefix: &gnmipb.Path{Target: target},
		Path: []*gnmipb.Path{{
			Elem: []*gnmipb.PathElem{{Name: "TABLE"}},
		}},
	}
}

func testSetRequest(target string) *gnmipb.SetRequest {
	return &gnmipb.SetRequest{
		Prefix: &gnmipb.Path{Origin: "sonic-db"},
		Update: []*gnmipb.Update{{
			Path: &gnmipb.Path{Elem: []*gnmipb.PathElem{
				{Name: target},
				{Name: "localhost"},
				{Name: "TABLE"},
			}},
		}},
	}
}

func testSubscribeRequest(target string) *gnmipb.SubscribeRequest {
	return &gnmipb.SubscribeRequest{
		Request: &gnmipb.SubscribeRequest_Subscribe{
			Subscribe: &gnmipb.SubscriptionList{
				Prefix: &gnmipb.Path{Target: target},
				Mode:   gnmipb.SubscriptionList_STREAM,
				Subscription: []*gnmipb.Subscription{{
					Path: &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "TABLE"}}},
				}},
			},
		},
	}
}

func TestRPCAuthorizationMode(t *testing.T) {
	tests := []struct {
		name            string
		authModes       map[string]bool
		configTableName string
		wantEnabled     bool
		wantUnsupported bool
	}{
		{
			name:            "cert only GNMI_CLIENT_CERT",
			authModes:       map[string]bool{"cert": true},
			configTableName: gnmiClientCertTable,
			wantEnabled:     true,
		},
		{
			name:            "mixed auth",
			authModes:       map[string]bool{"cert": true, "jwt": true},
			configTableName: gnmiClientCertTable,
			wantUnsupported: true,
		},
		{
			name:            "cert without GNMI_CLIENT_CERT",
			authModes:       map[string]bool{"cert": true},
			wantUnsupported: true,
		},
		{
			name:      "password only unchanged",
			authModes: map[string]bool{"password": true},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			enabled, unsupported := rpcAuthorizationMode(test.authModes, test.configTableName)
			if enabled != test.wantEnabled {
				t.Fatalf("enabled = %t, want %t", enabled, test.wantEnabled)
			}
			if got := unsupported != ""; got != test.wantUnsupported {
				t.Fatalf("unsupported mode reported = %t, want %t", got, test.wantUnsupported)
			}
		})
	}
}

func TestRPCAuthorizationUnary(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		request     interface{}
		roles       []string
		wantCode    codes.Code
		wantEffects int
	}{
		{
			name:        "readonly read allowed",
			method:      "/gnmi.gNMI/Get",
			request:     testGetRequest("CONFIG_DB"),
			roles:       []string{"gnmi_config_db_readonly"},
			wantCode:    codes.OK,
			wantEffects: 1,
		},
		{
			name:     "readonly write denied",
			method:   "/gnmi.gNMI/Set",
			request:  testSetRequest("CONFIG_DB"),
			roles:    []string{"gnmi_config_db_readonly"},
			wantCode: codes.PermissionDenied,
		},
		{
			name:        "readwrite write allowed",
			method:      "/gnmi.gNMI/Set",
			request:     testSetRequest("CONFIG_DB"),
			roles:       []string{"gnmi_config_db_readwrite"},
			wantCode:    codes.OK,
			wantEffects: 1,
		},
		{
			name:        "readonly gNOI status allowed",
			method:      "/gnoi.system.System/RebootStatus",
			request:     &emptypb.Empty{},
			roles:       []string{"gnoi_readonly"},
			wantCode:    codes.OK,
			wantEffects: 1,
		},
		{
			name:     "readonly gNOI mutation denied",
			method:   "/gnoi.healthz.Healthz/Acknowledge",
			request:  &emptypb.Empty{},
			roles:    []string{"gnoi_readonly"},
			wantCode: codes.PermissionDenied,
		},
		{
			name:     "service noaccess overrides scoped allow",
			method:   "/gnmi.gNMI/Get",
			request:  testGetRequest("CONFIG_DB"),
			roles:    []string{"gnmi_noaccess", "gnmi_config_db_readonly"},
			wantCode: codes.PermissionDenied,
		},
		{
			name:        "unrelated service noaccess does not deny target",
			method:      "/gnmi.gNMI/Get",
			request:     testGetRequest("CONFIG_DB"),
			roles:       []string{"gnoi_noaccess", "gnmi_config_db_readonly"},
			wantCode:    codes.OK,
			wantEffects: 1,
		},
		{
			name:     "cross-service role denied",
			method:   "/gnoi.system.System/Time",
			request:  &emptypb.Empty{},
			roles:    []string{"gnmi_readonly"},
			wantCode: codes.PermissionDenied,
		},
		{
			name:     "wrong gNMI target denied",
			method:   "/gnmi.gNMI/Get",
			request:  testGetRequest("STATE_DB"),
			roles:    []string{"gnmi_config_db_readonly"},
			wantCode: codes.PermissionDenied,
		},
		{
			name:     "unknown role denied",
			method:   "/gnmi.gNMI/Get",
			request:  testGetRequest("CONFIG_DB"),
			roles:    []string{"sonic_linux"},
			wantCode: codes.PermissionDenied,
		},
		{
			name:     "unclassified method denied",
			method:   "/gnmi.gNMI/Unclassified",
			roles:    []string{"gnmi_readwrite"},
			wantCode: codes.PermissionDenied,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			effects := 0
			interceptor := testRPCAuthorizationInterceptor(test.roles...).UnaryInterceptor()
			_, err := interceptor(context.Background(), test.request, &grpc.UnaryServerInfo{FullMethod: test.method},
				func(ctx context.Context, _ interface{}) (interface{}, error) {
					effects++
					if !rpcAuthorizationCompleted(ctx) {
						t.Fatal("authorized unary context is not marked complete")
					}
					if user, _ := ctx.Value(testAuthorizationContextKey{}).(string); user != "test-user" {
						t.Fatalf("authorized context user = %q, want test-user", user)
					}
					return nil, nil
				})
			if got := status.Code(err); got != test.wantCode {
				t.Fatalf("status = %v, want %v; err=%v", got, test.wantCode, err)
			}
			if effects != test.wantEffects {
				t.Fatalf("handler/backend effects = %d, want %d", effects, test.wantEffects)
			}
		})
	}
}

func TestRPCAuthorizationStream(t *testing.T) {
	tests := []struct {
		name                string
		method              string
		request             proto.Message
		roles               []string
		wantCode            codes.Code
		wantEffects         int
		wantNetworkReceives int
	}{
		{
			name:                "readonly stream read allowed",
			method:              "/gnmi.gNMI/Subscribe",
			request:             testSubscribeRequest("STATE_DB"),
			roles:               []string{"gnmi_state_db_readonly"},
			wantCode:            codes.OK,
			wantEffects:         1,
			wantNetworkReceives: 1,
		},
		{
			name:     "readonly stream write denied",
			method:   "/gnoi.file.File/Put",
			roles:    []string{"gnoi_readonly"},
			wantCode: codes.PermissionDenied,
		},
		{
			name:        "readwrite stream write allowed",
			method:      "/gnoi.file.File/Put",
			roles:       []string{"gnoi_readwrite"},
			wantCode:    codes.OK,
			wantEffects: 1,
		},
		{
			name:     "noaccess stream denied",
			method:   "/gnmi.gNMI/Subscribe",
			request:  testSubscribeRequest("STATE_DB"),
			roles:    []string{"gnmi_noaccess", "gnmi_state_db_readonly"},
			wantCode: codes.PermissionDenied,
		},
		{
			name:                "wrong stream target denied",
			method:              "/gnmi.gNMI/Subscribe",
			request:             testSubscribeRequest("STATE_DB"),
			roles:               []string{"gnmi_config_db_readonly"},
			wantCode:            codes.PermissionDenied,
			wantNetworkReceives: 1,
		},
		{
			name:     "unknown stream role denied",
			method:   "/gnmi.gNMI/Subscribe",
			request:  testSubscribeRequest("STATE_DB"),
			roles:    []string{"sonic_linux"},
			wantCode: codes.PermissionDenied,
		},
		{
			name:     "cross-service stream role denied",
			method:   "/gnmi.gNMI/Subscribe",
			request:  testSubscribeRequest("STATE_DB"),
			roles:    []string{"gnoi_readonly"},
			wantCode: codes.PermissionDenied,
		},
		{
			name:    "scoped noaccess overrides same-target allow before receive",
			method:  "/gnmi.gNMI/Subscribe",
			request: testSubscribeRequest("STATE_DB"),
			roles: []string{
				"gnmi_state_db_noaccess",
				"gnmi_state_db_readonly",
			},
			wantCode: codes.PermissionDenied,
		},
		{
			name:     "malformed scoped role denied before receive",
			method:   "/gnmi.gNMI/Subscribe",
			request:  testSubscribeRequest("STATE_DB"),
			roles:    []string{"gnmi__readonly"},
			wantCode: codes.PermissionDenied,
		},
		{
			name:    "different scoped allow remains eligible",
			method:  "/gnmi.gNMI/Subscribe",
			request: testSubscribeRequest("CONFIG_DB"),
			roles: []string{
				"gnmi_state_db_noaccess",
				"gnmi_config_db_readonly",
			},
			wantCode:            codes.OK,
			wantEffects:         1,
			wantNetworkReceives: 1,
		},
		{
			name:                "initial EOF preserves aborted status",
			method:              "/gnmi.gNMI/Subscribe",
			roles:               []string{"gnmi_readonly"},
			wantCode:            codes.Aborted,
			wantNetworkReceives: 1,
		},
		{
			name:     "unclassified stream denied",
			method:   "/gnoi.file.File/Unclassified",
			roles:    []string{"gnoi_readwrite"},
			wantCode: codes.PermissionDenied,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			effects := 0
			testStream := &testAuthorizationServerStream{ctx: context.Background()}
			if test.request != nil {
				testStream.messages = []proto.Message{test.request}
			}
			interceptor := testRPCAuthorizationInterceptor(test.roles...).StreamInterceptor()
			err := interceptor(nil, testStream,
				&grpc.StreamServerInfo{FullMethod: test.method},
				func(_ interface{}, stream grpc.ServerStream) error {
					effects++
					if !rpcAuthorizationCompleted(stream.Context()) {
						t.Fatal("authorized stream context is not marked complete")
					}
					if user, _ := stream.Context().Value(testAuthorizationContextKey{}).(string); user != "test-user" {
						t.Fatalf("authorized stream user = %q, want test-user", user)
					}
					if test.request != nil {
						replayed := new(gnmipb.SubscribeRequest)
						if err := stream.RecvMsg(replayed); err != nil {
							t.Fatalf("buffered Subscribe request was not replayed: %v", err)
						}
						if got, want := replayed.GetSubscribe().GetPrefix().GetTarget(),
							test.request.(*gnmipb.SubscribeRequest).GetSubscribe().GetPrefix().GetTarget(); got != want {
							t.Fatalf("replayed Subscribe target = %q, want %q", got, want)
						}
					}
					return nil
				})
			if got := status.Code(err); got != test.wantCode {
				t.Fatalf("status = %v, want %v; err=%v", got, test.wantCode, err)
			}
			if effects != test.wantEffects {
				t.Fatalf("handler/backend effects = %d, want %d", effects, test.wantEffects)
			}
			if testStream.recvCalls != test.wantNetworkReceives {
				t.Fatalf("network RecvMsg calls = %d, want %d", testStream.recvCalls, test.wantNetworkReceives)
			}
		})
	}
}

func TestRPCAuthorizationRequestTarget(t *testing.T) {
	tests := []struct {
		name      string
		ctx       context.Context
		method    string
		request   interface{}
		want      string
		wantRoute rpcAuthorizationRoute
		wantCode  codes.Code
	}{
		{
			name:     "Get database target",
			method:   "/gnmi.gNMI/Get",
			request:  testGetRequest("CONFIG_DB"),
			want:     "gnmi_CONFIG_DB",
			wantCode: codes.OK,
		},
		{
			name:    "Get operational target",
			method:  "/gnmi.gNMI/Get",
			request: testGetRequest("OPERATIONAL"),
			want:    "gnoi",
		},
		{
			name:   "Get rejects malformed database target",
			method: "/gnmi.gNMI/Get",
			request: &gnmipb.GetRequest{
				Prefix: &gnmipb.Path{Target: "CONFIG_DB/asic0/extra"},
				Path:   []*gnmipb.Path{{Elem: []*gnmipb.PathElem{{Name: "TABLE"}}}},
			},
			wantCode: codes.InvalidArgument,
		},
		{
			name:   "Get native path target",
			method: "/gnmi.gNMI/Get",
			request: &gnmipb.GetRequest{
				Prefix: &gnmipb.Path{Origin: "sonic-db"},
				Path: []*gnmipb.Path{{
					Elem: []*gnmipb.PathElem{
						{Name: "CONFIG_DB"},
						{Name: "localhost"},
						{Name: "TABLE"},
					},
				}},
			},
			want: "gnmi_CONFIG_DB",
		},
		{
			name:   "Set native database target",
			method: "/gnmi.gNMI/Set",
			request: &gnmipb.SetRequest{
				Prefix: &gnmipb.Path{Origin: "sonic-db"},
				Update: []*gnmipb.Update{{
					Path: &gnmipb.Path{Elem: []*gnmipb.PathElem{
						{Name: "CONFIG_DB"},
						{Name: "localhost"},
						{Name: "TABLE"},
					}},
				}},
			},
			want: "gnmi_CONFIG_DB",
		},
		{
			name:    "Set non-native target",
			method:  "/gnmi.gNMI/Set",
			request: &gnmipb.SetRequest{Prefix: &gnmipb.Path{Origin: "openconfig"}},
			want:    "gnmi",
		},
		{
			name:   "Set rejects prefix and path database conflict",
			method: "/gnmi.gNMI/Set",
			request: &gnmipb.SetRequest{
				Prefix: &gnmipb.Path{Origin: "sonic-db", Target: "CONFIG_DB"},
				Update: []*gnmipb.Update{{
					Path: &gnmipb.Path{Elem: []*gnmipb.PathElem{
						{Name: "APPL_DB"},
						{Name: "localhost"},
						{Name: "TABLE"},
					}},
				}},
			},
			wantCode: codes.InvalidArgument,
		},
		{
			name: "Set accepts explicit bypass-only route",
			ctx: metadata.NewIncomingContext(
				context.Background(),
				metadata.Pairs(bypass.MetadataKeyBypassValidation, "true"),
			),
			method: "/gnmi.gNMI/Set",
			request: &gnmipb.SetRequest{
				Prefix: &gnmipb.Path{Origin: "sonic-db", Target: "CONFIG_DB"},
				Update: []*gnmipb.Update{{
					Path: &gnmipb.Path{Elem: []*gnmipb.PathElem{
						{Name: "VNET"},
						{Name: "blue"},
					}},
				}},
			},
			want:      "gnmi_CONFIG_DB",
			wantRoute: rpcAuthorizationRouteSetBypassOnly,
		},
		{
			name:   "Set rejects bypass-only shape without metadata",
			method: "/gnmi.gNMI/Set",
			request: &gnmipb.SetRequest{
				Prefix: &gnmipb.Path{Origin: "sonic-db", Target: "CONFIG_DB"},
				Update: []*gnmipb.Update{{
					Path: &gnmipb.Path{Elem: []*gnmipb.PathElem{
						{Name: "VNET"},
						{Name: "blue"},
					}},
				}},
			},
			wantCode: codes.InvalidArgument,
		},
		{
			name:    "Subscribe database target",
			method:  "/gnmi.gNMI/Subscribe",
			request: testSubscribeRequest("STATE_DB"),
			want:    "gnmi_STATE_DB",
		},
		{
			name:   "Subscribe openconfig target",
			method: "/gnmi.gNMI/Subscribe",
			request: &gnmipb.SubscribeRequest{
				Request: &gnmipb.SubscribeRequest_Subscribe{
					Subscribe: &gnmipb.SubscriptionList{
						Prefix: &gnmipb.Path{Origin: "openconfig", Target: "YANG"},
						Subscription: []*gnmipb.Subscription{{
							Path: &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "interfaces"}}},
						}},
					},
				},
			},
			want: "gnmi",
		},
		{
			name:   "Subscribe native database target",
			method: "/gnmi.gNMI/Subscribe",
			request: &gnmipb.SubscribeRequest{
				Request: &gnmipb.SubscribeRequest_Subscribe{
					Subscribe: &gnmipb.SubscriptionList{
						Prefix: &gnmipb.Path{Origin: "sonic-db"},
						Subscription: []*gnmipb.Subscription{{
							Path: &gnmipb.Path{Elem: []*gnmipb.PathElem{
								{Name: "STATE_DB"},
								{Name: "localhost"},
								{Name: "PORT_TABLE"},
							}},
						}},
					},
				},
			},
			want: "gnmi_STATE_DB",
		},
		{
			name:   "Subscribe rejects native target conflict",
			method: "/gnmi.gNMI/Subscribe",
			request: &gnmipb.SubscribeRequest{
				Request: &gnmipb.SubscribeRequest_Subscribe{
					Subscribe: &gnmipb.SubscriptionList{
						Prefix: &gnmipb.Path{Origin: "sonic-db", Target: "CONFIG_DB"},
						Subscription: []*gnmipb.Subscription{{
							Path: &gnmipb.Path{Elem: []*gnmipb.PathElem{
								{Name: "STATE_DB"},
								{Name: "localhost"},
								{Name: "PORT_TABLE"},
							}},
						}},
					},
				},
			},
			wantCode: codes.InvalidArgument,
		},
		{
			name:    "Subscribe events target",
			method:  "/gnmi.gNMI/Subscribe",
			request: testSubscribeRequest("EVENTS"),
			want:    "gnmi_events",
		},
		{
			name:    "Subscribe others target",
			method:  "/gnmi.gNMI/Subscribe",
			request: testSubscribeRequest("OTHERS"),
			want:    "gnmi_others",
		},
		{
			name:    "Subscribe operational is not gNOI",
			method:  "/gnmi.gNMI/Subscribe",
			request: testSubscribeRequest("OPERATIONAL"),
			want:    "gnmi",
		},
		{
			name:   "Subscribe rejects malformed database target",
			method: "/gnmi.gNMI/Subscribe",
			request: &gnmipb.SubscribeRequest{
				Request: &gnmipb.SubscribeRequest_Subscribe{
					Subscribe: &gnmipb.SubscriptionList{
						Prefix: &gnmipb.Path{Target: "STATE_DB/asic0/extra"},
						Subscription: []*gnmipb.Subscription{{
							Path: &gnmipb.Path{Elem: []*gnmipb.PathElem{{Name: "TABLE"}}},
						}},
					},
				},
			},
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "Subscribe requires initial list",
			method:   "/gnmi.gNMI/Subscribe",
			request:  &gnmipb.SubscribeRequest{},
			wantCode: codes.InvalidArgument,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := test.ctx
			if ctx == nil {
				ctx = context.Background()
			}
			got, err := rpcAuthorizationRequestTarget(ctx, test.method, test.request)
			if code := status.Code(err); code != test.wantCode {
				t.Fatalf("status = %v, want %v; err=%v", code, test.wantCode, err)
			}
			if got.target != test.want {
				t.Fatalf("target = %q, want %q", got.target, test.want)
			}
			if got.route != test.wantRoute {
				t.Fatalf("route = %v, want %v", got.route, test.wantRoute)
			}
		})
	}
}

func TestCompletedRPCAuthorizationNarrowsTarget(t *testing.T) {
	tests := []struct {
		name          string
		method        string
		request       interface{}
		roles         []string
		handlerTarget string
		wantCode      codes.Code
	}{
		{
			name:          "matching scoped target allowed",
			method:        "/gnmi.gNMI/Get",
			request:       testGetRequest("CONFIG_DB"),
			roles:         []string{"gnmi_config_db_readonly"},
			handlerTarget: "gnmi_CONFIG_DB",
			wantCode:      codes.OK,
		},
		{
			name:          "handler target mismatch denied",
			method:        "/gnmi.gNMI/Get",
			request:       testGetRequest("CONFIG_DB"),
			roles:         []string{"gnmi_config_db_readonly"},
			handlerTarget: "gnmi_STATE_DB",
			wantCode:      codes.PermissionDenied,
		},
		{
			name:          "operational Get uses gNOI target",
			method:        "/gnmi.gNMI/Get",
			request:       testGetRequest("OPERATIONAL"),
			roles:         []string{"gnoi_readonly"},
			handlerTarget: "gnoi",
			wantCode:      codes.OK,
		},
		{
			name:          "registry read classification allows status",
			method:        "/gnoi.system.System/RebootStatus",
			request:       &emptypb.Empty{},
			roles:         []string{"gnoi_readonly"},
			handlerTarget: "gnoi",
			wantCode:      codes.OK,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var authorizedCtx context.Context
			_, err := testRPCAuthorizationInterceptor(test.roles...).UnaryInterceptor()(
				context.Background(),
				test.request,
				&grpc.UnaryServerInfo{FullMethod: test.method},
				func(ctx context.Context, _ interface{}) (interface{}, error) {
					authorizedCtx = ctx
					return nil, nil
				},
			)
			if err != nil {
				t.Fatalf("central authorization failed: %v", err)
			}
			err = checkCompletedRPCAuthorization(authorizedCtx, "test-user", test.roles, test.handlerTarget)
			if got := status.Code(err); got != test.wantCode {
				t.Fatalf("status = %v, want %v; err=%v", got, test.wantCode, err)
			}
		})
	}
}

func TestRPCAuthorizationMarksBypassOnlySet(t *testing.T) {
	ctx := metadata.NewIncomingContext(
		context.Background(),
		metadata.Pairs(bypass.MetadataKeyBypassValidation, "true"),
	)
	request := &gnmipb.SetRequest{
		Prefix: &gnmipb.Path{Origin: "sonic-db", Target: "CONFIG_DB"},
		Update: []*gnmipb.Update{{
			Path: &gnmipb.Path{Elem: []*gnmipb.PathElem{
				{Name: "VNET"},
				{Name: "blue"},
			}},
		}},
	}

	effects := 0
	_, err := testRPCAuthorizationInterceptor("gnmi_config_db_readwrite").UnaryInterceptor()(
		ctx,
		request,
		&grpc.UnaryServerInfo{FullMethod: "/gnmi.gNMI/Set"},
		func(ctx context.Context, _ interface{}) (interface{}, error) {
			effects++
			if !rpcAuthorizationSetBypassOnly(ctx) {
				t.Fatal("authorized Set was not marked bypass-only")
			}
			return nil, nil
		},
	)
	if err != nil {
		t.Fatalf("bypass-only Set authorization failed: %v", err)
	}
	if effects != 1 {
		t.Fatalf("handler effects = %d, want 1", effects)
	}
}

type authorizationOrderService interface {
	Get(context.Context, *gnmipb.GetRequest) (*emptypb.Empty, error)
	Set(context.Context, *gnmipb.SetRequest) (*emptypb.Empty, error)
	Subscribe(grpc.ServerStream) error
}

type authorizationOrderServer struct {
	unaryCalls       *atomic.Int32
	streamCalls      *atomic.Int32
	subscribeTargets chan<- string
}

func (s *authorizationOrderServer) Get(context.Context, *gnmipb.GetRequest) (*emptypb.Empty, error) {
	s.unaryCalls.Add(1)
	return &emptypb.Empty{}, nil
}

func (s *authorizationOrderServer) Set(context.Context, *gnmipb.SetRequest) (*emptypb.Empty, error) {
	s.unaryCalls.Add(1)
	return &emptypb.Empty{}, nil
}

func (s *authorizationOrderServer) Subscribe(stream grpc.ServerStream) error {
	s.streamCalls.Add(1)
	request := new(gnmipb.SubscribeRequest)
	if err := stream.RecvMsg(request); err != nil {
		return err
	}
	if s.subscribeTargets != nil {
		s.subscribeTargets <- request.GetSubscribe().GetPrefix().GetTarget()
	}
	return stream.SendMsg(&emptypb.Empty{})
}

func authorizationOrderGetHandler(
	srv interface{},
	ctx context.Context,
	dec func(interface{}) error,
	interceptor grpc.UnaryServerInterceptor,
) (interface{}, error) {
	req := new(gnmipb.GetRequest)
	if err := dec(req); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(authorizationOrderService).Get(ctx, req)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/gnmi.gNMI/Get",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(authorizationOrderService).Get(ctx, req.(*gnmipb.GetRequest))
	}
	return interceptor(ctx, req, info, handler)
}

func authorizationOrderSetHandler(
	srv interface{},
	ctx context.Context,
	dec func(interface{}) error,
	interceptor grpc.UnaryServerInterceptor,
) (interface{}, error) {
	req := new(gnmipb.SetRequest)
	if err := dec(req); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(authorizationOrderService).Set(ctx, req)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/gnmi.gNMI/Set",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(authorizationOrderService).Set(ctx, req.(*gnmipb.SetRequest))
	}
	return interceptor(ctx, req, info, handler)
}

func authorizationOrderSubscribeHandler(srv interface{}, stream grpc.ServerStream) error {
	return srv.(authorizationOrderService).Subscribe(stream)
}

var authorizationOrderServiceDesc = grpc.ServiceDesc{
	ServiceName: "gnmi.gNMI",
	HandlerType: (*authorizationOrderService)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "Get",
			Handler:    authorizationOrderGetHandler,
		},
		{
			MethodName: "Set",
			Handler:    authorizationOrderSetHandler,
		},
	},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "Subscribe",
			Handler:       authorizationOrderSubscribeHandler,
			ServerStreams: true,
			ClientStreams: true,
		},
	},
}

type authorizationChainHarness struct {
	conn               *grpc.ClientConn
	innerUnaryCalls    *atomic.Int32
	innerStreamCalls   *atomic.Int32
	unaryHandlerCalls  *atomic.Int32
	streamHandlerCalls *atomic.Int32
	subscribeTargets   chan string
}

func newAuthorizationChainHarness(t *testing.T, roles ...string) *authorizationChainHarness {
	t.Helper()
	authorization := testRPCAuthorizationInterceptor(roles...)
	innerUnaryCalls := new(atomic.Int32)
	innerStreamCalls := new(atomic.Int32)
	unaryHandlerCalls := new(atomic.Int32)
	streamHandlerCalls := new(atomic.Int32)
	subscribeTargets := make(chan string, 1)
	innerUnary := func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		innerUnaryCalls.Add(1)
		return handler(ctx, req)
	}
	innerStream := func(
		srv interface{},
		stream grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		innerStreamCalls.Add(1)
		return handler(srv, stream)
	}

	server := grpc.NewServer(
		grpc.ChainUnaryInterceptor(authorization.UnaryInterceptor()),
		grpc.ChainStreamInterceptor(authorization.StreamInterceptor()),
		grpc.ChainUnaryInterceptor(innerUnary),
		grpc.ChainStreamInterceptor(innerStream),
	)
	server.RegisterService(&authorizationOrderServiceDesc, &authorizationOrderServer{
		unaryCalls:       unaryHandlerCalls,
		streamCalls:      streamHandlerCalls,
		subscribeTargets: subscribeTargets,
	})

	listener := bufconn.Listen(1024 * 1024)
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	conn, err := grpc.NewClient(
		"passthrough:///bufconn",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient() failed: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
	})

	return &authorizationChainHarness{
		conn:               conn,
		innerUnaryCalls:    innerUnaryCalls,
		innerStreamCalls:   innerStreamCalls,
		unaryHandlerCalls:  unaryHandlerCalls,
		streamHandlerCalls: streamHandlerCalls,
		subscribeTargets:   subscribeTargets,
	}
}

func invokeAuthorizationSubscribe(ctx context.Context, conn *grpc.ClientConn, request *gnmipb.SubscribeRequest) error {
	stream, err := conn.NewStream(ctx, &grpc.StreamDesc{
		StreamName:    "Subscribe",
		ServerStreams: true,
		ClientStreams: true,
	}, "/gnmi.gNMI/Subscribe")
	if err != nil {
		return err
	}
	if err = stream.SendMsg(request); err != nil {
		return err
	}
	if err = stream.CloseSend(); err != nil {
		return err
	}
	return stream.RecvMsg(&emptypb.Empty{})
}

func invokeAuthorizationSubscribeWithoutRequest(ctx context.Context, conn *grpc.ClientConn) error {
	stream, err := conn.NewStream(ctx, &grpc.StreamDesc{
		StreamName:    "Subscribe",
		ServerStreams: true,
		ClientStreams: true,
	}, "/gnmi.gNMI/Subscribe")
	if err != nil {
		return err
	}
	if err = stream.CloseSend(); err != nil {
		return err
	}
	return stream.RecvMsg(&emptypb.Empty{})
}

func TestRPCAuthorizationPrecedesChainedInnerInterceptors(t *testing.T) {
	harness := newAuthorizationChainHarness(t, "gnmi_noaccess", "gnmi_config_db_readwrite")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := harness.conn.Invoke(ctx, "/gnmi.gNMI/Set", testSetRequest("CONFIG_DB"), &emptypb.Empty{})
	if got := status.Code(err); got != codes.PermissionDenied {
		t.Fatalf("unary status = %v, want PermissionDenied; err=%v", got, err)
	}

	err = invokeAuthorizationSubscribe(ctx, harness.conn, testSubscribeRequest("CONFIG_DB"))
	if got := status.Code(err); got != codes.PermissionDenied {
		t.Fatalf("stream status = %v, want PermissionDenied; err=%v", got, err)
	}

	if got := harness.innerUnaryCalls.Load(); got != 0 {
		t.Fatalf("inner unary interceptor calls = %d, want 0", got)
	}
	if got := harness.innerStreamCalls.Load(); got != 0 {
		t.Fatalf("inner stream interceptor calls = %d, want 0", got)
	}
	if got := harness.unaryHandlerCalls.Load(); got != 0 {
		t.Fatalf("unary handler calls = %d, want 0", got)
	}
	if got := harness.streamHandlerCalls.Load(); got != 0 {
		t.Fatalf("stream handler calls = %d, want 0", got)
	}
}

func TestRPCAuthorizationWrongTargetPrecedesInnerInterceptors(t *testing.T) {
	harness := newAuthorizationChainHarness(t, "gnmi_config_db_readonly")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := harness.conn.Invoke(ctx, "/gnmi.gNMI/Get", testGetRequest("STATE_DB"), &emptypb.Empty{})
	if got := status.Code(err); got != codes.PermissionDenied {
		t.Fatalf("unary status = %v, want PermissionDenied; err=%v", got, err)
	}
	err = invokeAuthorizationSubscribe(ctx, harness.conn, testSubscribeRequest("STATE_DB"))
	if got := status.Code(err); got != codes.PermissionDenied {
		t.Fatalf("stream status = %v, want PermissionDenied; err=%v", got, err)
	}

	if got := harness.innerUnaryCalls.Load(); got != 0 {
		t.Fatalf("inner unary interceptor calls = %d, want 0", got)
	}
	if got := harness.innerStreamCalls.Load(); got != 0 {
		t.Fatalf("inner stream interceptor calls = %d, want 0", got)
	}
	if got := harness.unaryHandlerCalls.Load(); got != 0 {
		t.Fatalf("unary handler calls = %d, want 0", got)
	}
	if got := harness.streamHandlerCalls.Load(); got != 0 {
		t.Fatalf("stream handler calls = %d, want 0", got)
	}
}

func TestRPCAuthorizationRejectsAmbiguousTargetsBeforeInnerInterceptors(t *testing.T) {
	harness := newAuthorizationChainHarness(t, "gnmi_config_db_readwrite")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conflictingSet := &gnmipb.SetRequest{
		Prefix: &gnmipb.Path{Origin: "sonic-db", Target: "CONFIG_DB"},
		Update: []*gnmipb.Update{{
			Path: &gnmipb.Path{Elem: []*gnmipb.PathElem{
				{Name: "APPL_DB"},
				{Name: "localhost"},
				{Name: "TABLE"},
			}},
		}},
	}
	err := harness.conn.Invoke(ctx, "/gnmi.gNMI/Set", conflictingSet, &emptypb.Empty{})
	if got := status.Code(err); got != codes.InvalidArgument {
		t.Fatalf("conflicting Set status = %v, want InvalidArgument; err=%v", got, err)
	}

	malformedGet := &gnmipb.GetRequest{
		Prefix: &gnmipb.Path{Target: "CONFIG_DB/asic0/extra"},
		Path:   []*gnmipb.Path{{Elem: []*gnmipb.PathElem{{Name: "TABLE"}}}},
	}
	err = harness.conn.Invoke(ctx, "/gnmi.gNMI/Get", malformedGet, &emptypb.Empty{})
	if got := status.Code(err); got != codes.InvalidArgument {
		t.Fatalf("malformed Get status = %v, want InvalidArgument; err=%v", got, err)
	}

	conflictingSubscribe := &gnmipb.SubscribeRequest{
		Request: &gnmipb.SubscribeRequest_Subscribe{
			Subscribe: &gnmipb.SubscriptionList{
				Prefix: &gnmipb.Path{Origin: "sonic-db", Target: "CONFIG_DB"},
				Subscription: []*gnmipb.Subscription{{
					Path: &gnmipb.Path{Elem: []*gnmipb.PathElem{
						{Name: "STATE_DB"},
						{Name: "localhost"},
						{Name: "PORT_TABLE"},
					}},
				}},
			},
		},
	}
	err = invokeAuthorizationSubscribe(ctx, harness.conn, conflictingSubscribe)
	if got := status.Code(err); got != codes.InvalidArgument {
		t.Fatalf("conflicting Subscribe status = %v, want InvalidArgument; err=%v", got, err)
	}

	if got := harness.innerUnaryCalls.Load(); got != 0 {
		t.Fatalf("inner unary interceptor calls = %d, want 0", got)
	}
	if got := harness.innerStreamCalls.Load(); got != 0 {
		t.Fatalf("inner stream interceptor calls = %d, want 0", got)
	}
	if got := harness.unaryHandlerCalls.Load(); got != 0 {
		t.Fatalf("unary handler calls = %d, want 0", got)
	}
	if got := harness.streamHandlerCalls.Load(); got != 0 {
		t.Fatalf("stream handler calls = %d, want 0", got)
	}
}

func TestRPCAuthorizationBuffersSubscribeBeforeInnerChain(t *testing.T) {
	harness := newAuthorizationChainHarness(t, "gnmi_state_db_readonly")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := invokeAuthorizationSubscribe(ctx, harness.conn, testSubscribeRequest("STATE_DB")); err != nil {
		t.Fatalf("authorized Subscribe failed: %v", err)
	}
	if got := harness.innerStreamCalls.Load(); got != 1 {
		t.Fatalf("inner stream interceptor calls = %d, want 1", got)
	}
	if got := harness.streamHandlerCalls.Load(); got != 1 {
		t.Fatalf("stream handler calls = %d, want 1", got)
	}
	select {
	case target := <-harness.subscribeTargets:
		if target != "STATE_DB" {
			t.Fatalf("buffered Subscribe target = %q, want STATE_DB", target)
		}
	default:
		t.Fatal("handler did not receive the buffered Subscribe request")
	}
}

func TestRPCAuthorizationPreservesInitialSubscribeEOF(t *testing.T) {
	harness := newAuthorizationChainHarness(t, "gnmi_readonly")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := invokeAuthorizationSubscribeWithoutRequest(ctx, harness.conn)
	if got := status.Code(err); got != codes.Aborted {
		t.Fatalf("Subscribe status = %v, want Aborted; err=%v", got, err)
	}
	if got := harness.innerStreamCalls.Load(); got != 0 {
		t.Fatalf("inner stream interceptor calls = %d, want 0", got)
	}
	if got := harness.streamHandlerCalls.Load(); got != 0 {
		t.Fatalf("stream handler calls = %d, want 0", got)
	}
}

func TestRPCAuthorizationBypassPrecedesClassificationAndAuthentication(t *testing.T) {
	authCalls := 0
	interceptor := newRPCAuthorizationInterceptor(
		func(ctx context.Context) (context.Context, *rpcIdentity, error) {
			authCalls++
			return ctx, nil, errors.New("authentication should not run")
		},
		func(context.Context) bool { return true },
	)

	effects := 0
	_, err := interceptor.UnaryInterceptor()(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: "/unclassified.Service/Method"},
		func(context.Context, interface{}) (interface{}, error) {
			effects++
			return nil, nil
		})
	if err != nil {
		t.Fatalf("bypassed RPC failed: %v", err)
	}
	if authCalls != 0 {
		t.Fatalf("authentication calls = %d, want 0", authCalls)
	}
	if effects != 1 {
		t.Fatalf("handler effects = %d, want 1", effects)
	}
}

func TestLegacyRoleAccessCompatibility(t *testing.T) {
	tests := []struct {
		name      string
		roles     []string
		write     bool
		wantError bool
	}{
		{name: "unknown read remains allowed", roles: []string{"sonic_linux"}},
		{name: "unknown write remains denied", roles: []string{"sonic_linux"}, write: true, wantError: true},
		{name: "readonly read remains allowed", roles: []string{"gnmi_readonly"}},
		{name: "readonly write remains denied", roles: []string{"gnmi_readonly"}, write: true, wantError: true},
		{name: "readwrite write remains allowed", roles: []string{"gnmi_readwrite"}, write: true},
		{name: "noaccess read remains denied", roles: []string{"gnmi_noaccess"}, wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			access := resolveTargetRoleAccess(test.roles, "gnmi")
			err := checkResolvedRoleAccess("test-user", "gnmi", test.write, access, false)
			if got := err != nil; got != test.wantError {
				t.Fatalf("error = %v, wantError %t", err, test.wantError)
			}
		})
	}
}

func TestRPCAuthorizationPreservesAuthenticationError(t *testing.T) {
	wantErr := status.Error(codes.Unauthenticated, "certificate rejected")
	interceptor := newRPCAuthorizationInterceptor(
		func(ctx context.Context) (context.Context, *rpcIdentity, error) {
			return ctx, nil, wantErr
		},
		nil,
	)

	effects := 0
	_, err := interceptor.UnaryInterceptor()(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: "/gnmi.gNMI/Get"},
		func(context.Context, interface{}) (interface{}, error) {
			effects++
			return nil, errors.New("unexpected handler call")
		})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("status = %v, want Unauthenticated; err=%v", status.Code(err), err)
	}
	if effects != 0 {
		t.Fatalf("handler/backend effects = %d, want 0", effects)
	}
}

type testAuthorizationServerStream struct {
	grpc.ServerStream
	ctx       context.Context
	messages  []proto.Message
	recvCalls int
}

func (s *testAuthorizationServerStream) Context() context.Context {
	return s.ctx
}

func (s *testAuthorizationServerStream) RecvMsg(message interface{}) error {
	s.recvCalls++
	if len(s.messages) == 0 {
		return io.EOF
	}
	destination, ok := message.(proto.Message)
	if !ok {
		return errors.New("destination is not a protobuf message")
	}
	proto.Reset(destination)
	proto.Merge(destination, s.messages[0])
	s.messages = s.messages[1:]
	return nil
}
