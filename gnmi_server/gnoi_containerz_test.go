package gnmi

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	gnoi_common_pb "github.com/openconfig/gnoi/common"
	gnoi_containerz_pb "github.com/openconfig/gnoi/containerz"
	gnoi_types_pb "github.com/openconfig/gnoi/types"
	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// dummyDeployServer implements Containerz_DeployServer for testing
type dummyDeployServer struct {
	gnoi_containerz_pb.Containerz_DeployServer
	recvQueue []*gnoi_containerz_pb.DeployRequest
	sendResp  []*gnoi_containerz_pb.DeployResponse
	sendErr   error
	recvErr   error
}

func (d *dummyDeployServer) Recv() (*gnoi_containerz_pb.DeployRequest, error) {
	if d.recvErr != nil {
		return nil, d.recvErr
	}
	if len(d.recvQueue) == 0 {
		return nil, io.EOF
	}
	req := d.recvQueue[0]
	d.recvQueue = d.recvQueue[1:]
	return req, nil
}

func (d *dummyDeployServer) Send(resp *gnoi_containerz_pb.DeployResponse) error {
	d.sendResp = append(d.sendResp, resp)
	return d.sendErr
}

func (d *dummyDeployServer) Context() context.Context {
	return context.Background()
}

type dummyListServer struct {
	gnoi_containerz_pb.Containerz_ListServer
}

type dummyLogServer struct {
	gnoi_containerz_pb.Containerz_LogServer
}

func TestContainerzServer_Unimplemented(t *testing.T) {
	server := &ContainerzServer{}

	// Test Remove
	_, err := server.Remove(context.Background(), &gnoi_containerz_pb.RemoveRequest{})
	if err == nil || status.Code(err) != codes.Unimplemented {
		t.Errorf("Remove: expected Unimplemented error, got %v", err)
	}

	// Test List
	err = server.List(&gnoi_containerz_pb.ListRequest{}, &dummyListServer{})
	if err == nil || status.Code(err) != codes.Unimplemented {
		t.Errorf("List: expected Unimplemented error, got %v", err)
	}

	// Test Start
	_, err = server.Start(context.Background(), &gnoi_containerz_pb.StartRequest{})
	if err == nil || status.Code(err) != codes.Unimplemented {
		t.Errorf("Start: expected Unimplemented error, got %v", err)
	}

	// Test Stop
	_, err = server.Stop(context.Background(), &gnoi_containerz_pb.StopRequest{})
	if err == nil || status.Code(err) != codes.Unimplemented {
		t.Errorf("Stop: expected Unimplemented error, got %v", err)
	}

	// Test Log
	err = server.Log(&gnoi_containerz_pb.LogRequest{}, &dummyLogServer{})
	if err == nil || status.Code(err) != codes.Unimplemented {
		t.Errorf("Log: expected Unimplemented error, got %v", err)
	}
}

func newServer() *ContainerzServer {
	return &ContainerzServer{
		server: &Server{
			config: &Config{},
		},
	}
}

func TestDeploy_Success(t *testing.T) {
    patches := gomonkey.NewPatches()
    defer patches.Reset()

    patches.ApplyFunc(authenticate, func(_ *Config, ctx context.Context, _ string, _ bool) (context.Context, error) {
        return ctx, nil
    })
    patches.ApplyMethod(reflect.TypeOf(&ssc.DbusClient{}), "DownloadFile", func(_ *ssc.DbusClient, host, user, pass, remote, local, proto string) error {
        return nil
    })
    patches.ApplyMethod(reflect.TypeOf(&ssc.DbusClient{}), "LoadDockerImage", func(_ *ssc.DbusClient, image string) error {
        return nil
    })
    patches.ApplyMethod(reflect.TypeOf(&ssc.DbusClient{}), "RemoveFile", func(_ *ssc.DbusClient, path string) error {
        return nil
    })

    server := newServer()
    stream := &dummyDeployServer{
        recvQueue: []*gnoi_containerz_pb.DeployRequest{
            {
                Request: &gnoi_containerz_pb.DeployRequest_ImageTransfer{
                    ImageTransfer: &gnoi_containerz_pb.ImageTransfer{
                        Name: "testimg",
                        Tag:  "latest",
                        RemoteDownload: &gnoi_common_pb.RemoteDownload{
                            Path:     "host:/remote.tar",
                            Protocol: gnoi_common_pb.RemoteDownload_SFTP,
                            Credentials: &gnoi_types_pb.Credentials{
                                Username: "user",
                                Password: &gnoi_types_pb.Credentials_Cleartext{Cleartext: "pass"},
                            },
                        },
                    },
                },
            },
        },
    }

    err := server.Deploy(stream)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if len(stream.sendResp) != 1 {
        t.Fatalf("expected 1 response, got %d", len(stream.sendResp))
    }
    if _, ok := stream.sendResp[0].Response.(*gnoi_containerz_pb.DeployResponse_ImageTransferSuccess); !ok {
        t.Errorf("expected ImageTransferSuccess, got %T", stream.sendResp[0].Response)
    }
}

func TestDeploy_DownloadFileError(t *testing.T) {
    patches := gomonkey.NewPatches()
    defer patches.Reset()

    patches.ApplyFunc(authenticate, func(_ *Config, ctx context.Context, _ string, _ bool) (context.Context, error) {
        return ctx, nil
    })
    patches.ApplyMethod(reflect.TypeOf(&ssc.DbusClient{}), "DownloadFile", func(_ *ssc.DbusClient, host, user, pass, remote, local, proto string) error {
        return errors.New("dbus error")
    })
    patches.ApplyMethod(reflect.TypeOf(&ssc.DbusClient{}), "LoadDockerImage", func(_ *ssc.DbusClient, image string) error {
        return nil
    })
    patches.ApplyMethod(reflect.TypeOf(&ssc.DbusClient{}), "RemoveFile", func(_ *ssc.DbusClient, path string) error {
        return nil
    })

    server := newServer()
    stream := &dummyDeployServer{
        recvQueue: []*gnoi_containerz_pb.DeployRequest{
            {
                Request: &gnoi_containerz_pb.DeployRequest_ImageTransfer{
                    ImageTransfer: &gnoi_containerz_pb.ImageTransfer{
                        Name: "testimg",
                        Tag:  "latest",
                        RemoteDownload: &gnoi_common_pb.RemoteDownload{
                            Path:     "host:/remote.tar",
                            Protocol: gnoi_common_pb.RemoteDownload_SFTP,
                            Credentials: &gnoi_types_pb.Credentials{
                                Username: "user",
                                Password: &gnoi_types_pb.Credentials_Cleartext{Cleartext: "pass"},
                            },
                        },
                    },
                },
            },
        },
    }

    err := server.Deploy(stream)
    if err == nil || !strings.Contains(err.Error(), "dbus error") {
        t.Errorf("expected dbus error, got %v", err)
    }
    st, _ := status.FromError(err)
    if st.Code() != codes.Internal {
        t.Errorf("expected code %v, got %v", codes.Internal, st.Code())
    }
}

func TestDeploy_InvalidPath(t *testing.T) {
    patches := gomonkey.NewPatches()
    defer patches.Reset()

    patches.ApplyFunc(authenticate, func(_ *Config, ctx context.Context, _ string, _ bool) (context.Context, error) {
        return ctx, nil
    })
    patches.ApplyMethod(reflect.TypeOf(&ssc.DbusClient{}), "DownloadFile", func(_ *ssc.DbusClient, host, user, pass, remote, local, proto string) error {
        return nil
    })
    patches.ApplyMethod(reflect.TypeOf(&ssc.DbusClient{}), "LoadDockerImage", func(_ *ssc.DbusClient, image string) error {
        return nil
    })
    patches.ApplyMethod(reflect.TypeOf(&ssc.DbusClient{}), "RemoveFile", func(_ *ssc.DbusClient, path string) error {
        return nil
    })

    server := newServer()
    stream := &dummyDeployServer{
        recvQueue: []*gnoi_containerz_pb.DeployRequest{
            {
                Request: &gnoi_containerz_pb.DeployRequest_ImageTransfer{
                    ImageTransfer: &gnoi_containerz_pb.ImageTransfer{
                        Name: "testimg",
                        Tag:  "latest",
                        RemoteDownload: &gnoi_common_pb.RemoteDownload{
                            Path:     "invalidpath",
                            Protocol: gnoi_common_pb.RemoteDownload_SFTP,
                        },
                    },
                },
            },
        },
    }

    err := server.Deploy(stream)
    if err == nil || !strings.Contains(err.Error(), "invalid remote download path") {
        t.Errorf("expected invalid remote download path error, got %v", err)
    }
    st, _ := status.FromError(err)
    if st.Code() != codes.InvalidArgument {
        t.Errorf("expected code %v, got %v", codes.InvalidArgument, st.Code())
    }
}

func TestDeploy_RecvError(t *testing.T) {
    patches := gomonkey.NewPatches()
    defer patches.Reset()

    patches.ApplyFunc(authenticate, func(_ *Config, ctx context.Context, _ string, _ bool) (context.Context, error) {
        return ctx, nil
    })
    patches.ApplyMethod(reflect.TypeOf(&ssc.DbusClient{}), "DownloadFile", func(_ *ssc.DbusClient, host, user, pass, remote, local, proto string) error {
        return nil
    })
    patches.ApplyMethod(reflect.TypeOf(&ssc.DbusClient{}), "LoadDockerImage", func(_ *ssc.DbusClient, image string) error {
        return nil
    })
    patches.ApplyMethod(reflect.TypeOf(&ssc.DbusClient{}), "RemoveFile", func(_ *ssc.DbusClient, path string) error {
        return nil
    })

    server := newServer()
    stream := &dummyDeployServer{
        recvErr: errors.New("recv failed"),
    }

    err := server.Deploy(stream)
    if err == nil || !strings.Contains(err.Error(), "recv failed") {
        t.Errorf("expected recv failed error, got %v", err)
    }
    st, _ := status.FromError(err)
    if st.Code() != codes.InvalidArgument {
        t.Errorf("expected code %v, got %v", codes.InvalidArgument, st.Code())
    }
}

func TestDeploy_SendError(t *testing.T) {
    patches := gomonkey.NewPatches()
    defer patches.Reset()

    patches.ApplyFunc(authenticate, func(_ *Config, ctx context.Context, _ string, _ bool) (context.Context, error) {
        return ctx, nil
    })
    patches.ApplyMethod(reflect.TypeOf(&ssc.DbusClient{}), "DownloadFile", func(_ *ssc.DbusClient, host, user, pass, remote, local, proto string) error {
        return nil
    })
    patches.ApplyMethod(reflect.TypeOf(&ssc.DbusClient{}), "LoadDockerImage", func(_ *ssc.DbusClient, image string) error {
        return nil
    })
    patches.ApplyMethod(reflect.TypeOf(&ssc.DbusClient{}), "RemoveFile", func(_ *ssc.DbusClient, path string) error {
        return nil
    })

    server := newServer()
    stream := &dummyDeployServer{
        recvQueue: []*gnoi_containerz_pb.DeployRequest{
            {
                Request: &gnoi_containerz_pb.DeployRequest_ImageTransfer{
                    ImageTransfer: &gnoi_containerz_pb.ImageTransfer{
                        Name: "testimg",
                        Tag:  "latest",
                        RemoteDownload: &gnoi_common_pb.RemoteDownload{
                            Path:     "host:/remote.tar",
                            Protocol: gnoi_common_pb.RemoteDownload_SFTP,
                            Credentials: &gnoi_types_pb.Credentials{
                                Username: "user",
                                Password: &gnoi_types_pb.Credentials_Cleartext{Cleartext: "pass"},
                            },
                        },
                    },
                },
            },
        },
        sendErr: errors.New("send failed"),
    }

    err := server.Deploy(stream)
    if err == nil || !strings.Contains(err.Error(), "send failed") {
        t.Errorf("expected send failed error, got %v", err)
    }
    st, _ := status.FromError(err)
    if st.Code() != codes.Internal {
        t.Errorf("expected code %v, got %v", codes.Internal, st.Code())
    }
}

func TestDeploy_FirstMessageWrongType(t *testing.T) {
    patches := gomonkey.NewPatches()
    defer patches.Reset()

    patches.ApplyFunc(authenticate, func(_ *Config, ctx context.Context, _ string, _ bool) (context.Context, error) {
        return ctx, nil
    })
    patches.ApplyMethod(reflect.TypeOf(&ssc.DbusClient{}), "DownloadFile", func(_ *ssc.DbusClient, host, user, pass, remote, local, proto string) error {
        return nil
    })
    patches.ApplyMethod(reflect.TypeOf(&ssc.DbusClient{}), "LoadDockerImage", func(_ *ssc.DbusClient, image string) error {
        return nil
    })
    patches.ApplyMethod(reflect.TypeOf(&ssc.DbusClient{}), "RemoveFile", func(_ *ssc.DbusClient, path string) error {
        return nil
    })

    server := newServer()
    stream := &dummyDeployServer{
        recvQueue: []*gnoi_containerz_pb.DeployRequest{
            {
                Request: &gnoi_containerz_pb.DeployRequest_Content{
                    Content: []byte("not an image transfer"),
                },
            },
        },
    }

    err := server.Deploy(stream)
    if err == nil || !strings.Contains(err.Error(), "first DeployRequest must be ImageTransfer") {
        t.Errorf("expected error for wrong first message type, got %v", err)
    }
    st, _ := status.FromError(err)
    if st.Code() != codes.InvalidArgument {
        t.Errorf("expected code %v, got %v", codes.InvalidArgument, st.Code())
    }
}

func TestDeploy_LoadDockerImageError(t *testing.T) {
    patches := gomonkey.NewPatches()
    defer patches.Reset()

    patches.ApplyFunc(authenticate, func(_ *Config, ctx context.Context, _ string, _ bool) (context.Context, error) {
        return ctx, nil
    })
    patches.ApplyMethod(reflect.TypeOf(&ssc.DbusClient{}), "DownloadFile", func(_ *ssc.DbusClient, host, user, pass, remote, local, proto string) error {
        return nil
    })
    patches.ApplyMethod(reflect.TypeOf(&ssc.DbusClient{}), "LoadDockerImage", func(_ *ssc.DbusClient, image string) error {
        return errors.New("load docker image error")
    })

    server := newServer()
    stream := &dummyDeployServer{
        recvQueue: []*gnoi_containerz_pb.DeployRequest{
            {
                Request: &gnoi_containerz_pb.DeployRequest_ImageTransfer{
                    ImageTransfer: &gnoi_containerz_pb.ImageTransfer{
                        Name: "testimg",
                        Tag:  "latest",
                        RemoteDownload: &gnoi_common_pb.RemoteDownload{
                            Path:     "host:/remote.tar",
                            Protocol: gnoi_common_pb.RemoteDownload_SFTP,
                            Credentials: &gnoi_types_pb.Credentials{
                                Username: "user",
                                Password: &gnoi_types_pb.Credentials_Cleartext{Cleartext: "pass"},
                            },
                        },
                    },
                },
            },
        },
    }

    err := server.Deploy(stream)
    if err == nil || !strings.Contains(err.Error(), "load docker image error") {
        t.Errorf("expected load docker image error, got %v", err)
    }
    st, _ := status.FromError(err)
    if st.Code() != codes.Internal {
        t.Errorf("expected code %v, got %v", codes.Internal, st.Code())
    }
}

func TestDeploy_RemoveFileError(t *testing.T) {
    patches := gomonkey.NewPatches()
    defer patches.Reset()

    patches.ApplyFunc(authenticate, func(_ *Config, ctx context.Context, _ string, _ bool) (context.Context, error) {
        return ctx, nil
    })
    patches.ApplyMethod(reflect.TypeOf(&ssc.DbusClient{}), "DownloadFile", func(_ *ssc.DbusClient, host, user, pass, remote, local, proto string) error {
        return nil
    })
    patches.ApplyMethod(reflect.TypeOf(&ssc.DbusClient{}), "LoadDockerImage", func(_ *ssc.DbusClient, image string) error {
        return nil
    })
    patches.ApplyMethod(reflect.TypeOf(&ssc.DbusClient{}), "RemoveFile", func(_ *ssc.DbusClient, path string) error {
        return errors.New("remove file error")
    })

    server := newServer()
    stream := &dummyDeployServer{
        recvQueue: []*gnoi_containerz_pb.DeployRequest{
            {
                Request: &gnoi_containerz_pb.DeployRequest_ImageTransfer{
                    ImageTransfer: &gnoi_containerz_pb.ImageTransfer{
                        Name: "testimg",
                        Tag:  "latest",
                        RemoteDownload: &gnoi_common_pb.RemoteDownload{
                            Path:     "host:/remote.tar",
                            Protocol: gnoi_common_pb.RemoteDownload_SFTP,
                            Credentials: &gnoi_types_pb.Credentials{
                                Username: "user",
                                Password: &gnoi_types_pb.Credentials_Cleartext{Cleartext: "pass"},
                            },
                        },
                    },
                },
            },
        },
    }

    err := server.Deploy(stream)
    if err == nil || !strings.Contains(err.Error(), "remove file error") {
        t.Errorf("expected remove file error, got %v", err)
    }
    st, _ := status.FromError(err)
    if st.Code() != codes.Internal {
        t.Errorf("expected code %v, got %v", codes.Internal, st.Code())
    }
}
