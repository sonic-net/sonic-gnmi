package main

import (
	"context"
	"flag"
	"os"
	"os/signal"

	"github.com/google/gnxi/utils/credentials"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/file"
	gnoi_os "github.com/sonic-net/sonic-gnmi/gnoi_client/os" // So it does not collide with os.
	"github.com/sonic-net/sonic-gnmi/gnoi_client/sonic"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/system"
	"google.golang.org/grpc"
)

var (
	module     = flag.String("module", "System", "gNOI Module")
	rpc        = flag.String("rpc", "Time", "rpc call in specified module to call")
	target     = flag.String("target", "localhost:8080", "Address:port of gNOI Server")
	targetName = flag.String("target_name", "hostname.com", "The target name use to verify the hostname returned by TLS handshake")
)

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
		switch *rpc {
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
		switch *rpc {
		case "Stat":
			file.Stat(conn, ctx)
		default:
			panic("Invalid RPC Name")
		}
	case "OS":
		switch *rpc {
		case "Verify":
			gnoi_os.Verify(conn, ctx)
		case "Activate":
			gnoi_os.Activate(conn, ctx)
		default:
			panic("Invalid RPC Name")
		}
	case "Sonic":
		switch *rpc {
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
