package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/gnxi/utils/credentials"
	gnoi_file_pb "github.com/openconfig/gnoi/file"
	gnoi_system_pb "github.com/openconfig/gnoi/system"
	spb "github.com/sonic-net/sonic-gnmi/proto/gnoi"
	spb_jwt "github.com/sonic-net/sonic-gnmi/proto/gnoi/jwt"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/config"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/utils"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/system"
	"google.golang.org/grpc"
	"os"
	"os/signal"
)

func main() {
	config.ParseFlag()
	opts := credentials.ClientCredentials(*config.TargetName)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt)
		<-c
		cancel()
	}()
	conn, err := grpc.Dial(*config.Target, opts...)
	if err != nil {
		panic(err.Error())
	}

	switch *config.Module {
	case "System":
		sc := gnoi_system_pb.NewSystemClient(conn)
		switch *config.Rpc {
		case "Time":
			system.Time(conn, ctx)
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
		switch *config.Rpc {
		case "Stat":
			fileStat(fc, ctx)
		default:
			panic("Invalid RPC Name")
		}
	case "Sonic":
		switch *config.Rpc {
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
	ctx = utils.SetUserCreds(ctx)
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
	ctx = utils.SetUserCreds(ctx)
	req := &gnoi_system_pb.KillProcessRequest{}
	err := json.Unmarshal([]byte(*config.Args), req)
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
	ctx = utils.SetUserCreds(ctx)
	req := &gnoi_system_pb.RebootRequest{}
	json.Unmarshal([]byte(*config.Args), req)
	_, err := sc.Reboot(ctx, req)
	if err != nil {
		panic(err.Error())
	}
}

func systemCancelReboot(sc gnoi_system_pb.SystemClient, ctx context.Context) {
	fmt.Println("System CancelReboot")
	ctx = utils.SetUserCreds(ctx)
	req := &gnoi_system_pb.CancelRebootRequest{}
	json.Unmarshal([]byte(*config.Args), req)
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
	ctx = utils.SetUserCreds(ctx)
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
	ctx = utils.SetUserCreds(ctx)
	req := &gnoi_file_pb.StatRequest{}
	err := json.Unmarshal([]byte(*config.Args), req)
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
	ctx = utils.SetUserCreds(ctx)
	req := &spb.TechsupportRequest{
		Input: &spb.TechsupportRequest_Input{},
	}

	json.Unmarshal([]byte(*config.Args), req)

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
	ctx = utils.SetUserCreds(ctx)
	req := &spb.CopyConfigRequest{
		Input: &spb.CopyConfigRequest_Input{},
	}
	json.Unmarshal([]byte(*config.Args), req)

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
	ctx = utils.SetUserCreds(ctx)
	req := &spb.ImageInstallRequest{
		Input: &spb.ImageInstallRequest_Input{},
	}
	json.Unmarshal([]byte(*config.Args), req)

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
	ctx = utils.SetUserCreds(ctx)
	req := &spb.ImageRemoveRequest{
		Input: &spb.ImageRemoveRequest_Input{},
	}
	json.Unmarshal([]byte(*config.Args), req)

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
	ctx = utils.SetUserCreds(ctx)
	req := &spb.ImageDefaultRequest{
		Input: &spb.ImageDefaultRequest_Input{},
	}
	json.Unmarshal([]byte(*config.Args), req)

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
	ctx = utils.SetUserCreds(ctx)
	req := &spb_jwt.AuthenticateRequest{}

	json.Unmarshal([]byte(*config.Args), req)

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
	ctx = utils.SetUserCreds(ctx)
	req := &spb_jwt.RefreshRequest{}

	json.Unmarshal([]byte(*config.Args), req)

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
	ctx = utils.SetUserCreds(ctx)
	req := &spb.ClearNeighborsRequest{
		Input: &spb.ClearNeighborsRequest_Input{},
	}
	json.Unmarshal([]byte(*config.Args), req)

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
