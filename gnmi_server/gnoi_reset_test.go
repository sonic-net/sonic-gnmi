package gnmi

import (
	"context"
	"crypto/tls"
	"fmt"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	reset_pb "github.com/openconfig/gnoi/factory_reset"
	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

const (
	factoryResetSuccess string = `{"reset_success": {}}`
	factoryResetFail    string = `{"reset_error": {"other": true, "detail": "Previous reset is ongoing."}}`
)

func TestGnoiResetServer(t *testing.T) {
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.Stop()

	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}
	targetAddr := "127.0.0.1:8081"
	conn, err := grpc.Dial(targetAddr, opts...)
	if err != nil {
		t.Fatalf("Dialing to %s failed: %v", targetAddr, err)
	}
	defer conn.Close()
	c := reset_pb.NewFactoryResetClient(conn)

	t.Run("Successful Reset", func(t *testing.T) {
		patch1 := gomonkey.ApplyFuncReturn(ssc.NewDbusClient, &ssc.DbusClient{}, nil)
		patch2 := gomonkey.ApplyFuncReturn(ssc.DbusApi, factoryResetSuccess, nil)
		defer patch1.Reset()
		defer patch2.Reset()

		resp, err := c.Start(context.Background(), &reset_pb.StartRequest{})
		if err != nil {
			t.Fatalf("Unexpected error %v", err)
		}
		if _, ok := resp.GetResponse().(*reset_pb.StartResponse_ResetSuccess); !ok {
			t.Fatalf("Expected ResetSuccess but got %#v", resp.Response)
		}
	})

	t.Run("Unsuccessful Reset, DBUS Client Error", func(t *testing.T) {
		patch := gomonkey.ApplyFuncReturn(ssc.NewDbusClient, nil, fmt.Errorf("client error"))
		defer patch.Reset()

		_, err := c.Start(context.Background(), &reset_pb.StartRequest{})
		if err == nil {
			t.Fatalf("Expected error but got success")
		}
		if status.Code(err) != codes.Internal {
			t.Fatalf("Expected Internal error but got %#v (%#v)", status.Code(err), err)
		}
	})

	t.Run("Unsuccessful Reset, DBUS Error", func(t *testing.T) {
		patch1 := gomonkey.ApplyFuncReturn(ssc.NewDbusClient, &ssc.DbusClient{}, nil)
		patch2 := gomonkey.ApplyFuncReturn(ssc.DbusApi, factoryResetFail, nil)
		defer patch1.Reset()
		defer patch2.Reset()

		resp, err := c.Start(context.Background(), &reset_pb.StartRequest{})
		if err != nil {
			t.Fatalf("Unexpected error %v", err)
		}
		if _, ok := resp.GetResponse().(*reset_pb.StartResponse_ResetError); !ok {
			t.Fatalf("Expected ResetError but got %#v", resp.Response)
		}
	})

}
