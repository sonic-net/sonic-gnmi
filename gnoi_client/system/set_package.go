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

// newSystemClient is a package-level variable that returns a new system.SystemClient.
// We define it here so that unit tests can replace it with a mock constructor if needed.
var newSystemClient = func(conn *grpc.ClientConn) system.SystemClient {
	return system.NewSystemClient(conn)
}

// SetPackage is the main entry point. It validates flags, creates the SystemClient,
// and then calls setPackageClient to perform the actual SetPackage gRPC flow.
func SetPackage(conn *grpc.ClientConn, ctx context.Context) {
	fmt.Println("System SetPackage")

	// Attach user credentials if needed.
	ctx = utils.SetUserCreds(ctx)

	// Validate the flags before proceeding.
	err := validateFlags()
	if err != nil {
		fmt.Println("Error validating flags:", err)
		return
	}

	// Create a new gNOI SystemClient using the function defined above.
	sc := newSystemClient(conn)

	// Call the helper that implements the SetPackage logic (sending requests, closing, etc.).
	if err := setPackageClient(sc, ctx); err != nil {
		fmt.Println("Error during SetPackage:", err)
	}
}

// setPackageClient contains the core gRPC calls to send the package request and
// receive the response. We separate it out for easier testing and mocking.
func setPackageClient(sc system.SystemClient, ctx context.Context) error {
	// Prepare the remote download info.
	download := &common.RemoteDownload{
		Path: *url,
	}

	// Build the package with flags.
	pkg := &system.Package{
		Filename:       *filename,
		Version:        *version,
		Activate:       *activate,
		RemoteDownload: download,
	}

	// The gNOI SetPackageRequest can contain different request types, but we only
	// use the "Package" request type here.
	req := &system.SetPackageRequest{
		Request: &system.SetPackageRequest_Package{
			Package: pkg,
		},
	}

	// Open a streaming RPC.
	stream, err := sc.SetPackage(ctx)
	if err != nil {
		return fmt.Errorf("error creating stream: %v", err)
	}

	// Send the package information.
	if err := stream.Send(req); err != nil {
		return fmt.Errorf("error sending package request: %v", err)
	}

	// Close the send direction of the stream. The device should download the
	// package itself, so we are not sending direct data or checksums here.
	if err := stream.CloseSend(); err != nil {
		return fmt.Errorf("error closing send direction: %v", err)
	}

	// Receive the final response from the device.
	resp, err := stream.CloseAndRecv()
	if err != nil {
		return fmt.Errorf("error receiving response: %v", err)
	}

	// For demonstration purposes, we simply print the response.
	fmt.Println(resp)
	return nil
}

// validateFlags ensures all required flags are set correctly before we proceed.
func validateFlags() error {
	if *filename == "" {
		return fmt.Errorf("missing -package_filename: Destination path and filename of the package is required for the SetPackage operation")
	}
	if *version == "" {
		return fmt.Errorf("missing -package_version: Version of the package is required for the SetPackage operation")
	}
	if *url == "" {
		return fmt.Errorf("missing -package_url: URL to download the package from is required for the SetPackage operation. Direct transfer is not supported yet")
	}
	if !*activate {
		// TODO: Currently, installing the image will always set it to default.
		return fmt.Errorf("-package_activate=false is not yet supported: The package will always be activated after setting it")
	}
	return nil
}
