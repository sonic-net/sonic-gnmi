package os

import (
	"context"
	"fmt"
	"encoding/json"
	pb 	"github.com/openconfig/gnoi/os"
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