package dpuproxy

import (
	"context"
	"io"
	"strings"
	"testing"

	gnoi_file_pb "github.com/openconfig/gnoi/file"
	"github.com/openconfig/gnoi/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type fakeFileGetClient struct {
	grpc.ClientStream
	responses []*gnoi_file_pb.GetResponse
	index     int
}

func (c *fakeFileGetClient) Recv() (*gnoi_file_pb.GetResponse, error) {
	if c.index >= len(c.responses) {
		return nil, io.EOF
	}
	response := c.responses[c.index]
	c.index++
	return response, nil
}

type fileGetClientStub struct {
	gnoi_file_pb.FileClient
	request *gnoi_file_pb.GetRequest
	stream  gnoi_file_pb.File_GetClient
}

func (c *fileGetClientStub) Get(_ context.Context, request *gnoi_file_pb.GetRequest, _ ...grpc.CallOption) (gnoi_file_pb.File_GetClient, error) {
	c.request = request
	return c.stream, nil
}

func TestDPUProxyForwardFileGetStream(t *testing.T) {
	oldNewFileClient := newFileClient
	client := &fileGetClientStub{stream: &fakeFileGetClient{responses: []*gnoi_file_pb.GetResponse{
		{Response: &gnoi_file_pb.GetResponse_Contents{Contents: []byte("status")}},
		{Response: &gnoi_file_pb.GetResponse_Hash{Hash: &types.HashType{Method: types.HashType_MD5, Hash: make([]byte, 16)}}},
	}}}
	newFileClient = func(grpc.ClientConnInterface) gnoi_file_pb.FileClient { return client }
	t.Cleanup(func() { newFileClient = oldNewFileClient })

	var sent []*gnoi_file_pb.GetResponse
	stream := &mockServerStreamForProxy{
		ctx:      context.Background(),
		maxRecvs: 1,
		recvMsgFunc: func(message interface{}) error {
			message.(*gnoi_file_pb.GetRequest).RemoteFile = "/tmp/dpu-digest"
			return nil
		},
		sendMsgFunc: func(message interface{}) error {
			sent = append(sent, message.(*gnoi_file_pb.GetResponse))
			return nil
		},
	}

	proxy := NewDPUProxy(nil)
	if err := proxy.forwardFileGetStream(context.Background(), nil, stream); err != nil {
		t.Fatalf("forwardFileGetStream() error = %v", err)
	}
	if client.request.GetRemoteFile() != "/tmp/dpu-digest" {
		t.Fatalf("forwarded path = %q", client.request.GetRemoteFile())
	}
	if len(sent) != 2 || string(sent[0].GetContents()) != "status" || sent[1].GetHash() == nil {
		t.Fatalf("forwarded responses = %+v", sent)
	}
}

func TestDPUFileGetRegistered(t *testing.T) {
	proxy := NewDPUProxy(nil)
	mode, found := proxy.getForwardingMode("/gnoi.file.File/Get")
	if !found || mode != ForwardToDPU {
		t.Fatalf("File.Get mode = %q found=%t, want forward", mode, found)
	}
}

func TestDPUFileGetAuthorizationRunsBeforeResolver(t *testing.T) {
	ctx := context.Background()
	ctx = metadata.NewIncomingContext(ctx, metadata.Pairs(
		MetadataKeyTargetType, TargetTypeDPU,
		MetadataKeyTargetIndex, "0",
	))
	authorized := false
	handlerCalled := false
	var authorizedMethod string
	proxy := NewDPUProxy(nil, func(_ context.Context, method string) error {
		authorized = true
		authorizedMethod = method
		return status.Error(codes.PermissionDenied, "denied")
	})
	err := proxy.StreamInterceptor()(nil, &mockServerStream{ctx: ctx}, &grpc.StreamServerInfo{
		FullMethod: "/gnoi.file.File/Get",
	}, func(interface{}, grpc.ServerStream) error {
		handlerCalled = true
		return nil
	})
	if status.Code(err) != codes.PermissionDenied || handlerCalled || !authorized || authorizedMethod != "/gnoi.file.File/Get" {
		t.Fatalf("code=%v handlerCalled=%t authorized=%t method=%q", status.Code(err), handlerCalled, authorized, authorizedMethod)
	}
}

func TestDPUFileGetAuthorizedRequestThenReachesResolver(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		MetadataKeyTargetType, TargetTypeDPU,
		MetadataKeyTargetIndex, "0",
	))
	authorized := false
	handlerCalled := false
	proxy := NewDPUProxy(nil, func(context.Context, string) error {
		authorized = true
		return nil
	})
	err := proxy.StreamInterceptor()(nil, &mockServerStream{ctx: ctx}, &grpc.StreamServerInfo{
		FullMethod: "/gnoi.file.File/Get",
	}, func(interface{}, grpc.ServerStream) error {
		handlerCalled = true
		return nil
	})
	if !authorized || !handlerCalled || err != nil {
		t.Fatalf("authorized=%t handlerCalled=%t error=%v", authorized, handlerCalled, err)
	}
}

func TestEveryDPUForwardableMethodAuthorizesBeforeResolver(t *testing.T) {
	for _, method := range defaultForwardableMethods {
		if strings.HasPrefix(method.FullMethod, "/grpc.reflection.") {
			continue
		}
		t.Run(method.FullMethod, func(t *testing.T) {
			ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
				MetadataKeyTargetType, TargetTypeDPU,
				MetadataKeyTargetIndex, "0",
			))
			authorized := false
			proxy := NewDPUProxy(nil, func(_ context.Context, gotMethod string) error {
				authorized = true
				if gotMethod != method.FullMethod {
					t.Fatalf("authorized method = %q, want %q", gotMethod, method.FullMethod)
				}
				return status.Error(codes.PermissionDenied, "denied")
			})
			if method.FullMethod == "/gnoi.system.System/Time" || method.FullMethod == "/gnoi.os.OS/Verify" || method.FullMethod == "/gnoi.os.OS/Activate" || method.FullMethod == "/gnoi.system.System/Reboot" || method.FullMethod == "/gnoi.file.File/TransferToRemote" {
				handlerCalled := false
				_, err := proxy.UnaryInterceptor()(ctx, "request", &grpc.UnaryServerInfo{FullMethod: method.FullMethod}, func(context.Context, interface{}) (interface{}, error) {
					handlerCalled = true
					return nil, nil
				})
				if status.Code(err) != codes.PermissionDenied || handlerCalled || !authorized {
					t.Fatalf("code=%v handler=%t authorized=%t", status.Code(err), handlerCalled, authorized)
				}
				return
			}
			handlerCalled := false
			err := proxy.StreamInterceptor()(nil, &mockServerStream{ctx: ctx}, &grpc.StreamServerInfo{FullMethod: method.FullMethod}, func(interface{}, grpc.ServerStream) error {
				handlerCalled = true
				return nil
			})
			if status.Code(err) != codes.PermissionDenied || handlerCalled || !authorized {
				t.Fatalf("code=%v handler=%t authorized=%t", status.Code(err), handlerCalled, authorized)
			}
		})
	}
}

func TestDPUFileGetWithoutAuthorizerFailsClosed(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		MetadataKeyTargetType, TargetTypeDPU,
		MetadataKeyTargetIndex, "0",
	))
	err := NewDPUProxy(nil).StreamInterceptor()(nil, &mockServerStream{ctx: ctx}, &grpc.StreamServerInfo{
		FullMethod: "/gnoi.file.File/Get",
	}, func(interface{}, grpc.ServerStream) error { return nil })
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("code=%v, want PermissionDenied", status.Code(err))
	}
}

func TestEveryDPUForwardableMethodWithoutAuthorizerFailsClosed(t *testing.T) {
	for _, method := range defaultForwardableMethods {
		if strings.HasPrefix(method.FullMethod, "/grpc.reflection.") {
			continue
		}
		t.Run(method.FullMethod, func(t *testing.T) {
			ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
				MetadataKeyTargetType, TargetTypeDPU,
				MetadataKeyTargetIndex, "0",
			))
			if method.FullMethod == "/gnoi.system.System/Time" || method.FullMethod == "/gnoi.os.OS/Verify" || method.FullMethod == "/gnoi.os.OS/Activate" || method.FullMethod == "/gnoi.system.System/Reboot" || method.FullMethod == "/gnoi.file.File/TransferToRemote" {
				_, err := NewDPUProxy(nil).UnaryInterceptor()(ctx, "request", &grpc.UnaryServerInfo{FullMethod: method.FullMethod}, func(context.Context, interface{}) (interface{}, error) {
					t.Fatal("handler called before authorization")
					return nil, nil
				})
				if status.Code(err) != codes.PermissionDenied {
					t.Fatalf("code=%v", status.Code(err))
				}
				return
			}
			err := NewDPUProxy(nil).StreamInterceptor()(nil, &mockServerStream{ctx: ctx}, &grpc.StreamServerInfo{FullMethod: method.FullMethod}, func(interface{}, grpc.ServerStream) error {
				t.Fatal("handler called before authorization")
				return nil
			})
			if status.Code(err) != codes.PermissionDenied {
				t.Fatalf("code=%v", status.Code(err))
			}
		})
	}
}

func TestDPUFileGetUnaryAuthorizationFailsClosed(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		MetadataKeyTargetType, TargetTypeDPU,
		MetadataKeyTargetIndex, "0",
	))
	handlerCalled := false
	_, err := NewDPUProxy(nil).UnaryInterceptor()(ctx, "request", &grpc.UnaryServerInfo{
		FullMethod: "/gnoi.file.File/Get",
	}, func(context.Context, interface{}) (interface{}, error) {
		handlerCalled = true
		return nil, nil
	})
	if status.Code(err) != codes.PermissionDenied || handlerCalled {
		t.Fatalf("code=%v handlerCalled=%t", status.Code(err), handlerCalled)
	}
}

func TestDPUFileGetDoesNotRegressOtherMethods(t *testing.T) {
	proxy := NewDPUProxy(nil)
	for method, want := range map[string]ForwardingMode{
		"/gnoi.file.File/Put":              ForwardToDPU,
		"/gnoi.file.File/TransferToRemote": HandleLocally,
		"/gnoi.system.System/Reboot":       HandleLocally,
		"/gnoi.os.OS/Verify":               ForwardToDPU,
	} {
		if got, found := proxy.getForwardingMode(method); !found || got != want {
			t.Errorf("%s mode = %q found=%t, want %q", method, got, found, want)
		}
	}
}
