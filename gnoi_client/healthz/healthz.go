package healthz

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	pb "github.com/openconfig/gnoi/healthz"
	"github.com/openconfig/gnoi/types"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/config"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/utils"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func parsePath(pathStr string) *types.Path {
	elems := []*types.PathElem{}
	parts := strings.Split(strings.TrimPrefix(pathStr, "/"), "/")
	keyRe := regexp.MustCompile(`(.*)\[(.*)=(.*)\]`)

	for _, p := range parts {
		if p == "" {
			continue
		}
		if keyRe.MatchString(p) {
			m := keyRe.FindStringSubmatch(p)
			name := m[1]
			key := map[string]string{m[2]: strings.Trim(m[3], `"'`)}
			elems = append(elems, &types.PathElem{Name: name, Key: key})
		} else {
			elems = append(elems, &types.PathElem{Name: p})
		}
	}
	return &types.Path{Elem: elems}
}

func Get(conn *grpc.ClientConn, ctx context.Context) {
	ctx = utils.SetUserCreds(ctx)
	client := pb.NewHealthzClient(conn)

	var input struct {
		Path string `json:"path"` // full string path from CLI
	}
	if err := json.Unmarshal([]byte(*config.Args), &input); err != nil {
		utils.LogErrorAndExit("Invalid JSON input: %v", err)
	}
	req := &pb.GetRequest{Path: parsePath(input.Path)}

	resp, err := client.Get(ctx, req)
	if err != nil {
		utils.LogErrorAndExit("Get RPC failed: %v", err)
	}
	printComponentStatus(resp.Component)
}

func Acknowledge(conn *grpc.ClientConn, ctx context.Context) {
	ctx = utils.SetUserCreds(ctx)
	client := pb.NewHealthzClient(conn)

	var input struct {
		Path string `json:"path"`
		ID   string `json:"id"`
	}
	if err := json.Unmarshal([]byte(*config.Args), &input); err != nil {
		utils.LogErrorAndExit("Invalid JSON input: %v", err)
	}
	req := &pb.AcknowledgeRequest{
		Path: parsePath(input.Path),
		Id:   input.ID,
	}

	resp, err := client.Acknowledge(ctx, req)
	if err != nil {
		utils.LogErrorAndExit("Acknowledge RPC failed: %v", err)
	}
	fmt.Printf("Acknowledge response: %+v\n", resp.Status)
}

func Artifact(conn *grpc.ClientConn, ctx context.Context, id string) {
	ctx = utils.SetUserCreds(ctx)
	client := pb.NewHealthzClient(conn)

	req := &pb.ArtifactRequest{
		Id: id,
	}
	stream, err := client.Artifact(ctx, req)
	if err != nil {
		utils.LogErrorAndExit("Artifact RPC failed: %v", err)
	}
	var total int
	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			break // stream complete
		}
		if err != nil {
			utils.LogErrorAndExit("Error receiving artifact resp: %v", err)
		}
		switch c := resp.Contents.(type) {
		case *pb.ArtifactResponse_Header:
			fmt.Printf("Received header: %+v\n", c.Header)
		case *pb.ArtifactResponse_Trailer:
			fmt.Printf("Received trailer: %+v\n", c.Trailer)
			fmt.Printf("Final received size: %d bytes\n", total)
		case *pb.ArtifactResponse_Bytes:
			total += len(c.Bytes)
			fmt.Printf("Received bytes chunk: %d bytes (total=%d)\n", len(c.Bytes), total)
		case *pb.ArtifactResponse_Proto:
			fmt.Printf("Received proto message: %+v\n", c.Proto)
		default:
			fmt.Println("Received unknown content type")
		}
	}
	fmt.Printf("Artifact Response success\n")
}

func List(conn *grpc.ClientConn, ctx context.Context) {
	ctx = utils.SetUserCreds(ctx)
	client := pb.NewHealthzClient(conn)

	var input struct {
		Path                string `json:"path"`
		IncludeAcknowledged bool   `json:"include_acknowledged"`
	}
	if err := json.Unmarshal([]byte(*config.Args), &input); err != nil {
		utils.LogErrorAndExit("Invalid JSON input: %v", err)
	}
	req := &pb.ListRequest{
		Path:                parsePath(input.Path),
		IncludeAcknowledged: input.IncludeAcknowledged,
	}
	resp, err := client.List(ctx, req)
	if err != nil {
		if status.Code(err) == codes.Unimplemented {
			fmt.Println("Healthz.List RPC not implemented on server")
			return
		}
		utils.LogErrorAndExit("List RPC failed: %v", err)
	}
	fmt.Printf("List response: %+v\n", resp)
}

func Check(conn *grpc.ClientConn, ctx context.Context) {
	ctx = utils.SetUserCreds(ctx)
	client := pb.NewHealthzClient(conn)

	var input struct {
		Path    string `json:"path"`
		EventID string `json:"event_id,omitempty"`
	}
	if err := json.Unmarshal([]byte(*config.Args), &input); err != nil {
		utils.LogErrorAndExit("Invalid JSON input: %v", err)
	}
	req := &pb.CheckRequest{
		Path:    parsePath(input.Path),
		EventId: input.EventID,
	}
	resp, err := client.Check(ctx, req)
	if err != nil {
		if status.Code(err) == codes.Unimplemented {
			fmt.Println("Healthz.Check RPC not implemented on server")
			return
		}
		utils.LogErrorAndExit("Check RPC failed: %v", err)
	}
	fmt.Printf("Check response: %+v\n", resp.Status)
}

// Helper to print ComponentStatus nicely
func printComponentStatus(cs *pb.ComponentStatus) {
	fmt.Printf("Healthz Status for Component: %v\n", pathToString(cs.Path))
	fmt.Printf("Status: %v\n", cs.Status)
	fmt.Printf("Acknowledged: %v\n", cs.Acknowledged)
	fmt.Printf("ID: %v\n", cs.Id)
	fmt.Printf("Artifacts:\n")
	for _, a := range cs.Artifacts {
		fmt.Printf("  - Artifact ID: %v\n", a.Id)
		if f, ok := a.ArtifactType.(*pb.ArtifactHeader_File); ok {
			fmt.Printf("    File Name: %v\n", f.File.Name)
			fmt.Printf("    File Size: %v bytes\n", f.File.Size)

			if f.File.Hash != nil {
				fmt.Printf("    Hash Method: %v\n", f.File.Hash.Method)
				fmt.Printf("    Hash Value: %x\n", f.File.Hash.Hash)
			}
		}

	}
	if cs.Created != nil {
		fmt.Printf("Created: %v\n", cs.Created.AsTime())
	}
	if cs.Expires != nil {
		fmt.Printf("Expires: %v\n", cs.Expires.AsTime())
	}
}

func pathToString(p *types.Path) string {
	if p == nil {
		return "<nil>"
	}
	s := ""
	for _, e := range p.Elem {
		s += "/" + e.Name
	}
	return s
}
