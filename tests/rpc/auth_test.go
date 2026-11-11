//go:build e2e

package rpc_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	pb "github.com/openconfig/gnmi/proto/gnmi"
	gnmi "github.com/sonic-net/sonic-gnmi/gnmi_server"
	spb "github.com/sonic-net/sonic-gnmi/proto"
	"github.com/sonic-net/sonic-gnmi/tests/rpc/testutil"
	"google.golang.org/grpc"
)

func TestCertificateReadonlyGet(t *testing.T) {
	env := testutil.NewFixture(t)
	connectNativeRoleStore(t, env.Socket)
	ca := testutil.NewCertificateAuthority(t)
	service, err := gnmi.NewGetService(gnmi.Config{
		UserAuth: gnmi.AuthTypes{"cert": true}, ConfigTableName: "GNMI_CLIENT_CERT",
	}, func(paths []*pb.Path, prefix *pb.Path) (gnmi.GetReader, error) {
		return &fixedReader{path: paths[0]}, nil
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
		client := pb.NewGNMIClient(testutil.NewClientConn(t, server.Address, ca.ClientTLS(t, "reader")))
		response, err := client.Get(ctx, request)
		check(t, err)
		var values []string
		for _, notification := range response.Notification {
			for _, update := range notification.Update {
				var value string
				check(t, json.Unmarshal(update.Val.GetJsonIetfVal(), &value))
				values = append(values, value)
			}
		}
		if len(values) != 1 || values[0] != "Private" {
			t.Fatalf("Get returned %q; want [Private]", values)
		}
	})
}

type fixedReader struct{ path *pb.Path }

func (r *fixedReader) Get(*sync.WaitGroup) ([]*spb.Value, error) {
	return []*spb.Value{{Path: r.path, Val: &pb.TypedValue{Value: &pb.TypedValue_JsonIetfVal{JsonIetfVal: []byte(`"Private"`)}}}}, nil
}
func (*fixedReader) Close() error { return nil }
