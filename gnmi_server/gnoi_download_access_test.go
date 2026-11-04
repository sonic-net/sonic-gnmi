package gnmi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"

	gnoi_common_pb "github.com/openconfig/gnoi/common"
	gnoi_containerz_pb "github.com/openconfig/gnoi/containerz"
	gnoi_file_pb "github.com/openconfig/gnoi/file"
	gnoioras "github.com/sonic-net/sonic-gnmi/proto/gnoi/oras"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

type rejectDownloadTransport struct {
	calls int
}

func (r *rejectDownloadTransport) RoundTrip(*http.Request) (*http.Response, error) {
	r.calls++
	return nil, errors.New("unexpected outbound request")
}

func downloadPeerContexts() map[string]context.Context {
	return map[string]context.Context{
		"missing peer": context.Background(),
		"TCP IPv4": peer.NewContext(context.Background(), &peer.Peer{
			Addr: &net.TCPAddr{IP: net.IPv4(192, 0, 2, 1)},
		}),
		"TCP IPv6": peer.NewContext(context.Background(), &peer.Peer{
			Addr: &net.TCPAddr{IP: net.ParseIP("2001:db8::1")},
		}),
		"UDS": peer.NewContext(context.Background(), &peer.Peer{
			Addr: &net.UnixAddr{Name: "test.sock", Net: "unix"},
		}),
	}
}

func TestFileTransferToRemoteDisabledBeforeDownload(t *testing.T) {
	transport := &rejectDownloadTransport{}
	previous := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = previous })
	server := &FileServer{Server: &Server{config: &Config{}}}

	for name, ctx := range downloadPeerContexts() {
		for _, target := range []string{"local", "DPU"} {
			t.Run(name+"/"+target, func(t *testing.T) {
				requestContext := ctx
				if target == "DPU" {
					requestContext = metadata.NewIncomingContext(ctx, metadata.Pairs(
						"x-sonic-ss-target-type", "dpu",
						"x-sonic-ss-target-index", "0",
					))
				}
				response, err := server.TransferToRemote(requestContext, &gnoi_file_pb.TransferToRemoteRequest{
					LocalPath: "/tmp/test-image.bin",
					RemoteDownload: &gnoi_common_pb.RemoteDownload{
						Protocol: gnoi_common_pb.RemoteDownload_HTTP,
						Path:     "http://images.example/image.bin",
					},
				})
				if status.Code(err) != codes.Unimplemented || response != nil {
					t.Fatalf("TransferToRemote() = %v, %v; want nil, Unimplemented", response, err)
				}
				if transport.calls != 0 {
					t.Fatalf("outbound requests = %d, want 0", transport.calls)
				}
			})
		}
	}
}

func TestContainerzHTTPDisabledBeforeDownload(t *testing.T) {
	for name, ctx := range downloadPeerContexts() {
		for _, protocol := range []gnoi_common_pb.RemoteDownload_Protocol{
			gnoi_common_pb.RemoteDownload_HTTP,
			gnoi_common_pb.RemoteDownload_HTTPS,
		} {
			t.Run(name+"/"+protocol.String(), func(t *testing.T) {
				fixture := &deployFixture{}
				server := newContainerzTestServer(fixture.dependencies(t))
				transfer := validImageTransfer()
				transfer.RemoteDownload.Protocol = protocol
				scheme := "http"
				if protocol == gnoi_common_pb.RemoteDownload_HTTPS {
					scheme = "https"
				}
				transfer.RemoteDownload.Path = scheme + "://images.example/image.tar"
				stream := &dummyDeployServer{
					ctx:       ctx,
					recvQueue: []*gnoi_containerz_pb.DeployRequest{deployRequest(transfer)},
				}
				if err := server.Deploy(stream); status.Code(err) != codes.FailedPrecondition {
					t.Fatalf("Deploy() = %v, want FailedPrecondition", err)
				}
				if len(fixture.events) != 0 || fixture.tempPattern != "" {
					t.Fatalf("work performed before denial: %v, %q", fixture.events, fixture.tempPattern)
				}
			})
		}
	}
}

type downloadOrasStream struct {
	gnoioras.Oras_PullServer
	ctx context.Context
}

func (s *downloadOrasStream) Context() context.Context { return s.ctx }

func TestOrasPullRequiresUnixPeer(t *testing.T) {
	server := &OrasServer{Server: &Server{config: &Config{}}}
	for name, ctx := range downloadPeerContexts() {
		t.Run(name, func(t *testing.T) {
			// A UDS request reaches ordinary validation; all other transports
			// must be denied before validation or registry construction.
			want := codes.PermissionDenied
			if name == "UDS" {
				want = codes.InvalidArgument
			}
			ctx = metadata.NewIncomingContext(ctx, metadata.Pairs(
				"x-forwarded-proto", "unix",
			))
			err := server.Pull(&gnoioras.PullRequest{}, &downloadOrasStream{ctx: ctx})
			if status.Code(err) != want {
				t.Fatalf("Pull() = %v, want %v", err, want)
			}
		})
	}
}
