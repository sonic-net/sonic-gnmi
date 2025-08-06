package healthz

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	pb "github.com/openconfig/gnoi/healthz"
	"github.com/openconfig/gnoi/types"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/config"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/utils"
	"google.golang.org/grpc"
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
	fmt.Println("Healthz Get client")
	ctx = utils.SetUserCreds(ctx)
	client := pb.NewHealthzClient(conn)

	var input struct {
		Path string `json:"path"` // full string path from CLI
	}
	if err := json.Unmarshal([]byte(*config.Args), &input); err != nil {
		panic(fmt.Sprintf("Invalid JSON input: %v", err))
	}

	fmt.Printf("User CLI path: %s\n", input.Path)

	gnmiPath := parsePath(input.Path)
	fmt.Printf("Parsed CLI path: %s\n", gnmiPath)

	req := &pb.GetRequest{Path: gnmiPath}

	resp, err := client.Get(ctx, req)
	if err != nil {
		fmt.Sprintf("Healthz.Get failed: %v", err)
	}
	printComponentStatus(resp.Component)
}

func List(conn *grpc.ClientConn, ctx context.Context) {
	fmt.Println("Healthz List client")
	ctx = utils.SetUserCreds(ctx)
	client := pb.NewHealthzClient(conn)

	var input struct {
		Path                string `json:"path"` // full string path from CLI
		IncludeAcknowledged bool   `json:"include_acknowledged"`
	}
	if err := json.Unmarshal([]byte(*config.Args), &input); err != nil {
		panic(fmt.Sprintf("Invalid JSON input: %v", err))
	}

	fmt.Printf("User CLI path: %s\n", input.Path)

	gnmiPath := parsePath(input.Path)
	fmt.Printf("Parsed CLI path: %s\n", gnmiPath)

	req := &pb.ListRequest{
		Path:                parsePath(input.Path),
		IncludeAcknowledged: input.IncludeAcknowledged,
	}

	resp, err := client.List(ctx, req)
	if err != nil {
		panic(fmt.Sprintf("Healthz.List RPC failed: %v", err))
	}
	fmt.Printf("List response: %+v\n", resp)
}

func Check(conn *grpc.ClientConn, ctx context.Context) {
	fmt.Println("Healthz Check")

	ctx = utils.SetUserCreds(ctx)
	client := pb.NewHealthzClient(conn)

	// Read input
	var input struct {
		Path    string `json:"path"`
		EventID string `json:"event_id,omitempty"`
	}
	if err := json.Unmarshal([]byte(*config.Args), &input); err != nil {
		panic(fmt.Sprintf("Invalid JSON input: %v", err))
	}

	req := &pb.CheckRequest{
		Path:    parsePath(input.Path),
		EventId: input.EventID,
	}

	resp, err := client.Check(ctx, req)
	if err != nil {
		panic(fmt.Sprintf("Healthz.Check RPC failed: %v", err))
	}
	fmt.Printf("Check response: %+v\n", resp.Status)
}

// Helper to print ComponentStatus nicely
func printComponentStatus(cs *pb.ComponentStatus) {
	fmt.Printf("Healthz Status for: %v\n", pathToString(cs.Path))
	fmt.Printf("Status: %v\n", cs.Status)
	fmt.Printf("Acknowledged: %v\n", cs.Acknowledged)
	fmt.Printf("ID: %v\n", cs.Id)
	fmt.Printf("Artifacts:\n")
	for _, a := range cs.Artifacts {
		fmt.Printf("  - Artifact ID: %v\n", a.Id)
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
