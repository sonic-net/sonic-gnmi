package main

import (
	"context"
	"github.com/google/gnxi/utils/credentials"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/config"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/system"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/file"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/sonic"
	gnoi_os "github.com/sonic-net/sonic-gnmi/gnoi_client/os"	// So it does not collide with os.
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
		default:
			panic("Invalid RPC Name")
		}
	case "File":
		switch *config.Rpc {
		case "Stat":
			file.Stat(conn, ctx)
		default:
			panic("Invalid RPC Name")
		}
	case "OS":
		switch *config.Rpc {
		case "Verify":
			gnoi_os.Verify(conn, ctx)
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
	default:
		panic("Invalid Module Name")
	}

}