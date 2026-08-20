package sonic

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/config"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/utils"
	pb "github.com/sonic-net/sonic-gnmi/proto/gnoi"
	"google.golang.org/grpc"
)

func ShowTechSupport(conn *grpc.ClientConn, ctx context.Context) {
	fmt.Println("Sonic ShowTechsupport")
	ctx = utils.SetUserCreds(ctx)
	sc := pb.NewSonicServiceClient(conn)
	req := &pb.TechsupportRequest{
		Input: &pb.TechsupportRequest_Input{},
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

func CopyConfig(conn *grpc.ClientConn, ctx context.Context) {
	fmt.Println("Sonic CopyConfig")
	ctx = utils.SetUserCreds(ctx)
	sc := pb.NewSonicServiceClient(conn)
	req := &pb.CopyConfigRequest{
		Input: &pb.CopyConfigRequest_Input{},
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

func ImageInstall(conn *grpc.ClientConn, ctx context.Context) {
	fmt.Println("Sonic ImageInstall")
	ctx = utils.SetUserCreds(ctx)
	sc := pb.NewSonicServiceClient(conn)
	req := &pb.ImageInstallRequest{
		Input: &pb.ImageInstallRequest_Input{},
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

func ImageRemove(conn *grpc.ClientConn, ctx context.Context) {
	fmt.Println("Sonic ImageRemove")
	ctx = utils.SetUserCreds(ctx)
	sc := pb.NewSonicServiceClient(conn)
	req := &pb.ImageRemoveRequest{
		Input: &pb.ImageRemoveRequest_Input{},
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

func ImageDefault(conn *grpc.ClientConn, ctx context.Context) {
	fmt.Println("Sonic ImageDefault")
	ctx = utils.SetUserCreds(ctx)
	sc := pb.NewSonicServiceClient(conn)
	req := &pb.ImageDefaultRequest{
		Input: &pb.ImageDefaultRequest_Input{},
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

func ClearNeighbors(conn *grpc.ClientConn, ctx context.Context) {
	fmt.Println("Sonic ClearNeighbors")
	ctx = utils.SetUserCreds(ctx)
	sc := pb.NewSonicServiceClient(conn)
	req := &pb.ClearNeighborsRequest{
		Input: &pb.ClearNeighborsRequest_Input{},
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

// ConfigSave triggers SonicService.ConfigSave on the server, which persists
// the running configuration to /etc/sonic/config_db.json. The server ignores
// any caller-supplied path so `--jsonin` need only be `{}` (the default).
func ConfigSave(conn *grpc.ClientConn, ctx context.Context) {
	fmt.Println("Sonic ConfigSave")
	ctx = utils.SetUserCreds(ctx)
	sc := pb.NewSonicServiceClient(conn)
	req := &pb.ConfigSaveRequest{
		Input: &pb.ConfigSaveRequest_Input{},
	}
	json.Unmarshal([]byte(*config.Args), req)
	resp, err := sc.ConfigSave(ctx, req)
	if err != nil {
		panic(err.Error())
	}
	respstr, err := json.Marshal(resp)
	if err != nil {
		panic(err.Error())
	}
	fmt.Println(string(respstr))
}

// ConfigReload triggers SonicService.ConfigReload on the server. With an
// empty `--jsonin` the server reloads from /etc/sonic/config_db.json; pass
// `{"input":{"config_json":"<...>"}}` to reload an inline JSON document.
func ConfigReload(conn *grpc.ClientConn, ctx context.Context) {
	fmt.Println("Sonic ConfigReload")
	ctx = utils.SetUserCreds(ctx)
	sc := pb.NewSonicServiceClient(conn)
	req := &pb.ConfigReloadRequest{
		Input: &pb.ConfigReloadRequest_Input{},
	}
	json.Unmarshal([]byte(*config.Args), req)
	resp, err := sc.ConfigReload(ctx, req)
	if err != nil {
		panic(err.Error())
	}
	respstr, err := json.Marshal(resp)
	if err != nil {
		panic(err.Error())
	}
	fmt.Println(string(respstr))
}
