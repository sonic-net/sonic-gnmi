package main

import (
	"google.golang.org/grpc"
	gnoi_system_pb "github.com/openconfig/gnoi/system"
	spb_jwt "github.com/sonic-net/sonic-gnmi/proto/gnoi/jwt"
	"context"
	"os"
	"os/signal"
	"fmt"
	"flag"
	"google.golang.org/grpc/metadata"
	"github.com/google/gnxi/utils/credentials"
	"encoding/json"
)

var (
	module = flag.String("module", "System", "gNOI Module")
	rpc = flag.String("rpc", "Time", "rpc call in specified module to call")
	target = flag.String("target", "localhost:8080", "Address:port of gNOI Server")
	args = flag.String("jsonin", "", "RPC Arguments in json format")
	jwtToken = flag.String("jwt_token", "", "JWT Token if required")
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
		default:
			panic("Invalid RPC Name")
		}
	case "Sonic":
		switch *rpc {
		case "authenticate":
			sc := spb_jwt.NewSonicJwtServiceClient(conn)
			authenticate(sc, ctx)
		case "refresh":
			sc := spb_jwt.NewSonicJwtServiceClient(conn)
			refresh(sc, ctx)
		default:
			panic("Invalid RPC Name")
		}
	default:
		panic("Invalid Module Name")
	}

}

func systemTime(sc gnoi_system_pb.SystemClient, ctx context.Context) {
	fmt.Println("System Time")
	ctx = setUserCreds(ctx)
	resp,err := sc.Time(ctx, new(gnoi_system_pb.TimeRequest))
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
	req := &gnoi_system_pb.RebootRequest {}
	json.Unmarshal([]byte(*args), req)
	resp,err := sc.Reboot(ctx, req)
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
	req := &spb_jwt.AuthenticateRequest {}
	
	json.Unmarshal([]byte(*args), req)
	
	resp,err := sc.Authenticate(ctx, req)
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
	req := &spb_jwt.RefreshRequest {}
	
	json.Unmarshal([]byte(*args), req)

	resp,err := sc.Refresh(ctx, req)
	if err != nil {
		panic(err.Error())
	}
	respstr, err := json.Marshal(resp)
	if err != nil {
		panic(err.Error())
	}
	fmt.Println(string(respstr))
}

