//go:build e2e

package rpc_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/openconfig/gnmi/proto/gnmi"
	gnmi "github.com/sonic-net/sonic-gnmi/gnmi_server"
	spb "github.com/sonic-net/sonic-gnmi/proto"
	"github.com/sonic-net/sonic-gnmi/tests/rpc/testutil"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type observedReader struct{ calls *atomic.Int32 }

func (r *observedReader) Get(*sync.WaitGroup) ([]*spb.Value, error) {
	r.calls.Add(1)
	return nil, nil
}
func (*observedReader) Close() error { return nil }

func TestGetServiceRejectsBeforeDataRead(t *testing.T) {
	env := testutil.NewFixture(t)
	connectNativeRoleStore(t, env.Socket)
	ca := testutil.NewCertificateAuthority(t)
	var reads atomic.Int32
	service, err := gnmi.NewGetService(gnmi.Config{
		UserAuth: gnmi.AuthTypes{"cert": true}, ConfigTableName: "GNMI_CLIENT_CERT",
	}, func([]*pb.Path, *pb.Path) (gnmi.GetReader, error) { return &observedReader{calls: &reads}, nil })
	check(t, err)
	server := testutil.StartServer(t, ca.ServerTLS(t), func(s *grpc.Server) { pb.RegisterGNMIServer(s, service) })
	request := &pb.GetRequest{Prefix: &pb.Path{Target: "CONFIG_DB"}, Encoding: pb.Encoding_JSON_IETF,
		Path: []*pb.Path{{Elem: []*pb.PathElem{{Name: "anything"}}}}}
	for _, test := range []struct {
		name string
		role string
		code codes.Code
	}{{"noaccess", "gnmi_config_db_noaccess", codes.PermissionDenied}, {"unmapped", "", codes.Unauthenticated}} {
		env.Run(t, test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if test.role != "" {
				check(t, env.DB.HSet(ctx, "GNMI_CLIENT_CERT|case", "role@", test.role).Err())
			}
			client := pb.NewGNMIClient(testutil.NewClientConn(t, server.Address, ca.ClientTLS(t, "case")))
			_, err := client.Get(ctx, request)
			if status.Code(err) != test.code || reads.Load() != 0 {
				t.Fatalf("code=%v want=%v data reads=%d", status.Code(err), test.code, reads.Load())
			}
		})
	}
}
