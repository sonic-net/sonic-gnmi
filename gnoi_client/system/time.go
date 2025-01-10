package system

import (
	"context"
	"fmt"
	"encoding/json"
	pb 	"github.com/openconfig/gnoi/system"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/utils"
	"google.golang.org/grpc"
)

func Time(conn *grpc.ClientConn, ctx context.Context) {
	fmt.Println("System Time")
	ctx = utils.SetUserCreds(ctx)
	sc := pb.NewSystemClient(conn)
	resp, err := sc.Time(ctx, new(pb.TimeRequest))
	if err != nil {
		panic(err.Error())
	}
	respstr, err := json.Marshal(resp)
	if err != nil {
		panic(err.Error())
	}
	fmt.Println(string(respstr))
}