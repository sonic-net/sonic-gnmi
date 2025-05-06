package containerz

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/openconfig/gnoi/common"
	"github.com/openconfig/gnoi/containerz"
	"github.com/openconfig/gnoi/types"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/config"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/utils"
	"google.golang.org/grpc"
)

// DeployArgs holds the expected JSON structure for Deploy arguments.
type DeployArgs struct {
	Name     string `json:"name"`
	Tag      string `json:"tag"`
	Path     string `json:"path"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// newContainerzClient is a package-level variable for testability.
var newContainerzClient = func(conn *grpc.ClientConn) containerz.ContainerzClient {
	return containerz.NewContainerzClient(conn)
}

// Deploy requests the server to download the image using SFTP.
func Deploy(conn *grpc.ClientConn, ctx context.Context) {
	fmt.Println("Containerz Deploy (SFTP download, JSON args)")

	ctx = utils.SetUserCreds(ctx)

	var args DeployArgs
	if err := json.Unmarshal([]byte(*config.Args), &args); err != nil {
		fmt.Println("Error parsing JSON args:", err)
		return
	}

	if err := validateDeployArgs(&args); err != nil {
		fmt.Println("Error validating args:", err)
		return
	}

	client := newContainerzClient(conn)
	stream, err := client.Deploy(ctx)
	if err != nil {
		fmt.Println("Error creating Deploy stream:", err)
		return
	}

	req := &containerz.DeployRequest{
		Request: &containerz.DeployRequest_ImageTransfer{
			ImageTransfer: &containerz.ImageTransfer{
				Name: args.Name,
				Tag:  args.Tag,
				RemoteDownload: &common.RemoteDownload{
					Path:     args.Path, // e.g., 192.0.0.1:~/hello-world.tar
					Protocol: common.RemoteDownload_SFTP,
					Credentials: &types.Credentials{
						Username: args.Username,
						Password: &types.Credentials_Cleartext{
							Cleartext: args.Password,
						},
					},
				},
			},
		},
	}

	if err := stream.Send(req); err != nil {
		fmt.Println("Error sending ImageTransfer:", err)
		return
	}

	_ = stream.CloseSend()

	for {
		resp, err := stream.Recv()
		if err != nil {
			fmt.Println("Error receiving DeployResponse:", err)
			break
		}
		switch r := resp.Response.(type) {
		case *containerz.DeployResponse_ImageTransferReady:
			fmt.Printf("ImageTransferReady: chunk_size=%d\n", r.ImageTransferReady.ChunkSize)
		case *containerz.DeployResponse_ImageTransferProgress:
			fmt.Printf("ImageTransferProgress: bytes_received=%d\n", r.ImageTransferProgress.BytesReceived)
		case *containerz.DeployResponse_ImageTransferSuccess:
			fmt.Printf("ImageTransferSuccess: name=%s, tag=%s, image_size=%d\n",
				r.ImageTransferSuccess.Name, r.ImageTransferSuccess.Tag, r.ImageTransferSuccess.ImageSize)
			return // Quit on success
		case *containerz.DeployResponse_ImageTransferError:
			fmt.Printf("ImageTransferError: %v\n", r.ImageTransferError)
			return
		default:
			fmt.Printf("Unknown DeployResponse: %v\n", resp)
		}
	}
}

func validateDeployArgs(args *DeployArgs) error {
	if args.Name == "" {
		return fmt.Errorf("missing name")
	}
	if args.Tag == "" {
		return fmt.Errorf("missing tag")
	}
	if args.Path == "" {
		return fmt.Errorf("missing path (format: <host>:<remote-path>)")
	}
	if args.Username == "" {
		return fmt.Errorf("missing username")
	}
	if args.Password == "" {
		return fmt.Errorf("missing password")
	}
	return nil
}
