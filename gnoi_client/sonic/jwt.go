package sonic

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/config"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/utils"
	pb "github.com/sonic-net/sonic-gnmi/proto/gnoi/jwt"
	"google.golang.org/grpc"
)

func Authenticate(conn *grpc.ClientConn, ctx context.Context) {
	fmt.Println("Sonic Authenticate")
	ctx = utils.SetUserCreds(ctx)
	sc := pb.NewSonicJwtServiceClient(conn)
	req := &pb.AuthenticateRequest{}
	json.Unmarshal([]byte(*config.Args), req)

	resp, err := sc.Authenticate(ctx, req)
	if err != nil {
		panic(err.Error())
	}
	respstr, err := json.Marshal(resp)
	if err != nil {
		panic(err.Error())
	}
	fmt.Println(string(respstr))
}

func Refresh(conn *grpc.ClientConn, ctx context.Context) {
	fmt.Println("Sonic Refresh")
	ctx = utils.SetUserCreds(ctx)
	sc := pb.NewSonicJwtServiceClient(conn)
	req := &pb.RefreshRequest{}
	json.Unmarshal([]byte(*config.Args), req)
	resp, err := sc.Refresh(ctx, req)
	if err != nil {
		panic(err.Error())
	}
	respstr, err := json.Marshal(resp)
	if err != nil {
		panic(err.Error())
	}
	fmt.Println(string(respstr))
}
