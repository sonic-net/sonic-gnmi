package file

import (
	"context"
	"encoding/json"
	"fmt"

	pb "github.com/openconfig/gnoi/file"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/config"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/utils"
	"google.golang.org/grpc"
)

func Stat(conn *grpc.ClientConn, ctx context.Context) {
	fmt.Println("File Stat")
	ctx = utils.SetUserCreds(ctx)
	fc := pb.NewFileClient(conn)
	req := &pb.StatRequest{}
	err := json.Unmarshal([]byte(*config.Args), req)
	if err != nil {
		panic(err.Error())
	}
	resp, err := fc.Stat(ctx, req)
	if err != nil {
		panic(err.Error())
	}
	respstr, err := json.Marshal(resp)
	if err != nil {
		panic(err.Error())
	}
	fmt.Println(string(respstr))
}

func Remove(conn *grpc.ClientConn, ctx context.Context) {
	fmt.Println("File Remove")
	ctx = utils.SetUserCreds(ctx)
	fc := pb.NewFileClient(conn)
	req := &pb.RemoveRequest{}
	err := json.Unmarshal([]byte(*config.Args), req)
	if err != nil {
		panic(err.Error())
	}
	resp, err := fc.Remove(ctx, req)
	if err != nil {
		panic(err.Error())
	}
	respstr, err := json.Marshal(resp)
	if err != nil {
		panic(err.Error())
	}
	fmt.Println(string(respstr))
}
