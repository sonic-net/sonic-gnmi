package system

import (
	"context"
	"log"
	"fmt"
	"flag"
	"github.com/openconfig/gnoi/system"
	"github.com/openconfig/gnoi/common"
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
	log.Println("System SetPackage")
	ctx = utils.SetUserCreds(ctx)

	err := validateFlags()
	if err != nil {
		log.Println("Error validating flags: ", err)
		return
	}

	download := &common.RemoteDownload{
		Path : *url,
	}
	pkg := &system.Package{
		Filename: *filename,
		Version:  *version,
		Activate: *activate,
		RemoteDownload: download,
	}
	req := &system.SetPackageRequest{
		Request: &system.SetPackageRequest_Package{
			Package: pkg,
		},
	}

	// Temporary: Print the request
	log.Printf("%+v\n", req)
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
		// TODO: Support this after separating setting default image from
		// installing it in sonic-installer.
		return fmt.Errorf("-package_activate=false is not yet supported")
	}
	return nil
}
