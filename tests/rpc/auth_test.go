//go:build e2e

package rpc_test

import (
	"context"
	"testing"
	"time"

	pb "github.com/openconfig/gnmi/proto/gnmi"
	gnmi "github.com/sonic-net/sonic-gnmi/gnmi_server"
	"github.com/sonic-net/sonic-gnmi/tests/rpc/testutil"
	"google.golang.org/grpc"
)

func TestCertificateReadonlyGet(t *testing.T) {
	env := testutil.NewFixture(t)
	connectNativeRedis(t, env)
	ca := testutil.NewCertificateAuthority(t)
	service, err := gnmi.NewGetService(gnmi.Config{
		UserAuth: gnmi.AuthTypes{"cert": true}, ConfigTableName: "GNMI_CLIENT_CERT",
	})
	check(t, err)
	server := testutil.StartServer(t, ca.ServerTLS(t), func(registrar *grpc.Server) {
		pb.RegisterGNMIServer(registrar, service)
	})
	request := &pb.GetRequest{Prefix: &pb.Path{Target: "CONFIG_DB"}, Encoding: pb.Encoding_JSON_IETF,
		Path: []*pb.Path{{Elem: []*pb.PathElem{{Name: "DEVICE_METADATA"}, {Name: "localhost"}, {Name: "cloudtype"}}}}}
	env.Run(t, "readonly", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		check(t, env.DB.HSet(ctx, "GNMI_CLIENT_CERT|reader", "role@", "gnmi_config_db_readonly").Err())
		check(t, env.DB.HSet(ctx, "DEVICE_METADATA|localhost", "cloudtype", "Private").Err())
		client := pb.NewGNMIClient(testutil.NewClientConn(t, server.Address, ca.ClientTLS(t, "reader")))
		response, err := client.Get(ctx, request)
		check(t, err)
		var values []string
		for _, notification := range response.Notification {
			for _, update := range notification.Update {
				value, ok := update.Val.GetValue().(*pb.TypedValue_StringVal)
				if !ok {
					t.Fatalf("field Get returned %T; want StringVal", update.Val.GetValue())
				}
				values = append(values, value.StringVal)
			}
		}
		if len(values) != 1 || values[0] != "Private" {
			t.Fatalf("Get returned %q; want [Private]", values)
		}
	})
}
