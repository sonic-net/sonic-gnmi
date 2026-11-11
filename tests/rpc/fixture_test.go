//go:build e2e

package rpc_test

import (
	"context"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/openconfig/gnmi/proto/gnmi"
	gnmi "github.com/sonic-net/sonic-gnmi/gnmi_server"
	spb "github.com/sonic-net/sonic-gnmi/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestEnvironmentRollback(t *testing.T) {
	env := newEnvironment(t)
	ctx := context.Background()
	check(t, env.db.HSet(ctx, "original", "field", "before").Err())
	before, err := env.checkpoint(ctx)
	check(t, err)
	extraCleaned := false
	env.Run(t, "changes", func(t *testing.T) {
		check(t, env.db.HSet(ctx, "original", "field", "after").Err())
		check(t, env.db.Set(ctx, "new", "value", 0).Err())
		t.Cleanup(func() {
			value, err := env.db.HGet(ctx, "original", "field").Result()
			check(t, err)
			if value != "after" {
				t.Error("rollback ran before extra cleanup")
			}
			extraCleaned = true
		})
	})
	env.Run(t, "next", func(t *testing.T) {
		after, err := env.checkpoint(ctx)
		check(t, err)
		if !extraCleaned || !reflect.DeepEqual(before, after) {
			t.Fatal("next case did not see the original checkpoint")
		}
	})
}

func TestRollbackFailureStopsGroup(t *testing.T) {
	if os.Getenv("RPC_ROLLBACK_FAILURE_CHILD") == "1" {
		env := newEnvironment(t)
		env.Run(t, "break-rollback", func(t *testing.T) { check(t, env.db.Close()) })
		t.Fatal("FOLLOWING_CASE_EXECUTED")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRollbackFailureStopsGroup$", "-test.v")
	command.Env = append(os.Environ(), "RPC_ROLLBACK_FAILURE_CHILD=1")
	output, err := command.CombinedOutput()
	if err == nil || ctx.Err() != nil || strings.Contains(string(output), "FOLLOWING_CASE_EXECUTED") ||
		!strings.Contains(string(output), "environment restoration failed; stopping group") {
		t.Fatalf("expected bounded group abort after rollback failure, got %v: %s", err, output)
	}
}

type observedReader struct{ calls *atomic.Int32 }

func (r *observedReader) Get(*sync.WaitGroup) ([]*spb.Value, error) {
	r.calls.Add(1)
	return nil, nil
}
func (*observedReader) Close() error { return nil }

func TestGetServiceRejectsBeforeDataRead(t *testing.T) {
	env := newEnvironment(t)
	var reads atomic.Int32
	service, err := gnmi.NewGetService(gnmi.Config{
		UserAuth: gnmi.AuthTypes{"cert": true}, ConfigTableName: "GNMI_CLIENT_CERT",
	}, func([]*pb.Path, *pb.Path) (gnmi.GetReader, error) { return &observedReader{calls: &reads}, nil })
	check(t, err)
	server := startServer(t, env.serverTLS(), func(s *grpc.Server) { pb.RegisterGNMIServer(s, service) })
	request := &pb.GetRequest{Prefix: &pb.Path{Target: "CONFIG_DB"}, Encoding: pb.Encoding_JSON_IETF,
		Path: []*pb.Path{{Elem: []*pb.PathElem{{Name: "anything"}}}}}
	env.ready(t, server.address, request)
	reads.Store(0)
	for _, test := range []struct {
		name string
		role string
		code codes.Code
	}{{"noaccess", "gnmi_config_db_noaccess", codes.PermissionDenied}, {"unmapped", "", codes.Unauthenticated}} {
		env.Run(t, test.name, func(t *testing.T) {
			if test.role != "" {
				env.role(t, "case", test.role)
			}
			client := pb.NewGNMIClient(newClient(t, server.address, env.clientTLS(t, "case")))
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, err := client.Get(ctx, request)
			if status.Code(err) != test.code || reads.Load() != 0 {
				t.Fatalf("code=%v want=%v data reads=%d", status.Code(err), test.code, reads.Load())
			}
		})
	}
}
