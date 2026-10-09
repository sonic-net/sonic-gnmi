package file

import (
	"bytes"
	"context"
	"crypto/md5"
	"io"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/openconfig/gnoi/common"
	gnoi_file_pb "github.com/openconfig/gnoi/file"
	"github.com/openconfig/gnoi/types"
	"github.com/sonic-net/sonic-gnmi/internal/download"
	"github.com/sonic-net/sonic-gnmi/pkg/interceptors/dpuproxy"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestDPUStreamingWorkflow(t *testing.T) {
	payload := bytes.Repeat([]byte("dpu-stream"), 8*1024)
	put := &recordingPutClient{}
	patches := gomonkey.NewPatches()
	defer patches.Reset()
	patches.ApplyFunc(download.DownloadHTTPStreaming, func(context.Context, string, int64) (io.ReadCloser, int64, error) {
		return io.NopCloser(bytes.NewReader(payload)), int64(len(payload)), nil
	})
	patches.ApplyFunc(dpuproxy.GetDPUConnection, func(context.Context, string) (*grpc.ClientConn, error) {
		return &grpc.ClientConn{}, nil
	})
	patches.ApplyGlobalVar(&newFileClient, func(grpc.ClientConnInterface) gnoi_file_pb.FileClient {
		return &recordingFileClient{put: put}
	})

	req := &gnoi_file_pb.TransferToRemoteRequest{
		LocalPath: "/tmp/dpu.bin",
		RemoteDownload: &common.RemoteDownload{
			Path:     "http://example.test/dpu.bin",
			Protocol: common.RemoteDownload_HTTP,
		},
	}
	resp, err := HandleTransferToRemoteForDPUStreaming(context.Background(), req, "2")
	if err != nil {
		t.Fatalf("HandleTransferToRemoteForDPUStreaming: %v", err)
	}
	wantHash := md5.Sum(payload)
	if resp.Hash.Method != types.HashType_MD5 || !bytes.Equal(resp.Hash.Hash, wantHash[:]) {
		t.Fatalf("response hash = %v, want MD5 %x", resp.Hash, wantHash)
	}
	if len(put.requests) < 4 {
		t.Fatalf("got %d protocol frames, want open, multiple contents, and hash", len(put.requests))
	}
	if open := put.requests[0].GetOpen(); open == nil || open.RemoteFile != req.LocalPath || open.Permissions != 0644 {
		t.Fatalf("first frame = %v, want Open for %q", put.requests[0], req.LocalPath)
	}
	var got []byte
	for _, frame := range put.requests[1 : len(put.requests)-1] {
		chunk := frame.GetContents()
		if chunk == nil || len(chunk) > 64*1024 {
			t.Fatalf("content frame has invalid size %d", len(chunk))
		}
		got = append(got, chunk...)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("streamed payload differs: got %d bytes, want %d", len(got), len(payload))
	}
	if trailer := put.requests[len(put.requests)-1].GetHash(); trailer == nil || !bytes.Equal(trailer.Hash, wantHash[:]) {
		t.Fatalf("final frame = %v, want matching MD5 hash", put.requests[len(put.requests)-1])
	}
}

type recordingFileClient struct {
	put *recordingPutClient
}

func (c *recordingFileClient) Put(context.Context, ...grpc.CallOption) (gnoi_file_pb.File_PutClient, error) {
	return c.put, nil
}

func (*recordingFileClient) Stat(context.Context, *gnoi_file_pb.StatRequest, ...grpc.CallOption) (*gnoi_file_pb.StatResponse, error) {
	return nil, nil
}

func (*recordingFileClient) TransferToRemote(context.Context, *gnoi_file_pb.TransferToRemoteRequest, ...grpc.CallOption) (*gnoi_file_pb.TransferToRemoteResponse, error) {
	return nil, nil
}

func (*recordingFileClient) Remove(context.Context, *gnoi_file_pb.RemoveRequest, ...grpc.CallOption) (*gnoi_file_pb.RemoveResponse, error) {
	return nil, nil
}

func (*recordingFileClient) Get(context.Context, *gnoi_file_pb.GetRequest, ...grpc.CallOption) (gnoi_file_pb.File_GetClient, error) {
	return nil, nil
}

type recordingPutClient struct {
	requests []*gnoi_file_pb.PutRequest
}

func (c *recordingPutClient) Send(req *gnoi_file_pb.PutRequest) error {
	if chunk := req.GetContents(); chunk != nil {
		req = &gnoi_file_pb.PutRequest{Request: &gnoi_file_pb.PutRequest_Contents{
			Contents: append([]byte(nil), chunk...),
		}}
	}
	c.requests = append(c.requests, req)
	return nil
}

func (*recordingPutClient) CloseAndRecv() (*gnoi_file_pb.PutResponse, error) {
	return &gnoi_file_pb.PutResponse{}, nil
}

func (*recordingPutClient) Header() (metadata.MD, error) { return nil, nil }
func (*recordingPutClient) Trailer() metadata.MD         { return nil }
func (*recordingPutClient) CloseSend() error             { return nil }
func (*recordingPutClient) Context() context.Context     { return context.Background() }
func (*recordingPutClient) SendMsg(interface{}) error    { return nil }
func (*recordingPutClient) RecvMsg(interface{}) error    { return nil }
