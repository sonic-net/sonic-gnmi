package system

import (
	"context"
	"flag"
	"fmt"
	"github.com/openconfig/gnoi/common"
	"github.com/openconfig/gnoi/system"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/utils"
	"google.golang.org/grpc"
)

var (
	// Flags for System.SetPackage
	filename = flag.String("package_filename", "", "Destination path and filename of the package")
	version  = flag.String("package_version", "", "Version of the package, i.e. vendor internal name")
	url      = flag.String("package_url", "", "URL to download the package from")
	activate = flag.Bool("package_activate", true, "Whether to activate the package after setting it")
)

func SetPackage(conn *grpc.ClientConn, ctx context.Context) {
	fmt.Println("System SetPackage")
	ctx = utils.SetUserCreds(ctx)

	err := validateFlags()
	if err != nil {
		fmt.Println("Error validating flags: ", err)
		return
	}

	download := &common.RemoteDownload{
		Path: *url,
	}
	pkg := &system.Package{
		Filename:       *filename,
		Version:        *version,
		Activate:       *activate,
		RemoteDownload: download,
	}
	req := &system.SetPackageRequest{
		Request: &system.SetPackageRequest_Package{
			Package: pkg,
		},
	}

	sc := system.NewSystemClient(conn)
	stream, err := sc.SetPackage(ctx)
	if err != nil {
		fmt.Println("Error creating stream: ", err)
		return
	}

	// Send the package information.
	err = stream.Send(req)
	// Device should download the package, so skip the direct transfer and checksum.

	err = stream.CloseSend()
	if err != nil {
		fmt.Println("Error closing stream: ", err)
		return
	}

	// Receive the response.
	resp, err := stream.CloseAndRecv()
	if err != nil {
		fmt.Println("Error receiving response: ", err)
		return
	}

	fmt.Println(resp)
}

func validateFlags() error {
	if *filename == "" {
		return fmt.Errorf("missing -package_filename")
	}
	if *version == "" {
		return fmt.Errorf("missing -package_version")
	}
	if *url == "" {
		return fmt.Errorf("missing -package_url. Direct transfer is not supported yet")
	}
	if !*activate {
		// TODO: Currently, installing image will always set it to default.
		return fmt.Errorf("-package_activate=false is not yet supported")
	}
	return nil
}
