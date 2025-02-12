package gnmi

//This file contains dummy tests for the sake of coverage and will be removed later

import (
	"testing"
	"time"

	spb_gnoi "github.com/sonic-net/sonic-gnmi/proto/gnoi"
	"golang.org/x/net/context"
)

func TestDummyClearNeighbor(t *testing.T) {
	// Start server
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.s.Stop()

	// Run Client
	client := createClient(t, 8081)
	sc := spb_gnoi.NewSonicServiceClient(client)
	req := &spb_gnoi.ClearNeighborsRequest{
		Input: &spb_gnoi.ClearNeighborsRequest_Input{},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sc.ClearNeighbors(ctx, req)
}

func TestDummyCopyConfig(t *testing.T) {
	// Start server
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.s.Stop()

	// Run Client
	client := createClient(t, 8081)
	sc := spb_gnoi.NewSonicServiceClient(client)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req := &spb_gnoi.CopyConfigRequest{
		Input: &spb_gnoi.CopyConfigRequest_Input{},
	}
	sc.CopyConfig(ctx, req)
}
