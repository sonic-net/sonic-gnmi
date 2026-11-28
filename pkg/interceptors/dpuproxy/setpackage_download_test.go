package dpuproxy

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/openconfig/gnoi/common"
	syspb "github.com/openconfig/gnoi/system"
	testcert "github.com/sonic-net/sonic-gnmi/testdata/tls"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

type packageReceiver struct {
	syspb.UnimplementedSystemServer
	requests chan *syspb.SetPackageRequest
}

func (s *packageReceiver) SetPackage(stream syspb.System_SetPackageServer) error {
	req, err := stream.Recv()
	if err != nil {
		return err
	}
	s.requests <- req
	if _, err := stream.Recv(); err != io.EOF {
		return err
	}
	return stream.SendAndClose(&syspb.SetPackageResponse{})
}

func TestSetPackageRemoteDownloadTargetsDPU(t *testing.T) {
	cert, err := testcert.NewCert()
	if err != nil {
		t.Fatal(err)
	}
	dpuListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dpu := &packageReceiver{requests: make(chan *syspb.SetPackageRequest, 1)}
	dpuServer := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12,
	})))
	syspb.RegisterSystemServer(dpuServer, dpu)
	go dpuServer.Serve(dpuListener)
	t.Cleanup(dpuServer.Stop)
	host, port, _ := net.SplitHostPort(dpuListener.Addr().String())
	resolver := &DPUResolver{}
	patches := gomonkey.ApplyMethod(reflect.TypeOf(resolver), "GetDPUInfo",
		func(_ *DPUResolver, ctx context.Context, index string) (*DPUInfo, error) {
			if index != "3" {
				t.Errorf("resolved wrong DPU: %s", index)
			}
			return &DPUInfo{Index: index, IPAddress: host, Reachable: true, GNMIPortsToTry: []string{port}}, nil
		})
	defer patches.Reset()
	proxy := NewDPUProxy(resolver)
	t.Cleanup(func() {
		for _, conn := range proxy.conns {
			conn.Close()
		}
	})
	npuListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	npu := &packageReceiver{requests: make(chan *syspb.SetPackageRequest, 1)}
	npuServer := grpc.NewServer(grpc.StreamInterceptor(proxy.StreamInterceptor()))
	syspb.RegisterSystemServer(npuServer, npu)
	go npuServer.Serve(npuListener)
	t.Cleanup(npuServer.Stop)
	conn, err := grpc.NewClient(npuListener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(
		MetadataKeyTargetType, "dpu", MetadataKeyTargetIndex, "3",
	))
	stream, err := syspb.NewSystemClient(conn).SetPackage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&syspb.SetPackageRequest{Request: &syspb.SetPackageRequest_Package{
		Package: &syspb.Package{
			Filename:       "/var/tmp/image.bin",
			RemoteDownload: &common.RemoteDownload{Path: "https://images.example/image", Protocol: common.RemoteDownload_HTTPS},
		},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.CloseAndRecv(); err != nil {
		t.Fatal(err)
	}
	select {
	case req := <-dpu.requests:
		if req.GetPackage().GetRemoteDownload().GetPath() != "https://images.example/image" {
			t.Fatalf("remote package changed in proxy: %v", req)
		}
	default:
		t.Fatal("DPU did not receive remote package")
	}
	if len(npu.requests) != 0 {
		t.Fatal("NPU handled DPU install/download")
	}
}
