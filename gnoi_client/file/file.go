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

func Get(conn *grpc.ClientConn, ctx context.Context) {
	fmt.Println("File Get")
	ctx = utils.SetUserCreds(ctx)
	fc := pb.NewFileClient(conn)
	req := &pb.GetRequest{}
	err := json.Unmarshal([]byte(*config.Args), req)
	if err != nil {
		panic(err.Error())
	}
	stream, err := fc.Get(ctx, req)
	if err != nil {
		panic(err.Error())
	}
	for {
		resp, err := stream.Recv()
		if err != nil {
			panic(err.Error())
			break
		}

		switch r := resp.Response.(type) {
		case *pb.GetResponse_Contents:
			fmt.Printf("Received file chunk: %v\n", r.Contents)
		case *pb.GetResponse_Hash:
			fmt.Printf("Received file hash: %v\n", r.Hash)
		default:
			fmt.Println("Unknown GetResponse type")
		}
	}
}

func Put(conn *grpc.ClientConn, ctx context.Context) {
	fmt.Println("File Put")
	ctx = utils.SetUserCreds(ctx)
	fc := pb.NewFileClient(conn)
	req := &pb.PutRequest{}
	err := json.Unmarshal([]byte(*config.Args), req)
	if err != nil {
		panic(err.Error())
	}
	stream, err := fc.Put(ctx)
	if err != nil {
		panic(err.Error())
	}
	err = stream.Send(req)
	if err != nil {
		panic(err.Error())
	}
	resp, err := stream.CloseAndRecv()
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

func TransferToRemote(conn *grpc.ClientConn, ctx context.Context) {
	fmt.Println("File TransferToRemote")
	ctx = utils.SetUserCreds(ctx)
	fc := pb.NewFileClient(conn)
	req := &pb.TransferToRemoteRequest{}
	err := json.Unmarshal([]byte(*config.Args), req)
	if err != nil {
		panic(err.Error())
	}
	resp, err := fc.TransferToRemote(ctx, req)
	if err != nil {
		panic(err.Error())
	}
	respstr, err := json.Marshal(resp)
	if err != nil {
		panic(err.Error())
	}
	fmt.Println(string(respstr))
}
