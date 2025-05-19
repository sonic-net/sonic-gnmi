package sonic

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sonic-net/sonic-gnmi/gnoi_client/utils"
	pb "github.com/sonic-net/sonic-gnmi/proto/gnoi"
	"google.golang.org/grpc"
)

// UpdateFirmwareClient calls the UpdateFirmware RPC and prints all responses.
func UpdateFirmwareClient(conn *grpc.ClientConn, ctx context.Context, req *pb.UpdateFirmwareRequest) {
	ctx = utils.SetUserCreds(ctx)
	sc := pb.NewSonicUpgradeServiceClient(conn)
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
