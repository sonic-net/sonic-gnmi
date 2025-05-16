package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/google/gnxi/utils/credentials"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/config"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/containerz"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/file"
	gnoi_os "github.com/sonic-net/sonic-gnmi/gnoi_client/os" // So it does not collide with os.
	"github.com/sonic-net/sonic-gnmi/gnoi_client/sonic"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/system"
	"google.golang.org/grpc"
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
		switch *config.Rpc {
		case "Time":
			system.Time(conn, ctx)
		case "Reboot":
			system.Reboot(conn, ctx)
		case "CancelReboot":
			system.CancelReboot(conn, ctx)
		case "RebootStatus":
			system.RebootStatus(conn, ctx)
		case "KillProcess":
			system.KillProcess(conn, ctx)
		case "SetPackage":
			system.SetPackage(conn, ctx)
		default:
			panic("Invalid RPC Name")
		}
	case "File":
		switch *config.Rpc {
		case "Stat":
			file.Stat(conn, ctx)
		case "Get":
			file.Get(conn, ctx)
		case "Put":
			file.Put(conn, ctx)
		case "Remove":
			file.Remove(conn, ctx)
		case "TransferToRemote":
			file.TransferToRemote(conn, ctx)
		default:
			panic("Invalid RPC Name")
		}
	case "OS":
		switch *config.Rpc {
		case "Verify":
			gnoi_os.Verify(conn, ctx)
		case "Activate":
			gnoi_os.Activate(conn, ctx)
		default:
			panic("Invalid RPC Name")
		}
	case "Sonic":
		switch *config.Rpc {
		case "showtechsupport":
			sonic.ShowTechSupport(conn, ctx)
		case "copyConfig":
			sonic.CopyConfig(conn, ctx)
		case "authenticate":
			sonic.Authenticate(conn, ctx)
		case "imageInstall":
			sonic.ImageInstall(conn, ctx)
		case "imageDefault":
			sonic.ImageDefault(conn, ctx)
		case "imageRemove":
			sonic.ImageRemove(conn, ctx)
		case "refresh":
			sonic.Refresh(conn, ctx)
		case "clearNeighbors":
			sonic.ClearNeighbors(conn, ctx)
		default:
			panic("Invalid RPC Name")
		}
	case "Containerz":
		switch *config.Rpc {
		case "Deploy":
			containerz.Deploy(conn, ctx)
		default:
			panic("Invalid RPC Name")
		}
	default:
		panic("Invalid Module Name")
	}

}
