package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/google/gnxi/utils/credentials"
	gnoi_file_pb "github.com/openconfig/gnoi/file"
	gnoi_system_pb "github.com/openconfig/gnoi/system"
	spb "github.com/sonic-net/sonic-gnmi/proto/gnoi"
	spb_jwt "github.com/sonic-net/sonic-gnmi/proto/gnoi/jwt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"os"
	"os/signal"
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
			systemKillProcess(sc, ctx)
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
	case "Sonic":
		switch *rpc {
		case "showtechsupport":
			sc := spb.NewSonicServiceClient(conn)
			sonicShowTechSupport(sc, ctx)
		case "copyConfig":
			sc := spb.NewSonicServiceClient(conn)
			sonicCopyConfig(sc, ctx)
		case "authenticate":
			sc := spb_jwt.NewSonicJwtServiceClient(conn)
			sonicAuthenticate(sc, ctx)
		case "imageInstall":
			sc := spb.NewSonicServiceClient(conn)
			sonicImageInstall(sc, ctx)
		case "imageDefault":
			sc := spb.NewSonicServiceClient(conn)
			sonicImageDefault(sc, ctx)
		case "imageRemove":
			sc := spb.NewSonicServiceClient(conn)
			sonicImageRemove(sc, ctx)
		case "refresh":
			sc := spb_jwt.NewSonicJwtServiceClient(conn)
			sonicRefresh(sc, ctx)
		case "clearNeighbors":
			sc := spb.NewSonicServiceClient(conn)
			sonicClearNeighbors(sc, ctx)
		default:
			panic("Invalid RPC Name")
		}
	default:
		panic("Invalid Module Name")
	}

}

// RPC for System Services
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

func systemKillProcess(sc gnoi_system_pb.SystemClient, ctx context.Context) {
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

// RPC for File Services
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

// RPC for Sonic Services
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

func sonicCopyConfig(sc spb.SonicServiceClient, ctx context.Context) {
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

func sonicImageInstall(sc spb.SonicServiceClient, ctx context.Context) {
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

func sonicImageRemove(sc spb.SonicServiceClient, ctx context.Context) {
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

func sonicImageDefault(sc spb.SonicServiceClient, ctx context.Context) {
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

func sonicAuthenticate(sc spb_jwt.SonicJwtServiceClient, ctx context.Context) {
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

func sonicRefresh(sc spb_jwt.SonicJwtServiceClient, ctx context.Context) {
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

func sonicClearNeighbors(sc spb.SonicServiceClient, ctx context.Context) {
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
