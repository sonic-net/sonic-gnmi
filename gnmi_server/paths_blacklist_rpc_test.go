package gnmi

// RPC-level tests for paths blacklist enforcement: they start a gNMI server
// with Config.PathsBlacklist set and assert that Get, Set, and Subscribe are
// rejected with a generic PermissionDenied, and that authentication failures
// take precedence over blacklist results.

import (
	"context"
	"crypto/tls"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	pb "github.com/openconfig/gnmi/proto/gnmi"
	testcert "github.com/sonic-net/sonic-gnmi/testdata/tls"

	"github.com/sonic-net/sonic-gnmi/pkg/pathblacklist"
	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
)

func createBlacklistServer(t *testing.T, port int64, policyContent string, userAuth AuthTypes) *Server {
	t.Helper()
	policy, err := pathblacklist.Parse(strings.NewReader(policyContent))
	if err != nil {
		t.Fatalf("failed to parse blacklist policy: %v", err)
	}
	certificate, err := testcert.NewCert()
	if err != nil {
		t.Fatalf("could not load server key pair: %s", err)
	}
	tlsCfg := &tls.Config{
		ClientAuth:   tls.RequestClientCert,
		Certificates: []tls.Certificate{certificate},
	}
	tlsOpts := []grpc.ServerOption{grpc.Creds(credentials.NewTLS(tlsCfg))}
	cfg := &Config{
		Port:                port,
		EnableTranslibWrite: true,
		EnableNativeWrite:   true,
		Threshold:           100,
		ImgDir:              "/tmp",
		UserAuth:            userAuth,
		PathsBlacklist:      policy,
	}
	s, err := NewServer(cfg, tlsOpts, nil)
	if err != nil {
		t.Fatalf("Failed to create gNMI server: %v", err)
	}
	return s
}

func dialBlacklistServer(t *testing.T) (pb.GNMIClient, *grpc.ClientConn) {
	t.Helper()
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}
	conn, err := grpc.Dial("127.0.0.1:8081", opts...)
	if err != nil {
		t.Fatalf("Dialing to 127.0.0.1:8081 failed: %v", err)
	}
	return pb.NewGNMIClient(conn), conn
}

func TestGetWithPathsBlacklist(t *testing.T) {
	s := createBlacklistServer(t, 8081,
		"COUNTERS_DB /COUNTERS/Ethernet68\nCONFIG_DB /PORT\n", nil)
	go runServer(t, s)
	defer s.ForceStop()

	ns, _ := sdcfg.GetDbDefaultNamespace()
	prepareDb(t, ns)

	gClient, conn := dialBlacklistServer(t)
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tds := []struct {
		desc        string
		pathTarget  string
		textPbPath  string
		wantRetCode codes.Code
	}{
		{
			desc:        "blacklisted path is rejected",
			pathTarget:  "COUNTERS_DB",
			textPbPath:  `elem: <name: "COUNTERS" > elem: <name: "Ethernet68" >`,
			wantRetCode: codes.PermissionDenied,
		},
		{
			desc:        "suffix glob covering blacklisted path is rejected",
			pathTarget:  "COUNTERS_DB",
			textPbPath:  `elem: <name: "COUNTERS" > elem: <name: "Ethernet*" >`,
			wantRetCode: codes.PermissionDenied,
		},
		{
			desc:        "ancestor of blacklisted path is rejected",
			pathTarget:  "COUNTERS_DB",
			textPbPath:  `elem: <name: "COUNTERS" >`,
			wantRetCode: codes.PermissionDenied,
		},
		{
			desc:        "sibling path is allowed",
			pathTarget:  "COUNTERS_DB",
			textPbPath:  `elem: <name: "COUNTERS" > elem: <name: "Ethernet1" >`,
			wantRetCode: codes.OK,
		},
	}
	for _, td := range tds {
		t.Run(td.desc, func(t *testing.T) {
			runTestGet(t, ctx, gClient, td.pathTarget, td.textPbPath, td.wantRetCode, nil, false)
		})
	}

	t.Run("native sonic-db form is rejected", func(t *testing.T) {
		req := &pb.GetRequest{
			Prefix: &pb.Path{Origin: "sonic-db"},
			Path: []*pb.Path{{Elem: []*pb.PathElem{
				{Name: "CONFIG_DB"}, {Name: "localhost"}, {Name: "PORT"}}}},
			Encoding: pb.Encoding_JSON_IETF,
		}
		_, err := gClient.Get(ctx, req)
		if got := status.Code(err); got != codes.PermissionDenied {
			t.Fatalf("got return code %v (err %v), want PermissionDenied", got, err)
		}
	})
}

func TestSetWithPathsBlacklist(t *testing.T) {
	s := createBlacklistServer(t, 8081, "CONFIG_DB /PORT\n", nil)
	go runServer(t, s)
	defer s.ForceStop()

	gClient, conn := dialBlacklistServer(t)
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req := &pb.SetRequest{
		Prefix: &pb.Path{Origin: "sonic-db"},
		Update: []*pb.Update{{
			Path: &pb.Path{Elem: []*pb.PathElem{
				{Name: "CONFIG_DB"}, {Name: "localhost"}, {Name: "PORT"}, {Name: "Ethernet0"}}},
			Val: &pb.TypedValue{Value: &pb.TypedValue_JsonIetfVal{JsonIetfVal: []byte(`{"admin_status": "down"}`)}},
		}},
	}
	_, err := gClient.Set(ctx, req)
	if got := status.Code(err); got != codes.PermissionDenied {
		t.Fatalf("got return code %v (err %v), want PermissionDenied", got, err)
	}
	if msg := status.Convert(err).Message(); strings.Contains(msg, "CONFIG_DB") {
		t.Fatalf("error message leaks policy contents: %q", msg)
	}
}

func TestSubscribeWithPathsBlacklist(t *testing.T) {
	s := createBlacklistServer(t, 8081, "COUNTERS_DB /COUNTERS/Ethernet68\n", nil)
	go runServer(t, s)
	defer s.ForceStop()

	ns, _ := sdcfg.GetDbDefaultNamespace()
	prepareDb(t, ns)

	gClient, conn := dialBlacklistServer(t)
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stream, err := gClient.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}
	sub := &pb.SubscribeRequest{Request: &pb.SubscribeRequest_Subscribe{
		Subscribe: &pb.SubscriptionList{
			Prefix: &pb.Path{Target: "COUNTERS_DB"},
			Subscription: []*pb.Subscription{{
				Path: &pb.Path{Elem: []*pb.PathElem{{Name: "COUNTERS"}, {Name: "Ethernet68"}}},
			}},
			Mode: pb.SubscriptionList_ONCE,
		},
	}}
	if err := stream.Send(sub); err != nil {
		t.Fatalf("Failed to send subscribe request: %v", err)
	}
	_, err = stream.Recv()
	if got := status.Code(err); got != codes.PermissionDenied {
		t.Fatalf("got return code %v (err %v), want PermissionDenied", got, err)
	}
}

func TestPathsBlacklistUnauthenticated(t *testing.T) {
	// With client auth enabled and no credentials supplied, an RPC for a
	// blacklisted path must fail authentication, not report a policy result.
	s := createBlacklistServer(t, 8081, "COUNTERS_DB /COUNTERS/Ethernet68\n",
		AuthTypes{"password": true, "cert": true, "jwt": true})
	go runServer(t, s)
	defer s.ForceStop()

	ns, _ := sdcfg.GetDbDefaultNamespace()
	prepareDb(t, ns)

	gClient, conn := dialBlacklistServer(t)
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req := &pb.GetRequest{
		Prefix:   &pb.Path{Target: "COUNTERS_DB"},
		Path:     []*pb.Path{{Elem: []*pb.PathElem{{Name: "COUNTERS"}, {Name: "Ethernet68"}}}},
		Encoding: pb.Encoding_JSON_IETF,
	}
	_, err := gClient.Get(ctx, req)
	if got := status.Code(err); got != codes.Unauthenticated {
		t.Fatalf("got return code %v (err %v), want Unauthenticated", got, err)
	}
}
