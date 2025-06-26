package gnmi

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	reset_pb "github.com/openconfig/gnoi/factory_reset"
	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"testing"
)

const factoryResetSuccess = `{"reset_success": {}}`
const factoryResetFail = `{"reset_error": {"other": true, "detail": "Previous reset is ongoing."}}`

type resetTestCase struct {
	desc   string
	msg    string
	err    error // If you want to simulate client creation failure
	expect func(t *testing.T, resp *reset_pb.StartResponse, err error)
}

var resetTests = []resetTestCase{
	{
		desc: "Successful Reset",
		msg:  factoryResetSuccess,
		expect: func(t *testing.T, resp *reset_pb.StartResponse, err error) {
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			if _, ok := resp.GetResponse().(*reset_pb.StartResponse_ResetSuccess); !ok {
				t.Fatalf("Expected ResetSuccess, got: %#v", resp.Response)
			}
		},
	},
	{
		desc: "DBUS Client Error",
		err:  errors.New("fake dbus error"),
		expect: func(t *testing.T, _ *reset_pb.StartResponse, err error) {
			if err == nil || status.Code(err) != codes.Internal {
				t.Fatalf("Expected Internal error, got: %#v", err)
			}
		},
	},
	{
		desc: "Reset Error from DBUS",
		msg:  factoryResetFail,
		expect: func(t *testing.T, resp *reset_pb.StartResponse, err error) {
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			if _, ok := resp.GetResponse().(*reset_pb.StartResponse_ResetError); !ok {
				t.Fatalf("Expected ResetError, got: %#v", resp.Response)
			}
		},
	},
}

func TestGnoiResetServer(t *testing.T) {
	for _, tc := range resetTests {
		t.Run(tc.desc, func(t *testing.T) {
			s := createServer(t, 8081)
			go runServer(t, s)
			defer s.Stop()

			// Override NewDbusClient
			orig := ssc.NewDbusClient
			defer func() { ssc.NewDbusClient = orig }()
			ssc.NewDbusClient = func(_ ssc.Caller) (ssc.Service, error) {
				if tc.err != nil {
					return nil, tc.err
				}
				return &ssc.FakeDbusClient{Msg: tc.msg}, nil
			}

			tlsConfig := &tls.Config{InsecureSkipVerify: true}
			conn, err := grpc.Dial(fmt.Sprintf("127.0.0.1:%d", s.config.Port),
				grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
			if err != nil {
				t.Fatalf("Dial failed: %v", err)
			}
			defer conn.Close()

			client := reset_pb.NewFactoryResetClient(conn)
			ctx := context.Background()

			resp, err := client.Start(ctx, &reset_pb.StartRequest{})
			tc.expect(t, resp, err)
		})
	}
}
