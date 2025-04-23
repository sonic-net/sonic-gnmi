package containerz

import (
	"context"
	"flag"
	"fmt"

	"github.com/openconfig/gnoi/common"
	"github.com/openconfig/gnoi/containerz"
	"github.com/openconfig/gnoi/types"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/utils"
	"google.golang.org/grpc"
)

var (
	imageName = flag.String("container_image_name", "", "Name of the container image")
	imageTag  = flag.String("container_image_tag", "", "Tag/version of the container image")
	imageURL  = flag.String("container_image_url", "", "SFTP path in the form <host>:<remote-path> (e.g., 192.0.0.1:~/hello-world.tar)")
	username  = flag.String("container_image_username", "", "Username for SFTP authentication")
	password  = flag.String("container_image_password", "", "Password for SFTP authentication")
)

// newContainerzClient is a package-level variable for testability.
var newContainerzClient = func(conn *grpc.ClientConn) containerz.ContainerzClient {
	return containerz.NewContainerzClient(conn)
}

// Deploy requests the server to download the image using SFTP.
func Deploy(conn *grpc.ClientConn, ctx context.Context) {
	fmt.Println("Containerz Deploy (SFTP download)")

	ctx = utils.SetUserCreds(ctx)

	if err := validateDeployFlags(); err != nil {
		fmt.Println("Error validating flags:", err)
		return
	}

	client := newContainerzClient(conn)
	stream, err := client.Deploy(ctx)
	if err != nil {
		fmt.Println("Error creating Deploy stream:", err)
		return
	}

	// Send only the ImageTransfer message with SFTP remote_download and credentials.
	req := &containerz.DeployRequest{
		Request: &containerz.DeployRequest_ImageTransfer{
			ImageTransfer: &containerz.ImageTransfer{
				Name: *imageName,
				Tag:  *imageTag,
				RemoteDownload: &common.RemoteDownload{
					Path:     *imageURL, // e.g., 192.0.0.1:~/hello-world.tar
					Protocol: common.RemoteDownload_SFTP,
					Credentials: &types.Credentials{
						Username: *username,
						Password: &types.Credentials_Cleartext{
							Cleartext: *password,
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

	// Close the send direction, as we are not sending any content.
	_ = stream.CloseSend()

	// Print all responses from the server.
	for {
		resp, err := stream.Recv()
		if err != nil {
			break
		}
		fmt.Printf("Received DeployResponse: %v\n", resp)
	}
}

func validateDeployFlags() error {
	if *imageName == "" {
		return fmt.Errorf("missing -container_image_name: required")
	}
	if *imageTag == "" {
		return fmt.Errorf("missing -container_image_tag: required")
	}
	if *imageURL == "" {
		return fmt.Errorf("missing -container_image_url: required (format: <host>:<remote-path>)")
	}
	if *username == "" {
		return fmt.Errorf("missing -container_image_username: required")
	}
	if *password == "" {
		return fmt.Errorf("missing -container_image_password: required")
	}
	return nil
}
