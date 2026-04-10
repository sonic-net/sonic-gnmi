package gnmi

import (
	"context"
	"crypto/tls"
	"io"
	"io/ioutil"
	"sync"
	"testing"
	"time"

	pb "github.com/openconfig/gnmi/proto/gnmi"
	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
	testcert "github.com/sonic-net/sonic-gnmi/testdata/tls"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func createServerWithStreamMultiplexing(t *testing.T, port int64) *Server {
	t.Helper()
	certificate, err := testcert.NewCert()
	if err != nil {
		t.Fatalf("could not load server key pair: %s", err)
	}
	tlsCfg := &tls.Config{
		ClientAuth:   tls.RequestClientCert,
		Certificates: []tls.Certificate{certificate},
	}

	tlsOpts := []grpc.ServerOption{grpc.Creds(credentials.NewTLS(tlsCfg))}
	cfg := &Config{
		Port:                     port,
		EnableTranslibWrite:      true,
		EnableNativeWrite:        true,
		Threshold:                100,
		ImgDir:                   "/tmp",
		EnableStreamMultiplexing: true,
	}
	s, err := NewServer(cfg, tlsOpts, nil)
	if err != nil {
		t.Fatalf("Failed to create gNMI server: %v", err)
	}
	return s
}

func TestMultipleStreamsOnSameTCPConn(t *testing.T) {
	s := createServerWithStreamMultiplexing(t, 8081)
	go runServer(t, s)
	defer s.ForceStop()

	ns, _ := sdcfg.GetDbDefaultNamespace()

	// Set up COUNTERS_DB with all required maps (port name, queue, PG, fabric, etc.)
	prepareDb(t, ns)

	// Set up APPL_DB data
	applDbClient := getRedisClientN(t, 0, ns)
	defer applDbClient.Close()
	applDbClient.FlushDB(context.Background())
	applDbClient.HSet(context.Background(), "ROUTE_TABLE:0.0.0.0/0", "ifname", "dummy")
	applDbClient.HSet(context.Background(), "ROUTE_TABLE:0.0.0.0/0", "nexthop", "dummy")

	// Set up STATE_DB data
	fileName := "../testdata/NEIGH_STATE_TABLE.txt"
	neighStateTableByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	stateDbClient := getRedisClientN(t, 6, ns)
	defer stateDbClient.Close()
	stateDbClient.FlushDB(context.Background())
	mpi_neigh := loadConfig(t, "", neighStateTableByte)
	loadDB(t, stateDbClient, mpi_neigh)

	time.Sleep(time.Millisecond * 100)

	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}
	conn, err := grpc.Dial("127.0.0.1:8081", opts...)
	if err != nil {
		t.Fatalf("Failed to dial: %v", err)
	}
	defer conn.Close()

	gClient := pb.NewGNMIClient(conn)
	ctx := context.Background()

	stream1, err := gClient.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Failed to create Subscribe stream #1: %v", err)
	}

	stream2, err := gClient.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Failed to create Subscribe stream #2: %v", err)
	}

	stream3, err := gClient.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Failed to create Subscribe stream #3: %v", err)
	}

	responses := make(chan *pb.SubscribeResponse, 100)
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		msgCount := 0
		for {
			resp, err := stream1.Recv()
			if err == io.EOF {
				t.Logf("Stream1 (STATE_DB): EOF after %d messages", msgCount)
				return
			}
			if err != nil {
				t.Logf("Stream1 (STATE_DB) recv error after %d messages: %v", msgCount, err)
				return
			}
			msgCount++
			if resp.GetSyncResponse() {
				t.Logf("Stream1 (STATE_DB): got sync response (#%d)", msgCount)
			}
			if update := resp.GetUpdate(); update != nil {
				t.Logf("Stream1 (STATE_DB): got update (#%d) with %d updates, prefix target: %s", msgCount, len(update.Update), update.Prefix.GetTarget())
			}
			responses <- resp
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		msgCount := 0
		for {
			resp, err := stream2.Recv()
			if err == io.EOF {
				t.Logf("Stream2 (COUNTERS_DB): EOF after %d messages", msgCount)
				return
			}
			if err != nil {
				t.Logf("Stream2 (COUNTERS_DB) recv error after %d messages: %v", msgCount, err)
				return
			}
			msgCount++
			if resp.GetSyncResponse() {
				t.Logf("Stream2 (COUNTERS_DB): got sync response (#%d)", msgCount)
			}
			if update := resp.GetUpdate(); update != nil {
				t.Logf("Stream2 (COUNTERS_DB): got update (#%d) with %d updates, prefix target: %s", msgCount, len(update.Update), update.Prefix.GetTarget())
			}
			responses <- resp
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		msgCount := 0
		for {
			resp, err := stream3.Recv()
			if err == io.EOF {
				t.Logf("Stream3 (APPL_DB): EOF after %d messages", msgCount)
				return
			}
			if err != nil {
				t.Logf("Stream3 (APPL_DB) recv error after %d messages: %v", msgCount, err)
				return
			}
			msgCount++
			if resp.GetSyncResponse() {
				t.Logf("Stream3 (APPL_DB): got sync response (#%d)", msgCount)
			}
			if update := resp.GetUpdate(); update != nil {
				t.Logf("Stream3 (APPL_DB): got update (#%d) with %d updates, prefix target: %s", msgCount, len(update.Update), update.Prefix.GetTarget())
			}
			responses <- resp
		}
	}()

	req1 := &pb.SubscribeRequest{
		Request: &pb.SubscribeRequest_Subscribe{
			Subscribe: &pb.SubscriptionList{
				Prefix: &pb.Path{Target: "STATE_DB"},
				Mode:   pb.SubscriptionList_POLL,
				Subscription: []*pb.Subscription{
					{
						Path: &pb.Path{
							Elem: []*pb.PathElem{
								{Name: "NEIGH_STATE_TABLE"},
							},
						},
					},
				},
			},
		},
	}

	if err := stream1.Send(req1); err != nil {
		t.Fatalf("Failed to send SubscribeRequest on stream1: %v", err)
	}
	t.Logf("Sent SubscribeRequest on stream #1 for STATE_DB")

	req2 := &pb.SubscribeRequest{
		Request: &pb.SubscribeRequest_Subscribe{
			Subscribe: &pb.SubscriptionList{
				Prefix: &pb.Path{Target: "COUNTERS_DB"},
				Mode:   pb.SubscriptionList_POLL,
				Subscription: []*pb.Subscription{
					{
						Path: &pb.Path{
							Elem: []*pb.PathElem{
								{Name: "COUNTERS"},
								{Name: "Ethernet68"},
							},
						},
					},
				},
			},
		},
	}

	if err := stream2.Send(req2); err != nil {
		t.Fatalf("Failed to send SubscribeRequest on stream2: %v", err)
	}
	t.Logf("Sent SubscribeRequest on stream #2 for COUNTERS_DB")

	req3 := &pb.SubscribeRequest{
		Request: &pb.SubscribeRequest_Subscribe{
			Subscribe: &pb.SubscriptionList{
				Prefix: &pb.Path{Target: "APPL_DB"},
				Mode:   pb.SubscriptionList_POLL,
				Subscription: []*pb.Subscription{
					{
						Path: &pb.Path{
							Elem: []*pb.PathElem{
								{Name: "ROUTE_TABLE"},
								{Name: "0.0.0.0/0"},
							},
						},
					},
				},
			},
		},
	}

	if err := stream3.Send(req3); err != nil {
		t.Fatalf("Failed to send SubscribeRequest on stream3: %v", err)
	}
	t.Logf("Sent SubscribeRequest on stream #3 for APPL_DB")

	// Wait for subscriptions to be registered
	time.Sleep(time.Millisecond * 200)

	pollReq := &pb.SubscribeRequest{
		Request: &pb.SubscribeRequest_Poll{
			Poll: &pb.Poll{},
		},
	}

	numPolls := 3
	for i := 0; i < numPolls; i++ {
		if err := stream1.Send(pollReq); err != nil {
			t.Fatalf("Failed to send poll request %d on stream1: %v", i+1, err)
		}
		if err := stream2.Send(pollReq); err != nil {
			t.Fatalf("Failed to send poll request %d on stream2: %v", i+1, err)
		}
		if err := stream3.Send(pollReq); err != nil {
			t.Fatalf("Failed to send poll request %d on stream3: %v", i+1, err)
		}
		t.Logf("Sent poll request %d on all 3 streams", i+1)
		time.Sleep(time.Millisecond * 100)
	}

	// Wait for responses to arrive
	time.Sleep(time.Millisecond * 500)

	// Verify server state WHILE streams are still active
	s.cMu.Lock()
	activeClients := len(s.clients)
	clientKeys := make([]ClientKey, 0, len(s.clients))
	uniqueAddrs := make(map[string]bool)

	for clientKey := range s.clients {
		clientKeys = append(clientKeys, clientKey)
		uniqueAddrs[clientKey.PeerAddr] = true
	}
	s.cMu.Unlock()

	t.Logf("Server has %d active client(s): %v", activeClients, clientKeys)
	t.Logf("Unique TCP addresses: %v", uniqueAddrs)

	// Close send on all streams
	stream1.CloseSend()
	stream2.CloseSend()
	stream3.CloseSend()

	// Wait for receivers to finish
	go func() {
		wg.Wait()
		close(responses)
	}()

	// Collect responses
	stateDbResponses := 0
	countersDbResponses := 0
	applDbResponses := 0
	syncMessages := 0

	timeout := time.After(2 * time.Second)
collectLoop:
	for {
		select {
		case resp, ok := <-responses:
			if !ok {
				break collectLoop
			}
			if update := resp.GetUpdate(); update != nil {
				if update.Prefix != nil {
					switch update.Prefix.Target {
					case "STATE_DB":
						stateDbResponses++
					case "COUNTERS_DB":
						countersDbResponses++
					case "APPL_DB":
						applDbResponses++
					}
				}
			}
			if resp.GetSyncResponse() {
				syncMessages++
			}
		case <-timeout:
			t.Logf("Timeout waiting for responses")
			break collectLoop
		}
	}

	t.Logf("Received %d STATE_DB, %d COUNTERS_DB, %d APPL_DB update responses, and %d sync messages",
		stateDbResponses, countersDbResponses, applDbResponses, syncMessages)

	// Verify we had 3 active clients while streams were running
	if activeClients != 3 {
		t.Errorf("Expected 3 active clients (one per stream), got %d", activeClients)
	}

	// Verify all streams on same TCP connection
	if len(uniqueAddrs) != 1 {
		t.Errorf("Expected all streams on 1 TCP connection, got %d unique addresses: %v", len(uniqueAddrs), uniqueAddrs)
	}

	// Verify responses received
	expectedUpdates := numPolls
	expectedSyncs := numPolls

	if stateDbResponses < expectedUpdates {
		t.Errorf("Expected at least %d STATE_DB update responses (one per poll), got %d", expectedUpdates, stateDbResponses)
	}
	if countersDbResponses < expectedUpdates {
		t.Errorf("Expected at least %d COUNTERS_DB update responses (one per poll), got %d", expectedUpdates, countersDbResponses)
	}
	if applDbResponses < expectedUpdates {
		t.Errorf("Expected at least %d APPL_DB update responses (one per poll), got %d", expectedUpdates, applDbResponses)
	}
	if syncMessages < expectedSyncs*3 {
		t.Errorf("Expected at least %d sync messages (%d per stream), got %d", expectedSyncs*3, expectedSyncs, syncMessages)
	}

	if stateDbResponses >= expectedUpdates && countersDbResponses >= expectedUpdates && applDbResponses >= expectedUpdates && len(uniqueAddrs) == 1 && activeClients == 3 {
		t.Logf("SUCCESS: 3 gRPC streams on same TCP connection, all receiving updates!")
	}
}

func TestMixedModeStreamsOnSameTCPConn(t *testing.T) {
	s := createServerWithStreamMultiplexing(t, 8082)
	go runServer(t, s)
	defer s.ForceStop()

	ns, _ := sdcfg.GetDbDefaultNamespace()

	// Set up APPL_DB data for POLL stream
	applDbClient := getRedisClientN(t, 0, ns)
	defer applDbClient.Close()
	applDbClient.FlushDB(context.Background())
	applDbClient.HSet(context.Background(), "ROUTE_TABLE:0.0.0.0/0", "ifname", "eth0")
	applDbClient.HSet(context.Background(), "ROUTE_TABLE:0.0.0.0/0", "nexthop", "10.0.0.1")

	// Set up STATE_DB data for ON_CHANGE stream
	stateDbClient := getRedisClientN(t, 6, ns)
	defer stateDbClient.Close()
	stateDbClient.FlushDB(context.Background())
	stateDbClient.HSet(context.Background(), "NEIGH_STATE_TABLE|10.0.0.1", "state", "reachable")

	time.Sleep(time.Millisecond * 100)

	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}
	conn, err := grpc.Dial("127.0.0.1:8082", opts...)
	if err != nil {
		t.Fatalf("Failed to dial: %v", err)
	}
	defer conn.Close()

	gClient := pb.NewGNMIClient(conn)
	ctx := context.Background()

	// Stream 1: POLL mode on APPL_DB
	pollStream, err := gClient.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Failed to create POLL Subscribe stream: %v", err)
	}

	// Stream 2: ON_CHANGE (STREAM) mode on STATE_DB
	onChangeStream, err := gClient.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Failed to create ON_CHANGE Subscribe stream: %v", err)
	}

	type taggedResponse struct {
		streamName string
		resp       *pb.SubscribeResponse
	}
	responses := make(chan taggedResponse, 100)
	var wg sync.WaitGroup

	// Receiver for POLL stream
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			resp, err := pollStream.Recv()
			if err == io.EOF {
				return
			}
			if err != nil {
				t.Logf("POLL stream recv error: %v", err)
				return
			}
			responses <- taggedResponse{streamName: "POLL", resp: resp}
		}
	}()

	// Receiver for ON_CHANGE stream
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			resp, err := onChangeStream.Recv()
			if err == io.EOF {
				return
			}
			if err != nil {
				t.Logf("ON_CHANGE stream recv error: %v", err)
				return
			}
			responses <- taggedResponse{streamName: "ON_CHANGE", resp: resp}
		}
	}()

	// Send POLL subscription request
	pollReq := &pb.SubscribeRequest{
		Request: &pb.SubscribeRequest_Subscribe{
			Subscribe: &pb.SubscriptionList{
				Prefix: &pb.Path{Target: "APPL_DB"},
				Mode:   pb.SubscriptionList_POLL,
				Subscription: []*pb.Subscription{
					{
						Path: &pb.Path{
							Elem: []*pb.PathElem{
								{Name: "ROUTE_TABLE"},
								{Name: "0.0.0.0/0"},
							},
						},
					},
				},
			},
		},
	}
	if err := pollStream.Send(pollReq); err != nil {
		t.Fatalf("Failed to send POLL SubscribeRequest: %v", err)
	}
	t.Logf("Sent POLL SubscribeRequest on APPL_DB")

	// Send ON_CHANGE subscription request
	onChangeReq := &pb.SubscribeRequest{
		Request: &pb.SubscribeRequest_Subscribe{
			Subscribe: &pb.SubscriptionList{
				Prefix: &pb.Path{Target: "STATE_DB"},
				Mode:   pb.SubscriptionList_STREAM,
				Subscription: []*pb.Subscription{
					{
						Path: &pb.Path{
							Elem: []*pb.PathElem{
								{Name: "NEIGH_STATE_TABLE"},
							},
						},
						Mode: pb.SubscriptionMode_ON_CHANGE,
					},
				},
			},
		},
	}
	if err := onChangeStream.Send(onChangeReq); err != nil {
		t.Fatalf("Failed to send ON_CHANGE SubscribeRequest: %v", err)
	}
	t.Logf("Sent ON_CHANGE SubscribeRequest on STATE_DB")

	// Wait for subscriptions to be set up and initial sync
	time.Sleep(time.Millisecond * 500)

	// Send a poll request on the POLL stream
	pollTrigger := &pb.SubscribeRequest{
		Request: &pb.SubscribeRequest_Poll{
			Poll: &pb.Poll{},
		},
	}
	if err := pollStream.Send(pollTrigger); err != nil {
		t.Fatalf("Failed to send poll trigger: %v", err)
	}
	t.Logf("Sent poll trigger on POLL stream")

	// Trigger an ON_CHANGE update by modifying STATE_DB
	time.Sleep(time.Millisecond * 200)
	stateDbClient.HSet(context.Background(), "NEIGH_STATE_TABLE|10.0.0.2", "state", "reachable")
	t.Logf("Triggered STATE_DB change for ON_CHANGE stream")

	// Wait for responses
	time.Sleep(time.Second * 1)

	// Check server state while streams are active
	s.cMu.Lock()
	activeClients := len(s.clients)
	clientKeys := make([]ClientKey, 0, len(s.clients))
	uniqueAddrs := make(map[string]bool)
	for clientKey := range s.clients {
		clientKeys = append(clientKeys, clientKey)
		uniqueAddrs[clientKey.PeerAddr] = true
	}
	s.cMu.Unlock()

	t.Logf("Server has %d active client(s): %v", activeClients, clientKeys)
	t.Logf("Unique TCP addresses: %v", uniqueAddrs)

	// Close streams
	pollStream.CloseSend()
	onChangeStream.CloseSend()

	go func() {
		wg.Wait()
		close(responses)
	}()

	// Collect responses
	pollUpdates := 0
	onChangeUpdates := 0
	pollSyncs := 0
	onChangeSyncs := 0

	timeout := time.After(2 * time.Second)
collectLoop:
	for {
		select {
		case tagged, ok := <-responses:
			if !ok {
				break collectLoop
			}
			if tagged.resp.GetSyncResponse() {
				if tagged.streamName == "POLL" {
					pollSyncs++
				} else {
					onChangeSyncs++
				}
			}
			if update := tagged.resp.GetUpdate(); update != nil {
				if tagged.streamName == "POLL" {
					pollUpdates++
				} else {
					onChangeUpdates++
				}
			}
		case <-timeout:
			t.Logf("Timeout waiting for responses")
			break collectLoop
		}
	}

	t.Logf("POLL stream: %d updates, %d syncs", pollUpdates, pollSyncs)
	t.Logf("ON_CHANGE stream: %d updates, %d syncs", onChangeUpdates, onChangeSyncs)

	// Verify 2 active clients on same TCP connection
	if activeClients != 2 {
		t.Errorf("Expected 2 active clients (POLL + ON_CHANGE), got %d", activeClients)
	}
	if len(uniqueAddrs) != 1 {
		t.Errorf("Expected all streams on 1 TCP connection, got %d unique addresses: %v", len(uniqueAddrs), uniqueAddrs)
	}

	// Verify both streams received data
	if pollUpdates < 1 {
		t.Errorf("Expected at least 1 POLL update response, got %d", pollUpdates)
	}
	if onChangeUpdates < 1 {
		t.Errorf("Expected at least 1 ON_CHANGE update response, got %d", onChangeUpdates)
	}

	if activeClients == 2 && len(uniqueAddrs) == 1 && pollUpdates >= 1 && onChangeUpdates >= 1 {
		t.Logf("SUCCESS: Mixed POLL + ON_CHANGE streams coexist on same TCP connection!")
	}
}

// TestMultipleStreamsDuplicateCloseWithoutFlag verifies that when EnableStreamMultiplexing is false (default),
// the legacy behavior is preserved: a second Subscribe from the same peer closes the first.
func TestMultipleStreamsDuplicateCloseWithoutFlag(t *testing.T) {
	// Use createServer (not createServerWithStreamMultiplexing) — EnableStreamMultiplexing defaults to false
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.ForceStop()

	ns, _ := sdcfg.GetDbDefaultNamespace()
	prepareDb(t, ns)

	time.Sleep(time.Millisecond * 100)

	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}
	conn, err := grpc.Dial("127.0.0.1:8081", opts...)
	if err != nil {
		t.Fatalf("Failed to dial: %v", err)
	}
	defer conn.Close()

	gClient := pb.NewGNMIClient(conn)
	ctx := context.Background()

	// Open first stream
	stream1, err := gClient.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Failed to create Subscribe stream #1: %v", err)
	}

	req1 := &pb.SubscribeRequest{
		Request: &pb.SubscribeRequest_Subscribe{
			Subscribe: &pb.SubscriptionList{
				Prefix: &pb.Path{Target: "STATE_DB"},
				Mode:   pb.SubscriptionList_POLL,
				Subscription: []*pb.Subscription{
					{
						Path: &pb.Path{
							Elem: []*pb.PathElem{
								{Name: "NEIGH_STATE_TABLE"},
							},
						},
					},
				},
			},
		},
	}
	if err := stream1.Send(req1); err != nil {
		t.Fatalf("Failed to send SubscribeRequest on stream1: %v", err)
	}
	t.Logf("Sent SubscribeRequest on stream #1 for STATE_DB")

	// Drain stream1's initial responses (update + sync) so the buffer is empty
	for {
		resp, err := stream1.Recv()
		if err != nil {
			t.Fatalf("Unexpected error draining stream1: %v", err)
		}
		if resp.GetSyncResponse() {
			t.Logf("Stream1 initial sync received, buffer drained")
			break
		}
	}

	// Verify 1 active client
	s.cMu.Lock()
	clientsBefore := len(s.clients)
	s.cMu.Unlock()
	t.Logf("Active clients before stream2: %d", clientsBefore)

	if clientsBefore != 1 {
		t.Errorf("Expected 1 active client before stream2, got %d", clientsBefore)
	}

	// Open second stream from the same connection — should close the first
	stream2, err := gClient.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Failed to create Subscribe stream #2: %v", err)
	}

	req2 := &pb.SubscribeRequest{
		Request: &pb.SubscribeRequest_Subscribe{
			Subscribe: &pb.SubscriptionList{
				Prefix: &pb.Path{Target: "STATE_DB"},
				Mode:   pb.SubscriptionList_POLL,
				Subscription: []*pb.Subscription{
					{
						Path: &pb.Path{
							Elem: []*pb.PathElem{
								{Name: "NEIGH_STATE_TABLE"},
							},
						},
					},
				},
			},
		},
	}
	if err := stream2.Send(req2); err != nil {
		t.Fatalf("Failed to send SubscribeRequest on stream2: %v", err)
	}
	t.Logf("Sent SubscribeRequest on stream #2 for STATE_DB")

	// Wait for stream2 to be registered and stream1 to be closed
	time.Sleep(time.Millisecond * 500)

	// Stream1 should have been closed by the duplicate detection.
	// Buffer is empty (we drained it), so Recv should return an error.
	_, err = stream1.Recv()
	if err == nil {
		t.Errorf("Expected stream1 to be closed by duplicate detection, but Recv succeeded")
	} else {
		t.Logf("Stream1 correctly closed: %v", err)
	}

	stream2.CloseSend()
	t.Logf("SUCCESS: Legacy duplicate-close behavior works when EnableStreamMultiplexing is disabled")
}
