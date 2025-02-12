package os

import (
	"context"
	"encoding/json"
	"fmt"
	pb "github.com/openconfig/gnoi/os"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/config"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/utils"
	"google.golang.org/grpc"
)

func Verify(conn *grpc.ClientConn, ctx context.Context) {
	fmt.Println("OS Verify")
	ctx = utils.SetUserCreds(ctx)
	osc := pb.NewOSClient(conn)
	resp, err := osc.Verify(ctx, new(pb.VerifyRequest))
	if err != nil {
		panic(err.Error())
	}
	respstr, err := json.Marshal(resp)
	if err != nil {
		panic(err.Error())
	}
	fmt.Println(string(respstr))
}

func Activate(conn *grpc.ClientConn, ctx context.Context) {
	fmt.Println("OS Activate")
	ctx = utils.SetUserCreds(ctx)
	osc := pb.NewOSClient(conn)
	req := &pb.ActivateRequest{}
	err := json.Unmarshal([]byte(*config.Args), req)
	if err != nil {
		panic("Failed to unmarshal JSON: " + err.Error())
	}
	resp, err := osc.Activate(ctx, req)
	if err != nil {
		panic("Failed to activate OS." + err.Error())
	}
	respstr, err := json.Marshal(resp)
	if err != nil {
		panic("Failed to marshal reponse: " + err.Error())
	}
	fmt.Println(string(respstr))
}
