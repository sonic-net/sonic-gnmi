package os

import (
	"context"
	"encoding/json"
	"fmt"
	pb "github.com/openconfig/gnoi/os"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/config"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/utils"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
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

func Install(conn *grpc.ClientConn, ctx context.Context) {
	fmt.Println("OS Install")
	fmt.Println("From client")
	ctx = utils.SetUserCreds(ctx)
	osc := pb.NewOSClient(conn)

	// Directly unmarshal JSON input into InstallRequest (no intermediate TransferRequest)
	var req pb.InstallRequest
	err := protojson.Unmarshal([]byte(*config.Args), &req)
	if err != nil {
		panic("Failed to unmarshal InstallRequest: " + err.Error())
	}

	// Optional: Print the marshaled request for verification
	jsonBytes, err := protojson.MarshalOptions{
		Indent:    "  ",
		Multiline: true,
	}.Marshal(&req)
	if err != nil {
		fmt.Println("Failed to marshal InstallRequest:", err)
	} else {
		fmt.Println("InstallRequest (JSON):\n", string(jsonBytes))
	}

	// Start the Install stream
	stream, err := osc.Install(ctx)
	if err != nil {
		panic("Failed to start Install stream: " + err.Error())
	}

	// Send the request
	if err := stream.Send(&req); err != nil {
		panic("Failed to send InstallRequest: " + err.Error())
	}

	// Handle responses
	for {
		resp, err := stream.Recv()
		if err != nil {
			panic("Failed to receive InstallResponse: " + err.Error())
		}

		jsonBytes, err := protojson.MarshalOptions{
			Multiline: true,
			Indent:    "  ",
		}.Marshal(resp)
		if err != nil {
			fmt.Println("Failed to marshal InstallResponse to JSON:", err)
		} else {
			fmt.Println("InstallResponse (JSON):\n", string(jsonBytes))
		}

		// Handle specific response types
		switch r := resp.Response.(type) {
		case *pb.InstallResponse_TransferReady:
			fmt.Println("Transfer Ready:", r.TransferReady)
		case *pb.InstallResponse_TransferProgress:
			fmt.Println("Transfer Progress:", r.TransferProgress)
		case *pb.InstallResponse_SyncProgress:
			fmt.Println("Sync Progress:", r.SyncProgress)
		case *pb.InstallResponse_Validated:
			fmt.Println("Validated:", r.Validated)
		case *pb.InstallResponse_InstallError:
			fmt.Println("Install Error:", r.InstallError)
		default:
			fmt.Println("Unknown InstallResponse type")
		}
	}
}
