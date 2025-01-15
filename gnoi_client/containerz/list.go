package containerz

import (
	"context"
	"fmt"
	"encoding/json"
	pb 	"github.com/openconfig/gnoi/containerz"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/utils"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/config"
	"google.golang.org/grpc"
)

func List(conn *grpc.ClientConn, ctx context.Context) {
	fmt.Println("List Containers")
	ctx = utils.SetUserCreds(ctx)
	cc := pb.NewContainerzClient(conn)
	req := &pb.ListRequest{}
	err := json.Unmarshal([]byte(*config.Args), req)
	if err != nil {
		panic(err.Error())
	}
	resp, err := cc.List(ctx, req)
	if err != nil {
		panic(err.Error())
	}
	fmt.Println(resp)
}