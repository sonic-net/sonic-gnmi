package gnmi

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	resetpb "github.com/openconfig/gnoi/factory_reset"
	filepb "github.com/openconfig/gnoi/file"
	ospb "github.com/openconfig/gnoi/os"
	gnoifile "github.com/sonic-net/sonic-gnmi/pkg/gnoi/file"
	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"
	"github.com/sonic-net/sonic-gnmi/swsscommon"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

type writeAuthorizationPutStream struct {
	filepb.File_PutServer
	ctx context.Context
}

func (s *writeAuthorizationPutStream) Context() context.Context {
	return s.ctx
}

type writeAuthorizationInstallStream struct {
	ospb.OS_InstallServer
	ctx       context.Context
	requests  []*ospb.InstallRequest
	recvIndex int
}

func (s *writeAuthorizationInstallStream) Context() context.Context {
	return s.ctx
}

func (s *writeAuthorizationInstallStream) Recv() (*ospb.InstallRequest, error) {
	if s.recvIndex < len(s.requests) {
		req := s.requests[s.recvIndex]
		s.recvIndex++
		return req, nil
	}
	return nil, io.EOF
}

func (s *writeAuthorizationInstallStream) Send(*ospb.InstallResponse) error {
	return nil
}

type writeAuthorizationOSBackend struct {
	calls int
}

func (b *writeAuthorizationOSBackend) InstallOS(string) (string, error) {
	b.calls++
	resp := &ospb.InstallResponse{}
	if b.calls == 1 {
		resp.Response = &ospb.InstallResponse_TransferReady{}
	} else {
		resp.Response = &ospb.InstallResponse_Validated{}
	}
	data, err := protojson.Marshal(resp)
	return string(data), err
}

type writeAuthorizationFactoryResetBackend struct {
	ssc.FakeClient
	calls int
}

func (b *writeAuthorizationFactoryResetBackend) FactoryReset(string) (string, error) {
	b.calls++
	return `{"reset_success":{}}`, nil
}

func writeAuthorizationContext(commonName string) context.Context {
	cert := &x509.Certificate{
		Subject: pkix.Name{CommonName: commonName},
	}
	return peer.NewContext(context.Background(), &peer.Peer{
		AuthInfo: credentials.TLSInfo{
			State: tls.ConnectionState{
				VerifiedChains: [][]*x509.Certificate{{cert}},
			},
		},
	})
}

func TestGNOIWriteAuthorization(t *testing.T) {
	if !swsscommon.SonicDBConfigIsInit() {
		swsscommon.SonicDBConfigInitialize()
	}

	const tableName = "GNMI_CLIENT_CERT"
	configDB := swsscommon.NewDBConnector("CONFIG_DB", uint(0), true)
	certTable := swsscommon.NewTable(configDB, tableName)
	t.Cleanup(func() {
		configDB.Flushdb()
		swsscommon.DeleteTable(certTable)
		swsscommon.DeleteDBConnector(configDB)
	})

	cfg := &Config{
		ConfigTableName: tableName,
		UserAuth: AuthTypes{
			"password": false,
			"cert":     true,
			"jwt":      false,
		},
	}

	handlers := []struct {
		name             string
		wantSuccessCalls int
		invoke           func(context.Context) (int, error)
	}{
		{
			name:             "File.TransferToRemote",
			wantSuccessCalls: 1,
			invoke: func(ctx context.Context) (int, error) {
				backendCalls := 0
				patch := gomonkey.ApplyFunc(
					gnoifile.HandleTransferToRemote,
					func(context.Context, *filepb.TransferToRemoteRequest) (*filepb.TransferToRemoteResponse, error) {
						backendCalls++
						return &filepb.TransferToRemoteResponse{}, nil
					},
				)
				defer patch.Reset()

				srv := &FileServer{Server: &Server{config: cfg}}
				_, err := srv.TransferToRemote(ctx, &filepb.TransferToRemoteRequest{})
				return backendCalls, err
			},
		},
		{
			name:             "File.Put",
			wantSuccessCalls: 1,
			invoke: func(ctx context.Context) (int, error) {
				backendCalls := 0
				patch := gomonkey.ApplyFunc(
					gnoifile.HandlePut,
					func(filepb.File_PutServer) error {
						backendCalls++
						return nil
					},
				)
				defer patch.Reset()

				srv := &FileServer{Server: &Server{config: cfg}}
				err := srv.Put(&writeAuthorizationPutStream{ctx: ctx})
				return backendCalls, err
			},
		},
		{
			name:             "File.Remove",
			wantSuccessCalls: 1,
			invoke: func(ctx context.Context) (int, error) {
				backendCalls := 0
				patch := gomonkey.ApplyFunc(
					gnoifile.HandleFileRemove,
					func(context.Context, *filepb.RemoveRequest) (*filepb.RemoveResponse, error) {
						backendCalls++
						return &filepb.RemoveResponse{}, nil
					},
				)
				defer patch.Reset()

				srv := &FileServer{Server: &Server{config: cfg}}
				_, err := srv.Remove(ctx, &filepb.RemoveRequest{RemoteFile: "/tmp/test"})
				return backendCalls, err
			},
		},
		{
			name:             "FactoryReset.Start",
			wantSuccessCalls: 1,
			invoke: func(ctx context.Context) (int, error) {
				backend := &writeAuthorizationFactoryResetBackend{}
				patch := gomonkey.ApplyFunc(
					ssc.NewDbusClient,
					func() (ssc.Service, error) {
						return backend, nil
					},
				)
				defer patch.Reset()

				srv := &Server{config: cfg}
				_, err := srv.Start(ctx, &resetpb.StartRequest{})
				return backend.calls, err
			},
		},
		{
			name:             "OS.Install",
			wantSuccessCalls: 2,
			invoke: func(ctx context.Context) (int, error) {
				backend := &writeAuthorizationOSBackend{}
				stream := &writeAuthorizationInstallStream{
					ctx: ctx,
					requests: []*ospb.InstallRequest{
						{
							Request: &ospb.InstallRequest_TransferRequest{
								TransferRequest: &ospb.TransferRequest{Version: "test-image"},
							},
						},
						{
							Request: &ospb.InstallRequest_TransferEnd{
								TransferEnd: &ospb.TransferEnd{},
							},
						},
					},
				}
				srv := &OSServer{
					Server:  &Server{config: cfg},
					backend: backend,
					ImgDir:  t.TempDir(),
				}
				err := srv.Install(stream)
				return backend.calls, err
			},
		},
	}

	identities := []struct {
		name       string
		commonName string
		role       string
		denied     bool
		wantCode   codes.Code
	}{
		{
			name:       "MappedReadWrite",
			commonName: "gnoi-writer",
			role:       "gnoi_readwrite",
		},
		{
			name:       "MappedReadOnly",
			commonName: "gnoi-reader",
			role:       "gnoi_readonly",
			denied:     true,
		},
		{
			name:       "UnknownIdentity",
			commonName: "gnoi-unknown",
			denied:     true,
			wantCode:   codes.Unauthenticated,
		},
	}

	for _, identity := range identities {
		t.Run(identity.name, func(t *testing.T) {
			configDB.Flushdb()
			if identity.role != "" {
				certTable.Hset(identity.commonName, "role@", identity.role)
			}

			for _, handler := range handlers {
				t.Run(handler.name, func(t *testing.T) {
					backendCalls, err := handler.invoke(writeAuthorizationContext(identity.commonName))
					if !identity.denied {
						if err != nil {
							t.Fatalf("mapped gnoi_readwrite identity was denied: %v", err)
						}
						if backendCalls != handler.wantSuccessCalls {
							t.Fatalf("backend calls = %d, want %d", backendCalls, handler.wantSuccessCalls)
						}
						return
					}
					if err == nil {
						t.Fatal("request succeeded, want authorization denial")
					}
					if identity.wantCode != codes.OK && status.Code(err) != identity.wantCode {
						t.Fatalf("status code = %v, want %v: %v", status.Code(err), identity.wantCode, err)
					}
					if backendCalls != 0 {
						t.Fatalf("backend calls after denial = %d, want 0", backendCalls)
					}
				})
			}
		})
	}
}
