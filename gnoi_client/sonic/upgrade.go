package sonic

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sonic-net/sonic-gnmi/gnoi_client/config"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/utils"
	pb "github.com/sonic-net/sonic-gnmi/proto/gnoi"
	"google.golang.org/grpc"
)

// UpdateFirmwareClient calls the UpdateFirmware RPC and prints all responses.
func UpdateFirmware(conn *grpc.ClientConn, ctx context.Context) {
	fmt.Println("Sonic UpdateFirmware")

	ctx = utils.SetUserCreds(ctx)
	sc := pb.NewSonicUpgradeServiceClient(conn)
	stream, err := sc.UpdateFirmware(ctx)
	if err != nil {
		fmt.Println("UpdateFirmware RPC error:", err)
		return
	}

	// Prepare the initial request
	params := &pb.FirmwareUpdateParams{}
	if err := json.Unmarshal([]byte(*config.Args), params); err != nil {
		fmt.Println("Unmarshal error:", err)
		return
	}
	request := &pb.UpdateFirmwareRequest{
		Request: &pb.UpdateFirmwareRequest_FirmwareUpdate{FirmwareUpdate: params},
	}
	if err := stream.Send(request); err != nil {
		fmt.Println("Send error:", err)
		return
	}
	// Close the send direction to indicate no more requests
	if err := stream.CloseSend(); err != nil {
		fmt.Println("CloseSend error:", err)
		return
	}

	for {
		status, err := stream.Recv()
		if err != nil {
			break // End of stream or error
		}
		respstr, err := json.Marshal(status)
		if err != nil {
			fmt.Println("Marshal error:", err)
			return
		}
		fmt.Println(string(respstr))
	}
}
