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
		case "CancelReboot":
			systemCancelReboot(sc, ctx)
		case "RebootStatus":
			systemRebootStatus(sc, ctx)
		case "Ping":
			systemPing(sc, ctx)
		case "Traceroute":
			systemTraceroute(sc, ctx)
		case "SetPackage":
			systemSetPackage(sc, ctx)
		case "SwitchControlProcessor":
			systemSwitchControlProcessor(sc, ctx)
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
	_,err := sc.Reboot(ctx, req)
	if err != nil {
		fmt.Println(err.Error())
	}
}

func systemCancelReboot(sc gnoi_system_pb.SystemClient, ctx context.Context) {
	fmt.Println("System CancelReboot")
	ctx = setUserCreds(ctx)
	req := &gnoi_system_pb.CancelRebootRequest {}
	json.Unmarshal([]byte(*args), req)
	resp,err := sc.CancelReboot(ctx, req)
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
	req := &gnoi_system_pb.RebootStatusRequest {}
	resp,err := sc.RebootStatus(ctx, req)
	if err != nil {
		panic(err.Error())
	}
	respstr, err := json.Marshal(resp)
	if err != nil {
		panic(err.Error())
	}
	fmt.Println(string(respstr))
}

func systemPing(sc gnoi_system_pb.SystemClient, ctx context.Context) {
	fmt.Println("System Ping")
	ctx = setUserCreds(ctx)
	req := &gnoi_system_pb.PingRequest {}
	json.Unmarshal([]byte(*args), req)
	resp, err := sc.Ping(ctx, req)
	if err != nil {
		panic(err.Error())
	}
	respstr, err := json.Marshal(resp)
	if err != nil {
		panic(err.Error())
	}
	fmt.Println(string(respstr))
}

func systemTraceroute(sc gnoi_system_pb.SystemClient, ctx context.Context) {
	fmt.Println("System Traceroute")
	ctx = setUserCreds(ctx)
	req := &gnoi_system_pb.TracerouteRequest {}
	json.Unmarshal([]byte(*args), req)
	resp, err := sc.Traceroute(ctx, req)
	if err != nil {
		panic(err.Error())
	}
	respstr, err := json.Marshal(resp)
	if err != nil {
		panic(err.Error())
	}
	fmt.Println(string(respstr))
}

func systemSetPackage(sc gnoi_system_pb.SystemClient, ctx context.Context) {
	fmt.Println("System SetPackage")
	_ = setUserCreds(ctx)
	stream, err := sc.SetPackage(context.Background())
	if err != nil {
		panic(err.Error())
	}
	req := &gnoi_system_pb.SetPackageRequest {}
	json.Unmarshal([]byte(*args), req)
	if err := stream.Send(req); err != nil {
		panic(err.Error())
	}
	resp, err := stream.CloseAndRecv()
	if err != nil {
		panic(err.Error())
	}
	respstr, err := json.Marshal(resp)
	if err != nil {
		panic(err.Error())
	}
	fmt.Println(string(respstr))
}

func systemSwitchControlProcessor(sc gnoi_system_pb.SystemClient, ctx context.Context) {
	fmt.Println("System SwitchControlProcessor")
	ctx = setUserCreds(ctx)
	req := &gnoi_system_pb.SwitchControlProcessorRequest {}
	json.Unmarshal([]byte(*args), req)
	resp, err := sc.SwitchControlProcessor(ctx, req)
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

