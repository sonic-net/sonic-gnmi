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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGetServiceRejectsUnauthorized(t *testing.T) {
	env := testutil.NewFixture(t)
	connectNativeRedis(t, env)
	ca := testutil.NewCertificateAuthority(t)
	service, err := gnmi.NewGetService(gnmi.Config{
		UserAuth: gnmi.AuthTypes{"cert": true}, ConfigTableName: "GNMI_CLIENT_CERT",
	})
	check(t, err)
	server := testutil.StartServer(t, ca.ServerTLS(t), func(s *grpc.Server) { pb.RegisterGNMIServer(s, service) })
	request := &pb.GetRequest{Prefix: &pb.Path{Target: "CONFIG_DB"}, Encoding: pb.Encoding_JSON_IETF,
		Path: []*pb.Path{{Elem: []*pb.PathElem{{Name: "DEVICE_METADATA"}, {Name: "localhost"}, {Name: "cloudtype"}}}}}
	for _, test := range []struct {
		name string
		role string
		code codes.Code
	}{{"noaccess", "gnmi_config_db_noaccess", codes.PermissionDenied}, {"unmapped", "", codes.Unauthenticated}} {
		env.Run(t, test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			check(t, env.DB.HSet(ctx, "DEVICE_METADATA|localhost", "cloudtype", "Private").Err())
			if test.role != "" {
				check(t, env.DB.HSet(ctx, "GNMI_CLIENT_CERT|case", "role@", test.role).Err())
			}
			client := pb.NewGNMIClient(testutil.NewClientConn(t, server.Address, ca.ClientTLS(t, "case")))
			response, err := client.Get(ctx, request)
			if status.Code(err) != test.code || response != nil {
				t.Fatalf("code=%v want=%v response=%v", status.Code(err), test.code, response)
			}
		})
	}
}
