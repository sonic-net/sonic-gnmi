package gnmi

import (
	"context"
	"crypto/tls"
	"fmt"
	reset_pb "github.com/openconfig/gnoi/factory_reset"
	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"testing"
)

const (
	factoryResetSuccess string = `{"reset_success": {}}`
	factoryResetFail    string = `{"reset_error": {"other": true, "detail": "Previous reset is ongoing."}}`
)

var resetTests = []struct {
	desc       string
	dbusCaller ssc.Caller
	f          func(t *testing.T, ctx context.Context, c reset_pb.FactoryResetClient, s *Server)
}{
	{
		desc:       "Successful Reset",
		dbusCaller: &ssc.FakeDbusCaller{Msg: factoryResetSuccess},
		f: func(t *testing.T, ctx context.Context, c reset_pb.FactoryResetClient, s *Server) {
			resp, err := c.Start(ctx, &reset_pb.StartRequest{})
			if err != nil {
				t.Fatalf("Unexpected error %v", err)
			}
			if _, ok := resp.GetResponse().(*reset_pb.StartResponse_ResetSuccess); !ok {
				t.Fatalf("Expected ResetSuccess but got %#v", resp.Response)
			}
		},
	},
	{
		desc:       "Unsuccessful Reset, DBUS Client Error",
		dbusCaller: nil,
		f: func(t *testing.T, ctx context.Context, c reset_pb.FactoryResetClient, s *Server) {
			_, err := c.Start(ctx, &reset_pb.StartRequest{})
			if err == nil {
				t.Fatalf("Expected error but got success")
			}
			if status.Code(err) != codes.Internal {
				t.Fatalf("Expected Internal error but got %#v (%#v)", status.Code(err), err)
			}
		},
	},
	{
		desc:       "Unsuccessful Reset, DBUS Error",
		dbusCaller: &ssc.FakeDbusCaller{Msg: factoryResetFail},
		f: func(t *testing.T, ctx context.Context, c reset_pb.FactoryResetClient, s *Server) {
			resp, err := c.Start(ctx, &reset_pb.StartRequest{})
			if err != nil {
				t.Fatalf("Unexpected error %v", err)
			}
			if _, ok := resp.GetResponse().(*reset_pb.StartResponse_ResetError); !ok {
				t.Fatalf("Expected ResetError but got %#v", resp.Response)
			}
		},
	},
	/*{
		desc:       "Unsuccessful Reset, NSF Freeze",
		dbusCaller: &ssc.FakeDbusCaller{},
		f: func(t *testing.T, ctx context.Context, c reset_pb.FactoryResetClient, s *Server) {
			s.WarmRestartHelper.SetFreezeStatus(true)
			defer s.WarmRestartHelper.SetFreezeStatus(false)
			_, err := c.Start(ctx, &reset_pb.StartRequest{})
			t.Logf("Case 4: Error %#v", err)
			if err == nil {
				t.Fatalf("Expected error but got success")
			}
			if status.Code(err) != codes.Unavailable {
				t.Fatalf("Expected Unavailable error but got %#v (%#v)", status.Code(err), err)
			}
		},
	},*/
}

// TestGnoiResetServer tests the implementation of the gnoi.FactoryReset server.
func TestGnoiResetServer(t *testing.T) {
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.Stop()
	defer resetDbusCaller()
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}
	targetAddr := fmt.Sprintf("127.0.0.1:%d", s.config.Port)
	conn, err := grpc.Dial(targetAddr, opts...)
	if err != nil {
		t.Fatalf("Dialing to %s failed: %v", targetAddr, err)
	}
	defer conn.Close()
	c := reset_pb.NewFactoryResetClient(conn)
	for _, tc := range resetTests {
		ctx := context.Background()
		t.Run(tc.desc, func(t *testing.T) {
			dbusCaller = tc.dbusCaller
			tc.f(t, ctx, c, s)
		})
	}
}
