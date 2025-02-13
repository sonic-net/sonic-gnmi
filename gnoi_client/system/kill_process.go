package system

import (
	"context"
	"encoding/json"
	"fmt"
	pb "github.com/openconfig/gnoi/system"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/config"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/utils"
	"google.golang.org/grpc"
)

func KillProcess(conn *grpc.ClientConn, ctx context.Context) {
	fmt.Println("Kill Process with optional restart")
	ctx = utils.SetUserCreds(ctx)
	sc := pb.NewSystemClient(conn)
	req := &pb.KillProcessRequest{}
	err := json.Unmarshal([]byte(*config.Args), req)
	if err != nil {
		panic(err.Error())
	}
	_, err = sc.KillProcess(ctx, req)
	if err != nil {
		panic(err.Error())
	}
}
