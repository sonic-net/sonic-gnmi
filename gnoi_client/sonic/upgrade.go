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
	ctx = utils.SetUserCreds(ctx)
	sc := pb.NewSonicUpgradeServiceClient(conn)
	req := &pb.UpdateFirmwareRequest{}

	// Unmarshal the JSON args into the request
	if err := json.Unmarshal([]byte(*config.Args), req); err != nil {
		fmt.Println("Unmarshal error:", err)
		return
	}

	stream, err := sc.UpdateFirmware(ctx, req)
	if err != nil {
		fmt.Println("UpdateFirmware RPC error:", err)
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
