package gnmi

import (
	"context"
	"testing"

	"github.com/golang/mock/gomock"
	gnoi_containerz_pb "github.com/openconfig/gnoi/containerz"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type dummyDeployServer struct {
	gnoi_containerz_pb.Containerz_DeployServer
}

type dummyListServer struct {
	gnoi_containerz_pb.Containerz_ListServer
}

type dummyLogServer struct {
	gnoi_containerz_pb.Containerz_LogServer
}

func TestContainerzServer_Unimplemented(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

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
