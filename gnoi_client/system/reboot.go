package system

import (
	"context"
	"fmt"
	"encoding/json"
	pb 	"github.com/openconfig/gnoi/system"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/utils"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/config"
	"google.golang.org/grpc"
)

func Reboot(conn *grpc.ClientConn, ctx context.Context) {
	fmt.Println("System Reboot")
	ctx = utils.SetUserCreds(ctx)
	sc := pb.NewSystemClient(conn)
	req := &pb.RebootRequest{}
	json.Unmarshal([]byte(*config.Args), req)
	_, err := sc.Reboot(ctx, req)
	if err != nil {
		panic(err.Error())
	}
}

func CancelReboot(conn *grpc.ClientConn, ctx context.Context) {
	fmt.Println("System CancelReboot")
	ctx = utils.SetUserCreds(ctx)
	sc := pb.NewSystemClient(conn)
	req := &pb.CancelRebootRequest{}
	json.Unmarshal([]byte(*config.Args), req)
	resp, err := sc.CancelReboot(ctx, req)
	if err != nil {
		panic(err.Error())
	}
	respstr, err := json.Marshal(resp)
	if err != nil {
		panic(err.Error())
	}
	fmt.Println(string(respstr))
}

func RebootStatus(conn *grpc.ClientConn, ctx context.Context) {
	fmt.Println("System RebootStatus")
	ctx = utils.SetUserCreds(ctx)
	sc := pb.NewSystemClient(conn)
	req := &pb.RebootStatusRequest{}
	resp, err := sc.RebootStatus(ctx, req)
	if err != nil {
		panic(err.Error())
	}
	respstr, err := json.Marshal(resp)
	if err != nil {
		panic(err.Error())
	}
	fmt.Println(string(respstr))
}