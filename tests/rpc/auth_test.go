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
	"google.golang.org/grpc"
)

func TestCertificateReadonlyGet(t *testing.T) {
	env := newEnvironment(t)
	service, err := gnmi.NewGetService(gnmi.Config{
		UserAuth: gnmi.AuthTypes{"cert": true}, ConfigTableName: "GNMI_CLIENT_CERT",
	}, func(paths []*pb.Path, prefix *pb.Path) (gnmi.GetReader, error) {
		return &fixedReader{path: paths[0]}, nil
	})
	check(t, err)
	server := startServer(t, env.serverTLS(), func(registrar *grpc.Server) {
		pb.RegisterGNMIServer(registrar, service)
	})
	request := &pb.GetRequest{Prefix: &pb.Path{Target: "CONFIG_DB"}, Encoding: pb.Encoding_JSON_IETF,
		Path: []*pb.Path{{Elem: []*pb.PathElem{{Name: "DEVICE_METADATA"}, {Name: "localhost"}, {Name: "cloudtype"}}}}}
	env.ready(t, server.address, request)
	env.Run(t, "readonly", func(t *testing.T) {
		env.role(t, "reader", "gnmi_config_db_readonly")
		client := pb.NewGNMIClient(newClient(t, server.address, env.clientTLS(t, "reader")))
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
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
