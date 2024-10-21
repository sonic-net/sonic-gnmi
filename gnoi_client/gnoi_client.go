package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/google/gnxi/utils/credentials"
	gnoi_file_pb "github.com/openconfig/gnoi/file"
	gnoi_os_pb "github.com/openconfig/gnoi/os"
	gnoi_system_pb "github.com/openconfig/gnoi/system"
	spb "github.com/sonic-net/sonic-gnmi/proto/gnoi"
	spb_jwt "github.com/sonic-net/sonic-gnmi/proto/gnoi/jwt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

var (
	module     = flag.String("module", "System", "gNOI Module")
	rpc        = flag.String("rpc", "Time", "rpc call in specified module to call")
	target     = flag.String("target", "localhost:8080", "Address:port of gNOI Server")
	args       = flag.String("jsonin", "", "RPC Arguments in json format")
	jwtToken   = flag.String("jwt_token", "", "JWT Token if required")
	targetName = flag.String("target_name", "hostname.com", "The target name use to verify the hostname returned by TLS handshake")
)

func setUserCreds(ctx context.Context) context.Context {
	if len(*jwtToken) > 0 {
		ctx = metadata.AppendToOutgoingContext(ctx, "access_token", *jwtToken)
	}
	return ctx
}
func main() {
	flag.Parse()
	opts := credentials.ClientCredentials(*targetName)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt)
		<-c
		cancel()
	}()
	conn, err := grpc.Dial(*target, opts...)
	if err != nil {
		panic(err.Error())
	}

	switch *module {
	case "System":
		sc := gnoi_system_pb.NewSystemClient(conn)
		switch *rpc {
		case "Time":
			systemTime(sc, ctx)
		case "Reboot":
			systemReboot(sc, ctx)
		case "CancelReboot":
			systemCancelReboot(sc, ctx)
		case "RebootStatus":
			systemRebootStatus(sc, ctx)
		case "KillProcess":
			killProcess(sc, ctx)
		default:
			panic("Invalid RPC Name")
		}
	case "File":
		fc := gnoi_file_pb.NewFileClient(conn)
		switch *rpc {
		case "Stat":
			fileStat(fc, ctx)
		default:
			panic("Invalid RPC Name")
		}
	case "OS":
		osc := gnoi_os_pb.NewOSClient(conn)
		switch *rpc {
		case "Install":
			osInstall(osc, ctx)
		case "Activate":
			osActivate(osc, ctx)
		case "Verify":
			osVerify(osc, ctx)
		default:
			panic("Invalid RPC Name")
		}
	case "Sonic":
		switch *rpc {
		case "showtechsupport":
			sc := spb.NewSonicServiceClient(conn)
			sonicShowTechSupport(sc, ctx)
		case "copyConfig":
			sc := spb.NewSonicServiceClient(conn)
			copyConfig(sc, ctx)
		case "authenticate":
			sc := spb_jwt.NewSonicJwtServiceClient(conn)
			authenticate(sc, ctx)
		case "imageInstall":
			sc := spb.NewSonicServiceClient(conn)
			imageInstall(sc, ctx)
		case "imageDefault":
			sc := spb.NewSonicServiceClient(conn)
			imageDefault(sc, ctx)
		case "imageRemove":
			sc := spb.NewSonicServiceClient(conn)
			imageRemove(sc, ctx)
		case "refresh":
			sc := spb_jwt.NewSonicJwtServiceClient(conn)
			refresh(sc, ctx)
		case "clearNeighbors":
			sc := spb.NewSonicServiceClient(conn)
			clearNeighbors(sc, ctx)
		default:
			panic("Invalid RPC Name")
		}
	default:
		panic("Invalid Module Name")
	}
}

func osInstall(osc gnoi_os_pb.OSClient, ctx context.Context) {
	fmt.Println("OS Install")
	ctx = setUserCreds(ctx)
	// Although this is streaming RPC, only the first message TransferRequest
	// contains input information.
	xfer_req := &gnoi_os_pb.TransferRequest{}
	err := json.Unmarshal([]byte(*args), xfer_req)
	if err != nil {
		panic(err.Error())
	}

	// Get Streaming client for OS installation.
	stream, err := osc.Install(ctx)
	if err != nil {
		panic(err.Error)
	}
	defer stream.CloseSend()

	req := gnoi_os_pb.InstallRequest{
		Request: &gnoi_os_pb.InstallRequest_TransferRequest{
			TransferRequest: xfer_req,
		},
	}
	if err := stream.Send(&req); err != nil {
		panic(err.Error())
	}

	// Wait for response
	resp, err := stream.Recv()
	if err != nil {
		panic(err.Error())
	}

	switch resp.GetResponse().(type) {
	case *gnoi_os_pb.InstallResponse_Validated:
		// The target already have the image, do nothing.
	case *gnoi_os_pb.InstallResponse_TransferReady:
		// Target ready for transfer. Sending image.
		// TODO: Make it customizable.
		// Using xfer_req.version as filename and assuming stored at tmp for now.
		img_path_prefix := "/tmp"
		img_name := xfer_req.GetVersion()
		img_path := filepath.Join(img_path_prefix, img_name)

		content, err := os.ReadFile(img_path)
		if err != nil {
			panic(err.Error())
		}

		req := gnoi_os_pb.InstallRequest{
			Request: &gnoi_os_pb.InstallRequest_TransferContent{
				TransferContent: content,
			},
		}

		if err := stream.Send(&req); err != nil {
			panic(err.Error())
		}
	case *gnoi_os_pb.InstallResponse_InstallError:
		// TODO(better error message.)
		panic("Installation error.")
	default:
		// TODO(better error message.)
		panic("Unexpected response")
	}
}

func osActivate(osc gnoi_os_pb.OSClient, ctx context.Context) {
	fmt.Println("OS Activate")
	ctx = setUserCreds(ctx)
	req := &gnoi_os_pb.ActivateRequest{}
	err := json.Unmarshal([]byte(*args), req)
	if err != nil {
		panic(err.Error())
	}
	_, err = osc.Activate(ctx, req)
	if err != nil {
		panic(err.Error())
	}
}

func osVerify(osc gnoi_os_pb.OSClient, ctx context.Context) {
	fmt.Println("OS Verify")
	ctx = setUserCreds(ctx)
	req := &gnoi_os_pb.VerifyRequest{}
	// VerifyRequest has no field.
	resp, err := osc.Verify(ctx, req)
	if err != nil {
		panic(err.Error())
	}
	resp_json, err := json.Marshal(resp)
	if err != nil {
		panic(err.Error())
	}
	fmt.Println(string(resp_json))
}

func systemTime(sc gnoi_system_pb.SystemClient, ctx context.Context) {
	fmt.Println("System Time")
	ctx = setUserCreds(ctx)
	resp, err := sc.Time(ctx, new(gnoi_system_pb.TimeRequest))
	if err != nil {
		panic(err.Error())
	}
	respstr, err := json.Marshal(resp)
	if err != nil {
		panic(err.Error())
	}
	fmt.Println(string(respstr))
}

func killProcess(sc gnoi_system_pb.SystemClient, ctx context.Context) {
	fmt.Println("Kill Process with optional restart")
	ctx = setUserCreds(ctx)
	req := &gnoi_system_pb.KillProcessRequest{}
	err := json.Unmarshal([]byte(*args), req)
	if err != nil {
		panic(err.Error())
	}
	_, err = sc.KillProcess(ctx, req)
	if err != nil {
		panic(err.Error())
	}
}

func fileStat(fc gnoi_file_pb.FileClient, ctx context.Context) {
	fmt.Println("File Stat")
	ctx = setUserCreds(ctx)
	req := &gnoi_file_pb.StatRequest{}
	err := json.Unmarshal([]byte(*args), req)
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

func systemReboot(sc gnoi_system_pb.SystemClient, ctx context.Context) {
	fmt.Println("System Reboot")
	ctx = setUserCreds(ctx)
	req := &gnoi_system_pb.RebootRequest{}
	json.Unmarshal([]byte(*args), req)
	_, err := sc.Reboot(ctx, req)
	if err != nil {
		panic(err.Error())
	}
}

func systemCancelReboot(sc gnoi_system_pb.SystemClient, ctx context.Context) {
	fmt.Println("System CancelReboot")
	ctx = setUserCreds(ctx)
	req := &gnoi_system_pb.CancelRebootRequest{}
	json.Unmarshal([]byte(*args), req)
	resp, err := sc.CancelReboot(ctx, req)
	if err != nil {
		panic(err.Error())
	}
	respstr, err := json.Marshal(resp)
	if err != nil {
		panic(err.Error())
	}
	fmt.Println(string(respstr))
}

func systemRebootStatus(sc gnoi_system_pb.SystemClient, ctx context.Context) {
	fmt.Println("System RebootStatus")
	ctx = setUserCreds(ctx)
	req := &gnoi_system_pb.RebootStatusRequest{}
	resp, err := sc.RebootStatus(ctx, req)
	if err != nil {
		panic(err.Error())
	}
	respstr, err := json.Marshal(resp)
	if err != nil {
		panic(err.Error())
	}
	fmt.Println(string(respstr))
}

func sonicShowTechSupport(sc spb.SonicServiceClient, ctx context.Context) {
	fmt.Println("Sonic ShowTechsupport")
	ctx = setUserCreds(ctx)
	req := &spb.TechsupportRequest{
		Input: &spb.TechsupportRequest_Input{},
	}

	json.Unmarshal([]byte(*args), req)

	resp, err := sc.ShowTechsupport(ctx, req)
	if err != nil {
		panic(err.Error())
	}
	respstr, err := json.Marshal(resp)
	if err != nil {
		panic(err.Error())
	}
	fmt.Println(string(respstr))
}

func copyConfig(sc spb.SonicServiceClient, ctx context.Context) {
	fmt.Println("Sonic CopyConfig")
	ctx = setUserCreds(ctx)
	req := &spb.CopyConfigRequest{
		Input: &spb.CopyConfigRequest_Input{},
	}
	json.Unmarshal([]byte(*args), req)

	resp, err := sc.CopyConfig(ctx, req)

	if err != nil {
		panic(err.Error())
	}
	respstr, err := json.Marshal(resp)
	if err != nil {
		panic(err.Error())
	}
	fmt.Println(string(respstr))
}
func imageInstall(sc spb.SonicServiceClient, ctx context.Context) {
	fmt.Println("Sonic ImageInstall")
	ctx = setUserCreds(ctx)
	req := &spb.ImageInstallRequest{
		Input: &spb.ImageInstallRequest_Input{},
	}
	json.Unmarshal([]byte(*args), req)

	resp, err := sc.ImageInstall(ctx, req)

	if err != nil {
		panic(err.Error())
	}
	respstr, err := json.Marshal(resp)
	if err != nil {
		panic(err.Error())
	}
	fmt.Println(string(respstr))
}
func imageRemove(sc spb.SonicServiceClient, ctx context.Context) {
	fmt.Println("Sonic ImageRemove")
	ctx = setUserCreds(ctx)
	req := &spb.ImageRemoveRequest{
		Input: &spb.ImageRemoveRequest_Input{},
	}
	json.Unmarshal([]byte(*args), req)

	resp, err := sc.ImageRemove(ctx, req)

	if err != nil {
		panic(err.Error())
	}
	respstr, err := json.Marshal(resp)
	if err != nil {
		panic(err.Error())
	}
	fmt.Println(string(respstr))
}

func imageDefault(sc spb.SonicServiceClient, ctx context.Context) {
	fmt.Println("Sonic ImageDefault")
	ctx = setUserCreds(ctx)
	req := &spb.ImageDefaultRequest{
		Input: &spb.ImageDefaultRequest_Input{},
	}
	json.Unmarshal([]byte(*args), req)

	resp, err := sc.ImageDefault(ctx, req)

	if err != nil {
		panic(err.Error())
	}
	respstr, err := json.Marshal(resp)
	if err != nil {
		panic(err.Error())
	}
	fmt.Println(string(respstr))
}

func authenticate(sc spb_jwt.SonicJwtServiceClient, ctx context.Context) {
	fmt.Println("Sonic Authenticate")
	ctx = setUserCreds(ctx)
	req := &spb_jwt.AuthenticateRequest{}

	json.Unmarshal([]byte(*args), req)

	resp, err := sc.Authenticate(ctx, req)
	if err != nil {
		panic(err.Error())
	}
	respstr, err := json.Marshal(resp)
	if err != nil {
		panic(err.Error())
	}
	fmt.Println(string(respstr))
}

func refresh(sc spb_jwt.SonicJwtServiceClient, ctx context.Context) {
	fmt.Println("Sonic Refresh")
	ctx = setUserCreds(ctx)
	req := &spb_jwt.RefreshRequest{}

	json.Unmarshal([]byte(*args), req)

	resp, err := sc.Refresh(ctx, req)
	if err != nil {
		panic(err.Error())
	}
	respstr, err := json.Marshal(resp)
	if err != nil {
		panic(err.Error())
	}
	fmt.Println(string(respstr))
}

func clearNeighbors(sc spb.SonicServiceClient, ctx context.Context) {
	fmt.Println("Sonic ClearNeighbors")
	ctx = setUserCreds(ctx)
	req := &spb.ClearNeighborsRequest{
		Input: &spb.ClearNeighborsRequest_Input{},
	}
	json.Unmarshal([]byte(*args), req)

	resp, err := sc.ClearNeighbors(ctx, req)

	if err != nil {
		panic(err.Error())
	}
	respstr, err := json.Marshal(resp)
	if err != nil {
		panic(err.Error())
	}
	fmt.Println(string(respstr))
}
