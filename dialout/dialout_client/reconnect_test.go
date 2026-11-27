package telemetry_dialout

// This file covers the destination-group publish and reconnection logic in
// dialout_client.go.
//
// The tests are hermetic: a local TLS gRPC dial-out server is started on
// 127.0.0.1:0 and fake spb.GNMIDialOut_PublishClient / sdc.Client
// implementations are used for the individual branches, so neither a
// redis-server nor a real collector is required. Redis backed configuration
// (DialOutRun) is out of scope here; the unit level entry points are exercised
// directly instead.
//
// Every wait is bounded (rcTestWaitTimeout) so that a regression cannot hang
// the CI test run, and every goroutine started by the code under test is
// joined before a test returns.

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Workiva/go-datastructures/queue"
	"github.com/agiledragon/gomonkey/v2"
	gpb "github.com/openconfig/gnmi/proto/gnmi"
	spb "github.com/sonic-net/sonic-gnmi/proto"
	sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
	testcert "github.com/sonic-net/sonic-gnmi/testdata/tls"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
)

const (
	// rcTestWaitTimeout bounds every wait in this file: a failing condition
	// fails the test instead of hanging the -timeout 180s CI job.
	rcTestWaitTimeout = 10 * time.Second
)

// -----------------------------------------------------------------------------
// tests
// -----------------------------------------------------------------------------

func TestRcClientSubscriptionString(t *testing.T) {
	cs := rcTestNewSubscription()
	cs.interval = 20 * time.Millisecond
	cs.sendMsg = 3
	cs.recvMsg = 4

	got := cs.String()
	for _, want := range []string{
		"rcTest-sub:rcTest-group",
		"interval 20ms",
		"sendMsg 3",
		"recvMsg 4",
		"rcTest-target",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("clientSubscription.String() = %q, want it to contain %q", got, want)
		}
	}
}

func TestRcNewClient(t *testing.T) {
	rcTestSaveGlobals(t)

	t.Run("connects to a tls destination", func(t *testing.T) {
		srv := &rcTestDialOutServer{}
		addr := rcTestStartDialOutServer(t, srv)
		clientCfg = rcTestTLSClientConfig(2 * time.Second)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		c, err := newClient(ctx, addr)
		if err != nil {
			t.Fatalf("newClient(%q) returned error: %v", addr, err)
		}
		if c == nil {
			t.Fatal("newClient returned a nil client")
		}
		if c.dest != addr {
			t.Fatalf("newClient dest = %q, want %q", c.dest, addr)
		}
		if c.client == nil {
			t.Fatal("newClient returned a client without GNMIDialOutClient")
		}
		// Client.Close closes the underlying connection. It does not reset the
		// connection field, so a second call forwards to the already closed
		// grpc connection and reports ErrClientConnClosing; that quirk is only
		// logged here, the test just makes sure Close never panics.
		if err := c.Close(); err != nil {
			t.Fatalf("Client.Close() returned error: %v", err)
		}
		if err := c.Close(); err != nil {
			t.Logf("second Client.Close() returned the documented grpc error: %v", err)
		}
		// A client without a connection is a no-op.
		empty := &Client{}
		if err := empty.Close(); err != nil {
			t.Fatalf("Client.Close() without a connection returned error: %v", err)
		}
	})

	t.Run("dial failure", func(t *testing.T) {
		clientCfg = rcTestTLSClientConfig(100 * time.Millisecond)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		start := time.Now()
		c, err := newClient(ctx, "127.0.0.1:1")
		if err == nil {
			_ = c.Close()
			t.Fatal("newClient to a refused port returned no error")
		}
		if c != nil {
			t.Fatalf("newClient returned client %v together with error %v", c, err)
		}
		if !strings.Contains(err.Error(), "Dial to (127.0.0.1:1") {
			t.Fatalf("unexpected dial error, want it to mention the destination: %v", err)
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Fatalf("dial failure took %v, expected it to be bounded by RetryInterval", elapsed)
		}
	})

	t.Run("missing transport security", func(t *testing.T) {
		clientCfg = &ClientConfig{RetryInterval: time.Second}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		c, err := newClient(ctx, "127.0.0.1:1")
		if err == nil {
			_ = c.Close()
			t.Fatal("newClient without TLS credentials returned no error")
		}
		if c != nil {
			t.Fatalf("newClient returned client %v together with error %v", c, err)
		}
		if !strings.Contains(err.Error(), "no transport security") {
			t.Fatalf("unexpected error for a client without transport credentials: %v", err)
		}
	})
}

func TestRcClientSubscriptionClose(t *testing.T) {
	t.Run("not opened", func(t *testing.T) {
		cs := rcTestNewSubscription()
		cs.Close()
		if cs.opened {
			t.Fatal("Close() marked an unopened subscription as opened")
		}
	})

	t.Run("opened with clients", func(t *testing.T) {
		cs := rcTestNewSubscription()
		cs.opened = true
		cs.stop = make(chan struct{})
		cs.q = queue.NewPriorityQueue(1, false)
		cs.clients = map[string]*Client{
			"rcTest-a": {dest: "rcTest-a"},
			"rcTest-b": {dest: "rcTest-b"},
		}

		cs.Close()

		select {
		case <-cs.stop:
		default:
			t.Fatal("Close() did not close the stop channel")
		}
		if !cs.q.Disposed() {
			t.Fatal("Close() did not dispose the priority queue")
		}
		if len(cs.clients) != 0 {
			t.Fatalf("Close() left %d clients behind", len(cs.clients))
		}
		if cs.opened {
			t.Fatal("Close() did not clear the opened flag")
		}

		// Closing an already closed subscription is a no-op.
		cs.Close()
	})

	t.Run("opened with nil channels", func(t *testing.T) {
		cs := &clientSubscription{opened: true}
		cs.Close()
		if cs.opened {
			t.Fatal("Close() did not clear the opened flag")
		}
		if cs.clients == nil {
			t.Fatal("Close() did not reset the client map")
		}
	})
}

func TestRcHandleNetworkChange(t *testing.T) {
	rcTestSaveGlobals(t)

	networkChangeTrigger = make(chan struct{}, 16)
	stop := make(chan struct{})
	cs := &clientSubscription{
		name:    "rcTest-handled",
		opened:  true,
		stop:    stop,
		q:       queue.NewPriorityQueue(1, false),
		clients: map[string]*Client{},
		cancel:  func() {},
	}
	oldSubs := ClientSubscriptionNameMap
	ClientSubscriptionNameMap = map[string]*clientSubscription{"rcTest-handled": cs}
	t.Cleanup(func() {
		ClientSubscriptionNameMap = oldSubs
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		handleNetworkChange(ctx)
		close(done)
	}()

	networkChangeTrigger <- struct{}{}

	// The subscription is closed and its context recreated: NewInstance then
	// fails because no destination group is configured, which is logged and
	// ignored by handleNetworkChange.
	select {
	case <-stop:
	case <-time.After(rcTestWaitTimeout):
		t.Fatalf("timed out after %v waiting for the network change to close the subscription", rcTestWaitTimeout)
	}
	if !cs.q.Disposed() {
		t.Fatal("network change handling did not dispose the subscription queue")
	}

	cancel()
	rcTestWaitDone(t, "handleNetworkChange to return", done)
}

func TestRcSend(t *testing.T) {
	t.Run("sends update with device info", func(t *testing.T) {
		rcTestStubDeviceInfo(t)
		cs := rcTestNewSubscription()
		cs.q = queue.NewPriorityQueue(1, false)
		stream := &rcTestPublishStream{}
		client := &Client{dest: "rcTest-dest"}

		errCh := make(chan error, 1)
		go func() {
			errCh <- cs.send(stream, client)
		}()

		if err := cs.q.Put(rcTestValue(2 * time.Second)); err != nil {
			t.Fatalf("queue put failed: %v", err)
		}
		rcTestWait(t, "the update to be sent", func() bool {
			return stream.sentCount() >= 1
		})

		resp := stream.sentAt(0)
		if resp == nil {
			t.Fatal("first sent response is nil")
		}
		if resp.GetSyncResponse() {
			t.Fatalf("update was sent as a sync response: %v", resp)
		}
		notif := resp.GetUpdate()
		if notif == nil {
			t.Fatalf("first sent response is not an update: %v", resp)
		}
		if got, want := notif.GetTimestamp(), int64(2*time.Second); got != want {
			t.Fatalf("notification timestamp = %d, want %d", got, want)
		}
		if len(notif.GetUpdate()) != 1 {
			t.Fatalf("notification has %d updates, want 1", len(notif.GetUpdate()))
		}
		elems := notif.GetUpdate()[0].GetPath().GetElem()
		if len(elems) != 2 {
			t.Fatalf("path has %d elements %v, want 2 (original path plus DeviceInfo)", len(elems), elems)
		}
		if got := elems[1].GetName(); got != "DeviceInfo" {
			t.Fatalf("last path element is %q, want %q", got, "DeviceInfo")
		}
		if got := elems[1].GetKey()["HostName"]; got != "rcTest-host" {
			t.Fatalf("DeviceInfo HostName = %q, want %q", got, "rcTest-host")
		}
		if got := elems[1].GetKey()["MgmtIp"]; got != "192.0.2.42" {
			t.Fatalf("DeviceInfo MgmtIp = %q, want %q", got, "192.0.2.42")
		}

		// A sync response is forwarded as is and keeps its own shape.
		if err := cs.q.Put(rcTestSyncValue()); err != nil {
			t.Fatalf("queue put failed: %v", err)
		}
		rcTestWait(t, "the sync response to be sent", func() bool {
			return stream.sentCount() >= 2
		})
		if resp := stream.sentAt(1); resp == nil || !resp.GetSyncResponse() {
			t.Fatalf("second sent response is not a sync response: %v", resp)
		}

		cs.q.Dispose()
		rcTestExpectDisposed(t, errCh)
		if cs.sendMsg != 2 || client.sendMsg != 2 {
			t.Fatalf("send counters are cs.sendMsg=%d client.sendMsg=%d, want 2/2", cs.sendMsg, client.sendMsg)
		}
		if cs.errors != 0 {
			t.Fatalf("cs.errors = %d, want 0", cs.errors)
		}
	})

	t.Run("sends delete without device info", func(t *testing.T) {
		rcTestStubDeviceInfo(t)
		cs := rcTestNewSubscription()
		cs.q = queue.NewPriorityQueue(1, false)
		stream := &rcTestPublishStream{}
		client := &Client{dest: "rcTest-dest"}

		errCh := make(chan error, 1)
		go func() {
			errCh <- cs.send(stream, client)
		}()
		if err := cs.q.Put(rcTestDeleteValue()); err != nil {
			t.Fatalf("queue put failed: %v", err)
		}
		rcTestWait(t, "the delete to be sent", func() bool {
			return stream.sentCount() >= 1
		})

		notif := stream.sentAt(0).GetUpdate()
		if notif == nil {
			t.Fatalf("delete was not sent as an update: %v", stream.sentAt(0))
		}
		if len(notif.GetDelete()) != 1 {
			t.Fatalf("notification has %d deletes, want 1", len(notif.GetDelete()))
		}
		if notif.GetUpdate() != nil {
			t.Fatalf("delete notification unexpectedly has updates: %v", notif.GetUpdate())
		}

		cs.q.Dispose()
		rcTestExpectDisposed(t, errCh)
	})

	t.Run("send error", func(t *testing.T) {
		rcTestStubDeviceInfo(t)
		cs := rcTestNewSubscription()
		cs.q = queue.NewPriorityQueue(1, false)
		stream := &rcTestPublishStream{sendFn: func(*gpb.SubscribeResponse) error {
			return errors.New("rcTest send failure")
		}}
		client := &Client{dest: "rcTest-dest"}

		errCh := make(chan error, 1)
		go func() {
			errCh <- cs.send(stream, client)
		}()
		if err := cs.q.Put(rcTestValue(0)); err != nil {
			t.Fatalf("queue put failed: %v", err)
		}

		select {
		case err := <-errCh:
			if err == nil || !strings.Contains(err.Error(), "rcTest send failure") {
				t.Fatalf("send returned %v, want the stream send error", err)
			}
		case <-time.After(rcTestWaitTimeout):
			t.Fatalf("timed out after %v waiting for send to return", rcTestWaitTimeout)
		}
		if cs.errors != 1 {
			t.Fatalf("cs.errors = %d, want 1", cs.errors)
		}
		if stream.sentCount() != 0 {
			t.Fatalf("stream recorded %d successfully sent responses, want 0", stream.sentCount())
		}
	})

	t.Run("fatal value", func(t *testing.T) {
		rcTestStubDeviceInfo(t)
		cs := rcTestNewSubscription()
		cs.q = queue.NewPriorityQueue(1, false)
		stream := &rcTestPublishStream{}
		client := &Client{dest: "rcTest-dest"}

		errCh := make(chan error, 1)
		go func() {
			errCh <- cs.send(stream, client)
		}()
		if err := cs.q.Put(rcTestFatalValue("rcTest fatal")); err != nil {
			t.Fatalf("queue put failed: %v", err)
		}

		select {
		case err := <-errCh:
			if err == nil || !strings.Contains(err.Error(), "rcTest fatal") {
				t.Fatalf("send returned %v, want the fatal value error", err)
			}
		case <-time.After(rcTestWaitTimeout):
			t.Fatalf("timed out after %v waiting for send to return", rcTestWaitTimeout)
		}
		if cs.errors != 1 {
			t.Fatalf("cs.errors = %d, want 1", cs.errors)
		}
		if stream.sentCount() != 0 {
			t.Fatalf("stream recorded %d sent responses, want 0", stream.sentCount())
		}
	})

	t.Run("unknown queue item", func(t *testing.T) {
		cs := rcTestNewSubscription()
		cs.q = queue.NewPriorityQueue(1, false)
		// The unknown item is pushed before the consumer starts so that the
		// queue never has to compare it with a sdc.Value.
		if err := cs.q.Put(rcTestQueueItem{ts: 1}); err != nil {
			t.Fatalf("queue put failed: %v", err)
		}
		stream := &rcTestPublishStream{}
		client := &Client{dest: "rcTest-dest"}

		errCh := make(chan error, 1)
		go func() {
			errCh <- cs.send(stream, client)
		}()
		rcTestWait(t, "the unknown item to be consumed", func() bool {
			return cs.errors >= 1
		})

		cs.q.Dispose()
		rcTestExpectDisposed(t, errCh)
		if stream.sentCount() != 0 {
			t.Fatalf("stream recorded %d sent responses for an unknown item, want 0", stream.sentCount())
		}
	})

	t.Run("metadata update throttling", func(t *testing.T) {
		rcTestStubDeviceInfo(t)
		cs := rcTestNewSubscription()
		cs.name = "METADATA-rcTest-sub"
		cs.q = queue.NewPriorityQueue(1, false)
		stream := &rcTestPublishStream{}
		client := &Client{dest: "rcTest-dest"}

		errCh := make(chan error, 1)
		go func() {
			errCh <- cs.send(stream, client)
		}()

		// First update sets the reference time, the second one is below the 10s
		// threshold and the third one exceeds it.
		for _, ts := range []time.Duration{2 * time.Second, 4 * time.Second, 30 * time.Second} {
			if err := cs.q.Put(rcTestValue(ts)); err != nil {
				t.Fatalf("queue put failed: %v", err)
			}
		}
		// A sync response has no update and is only logged.
		if err := cs.q.Put(rcTestSyncValue()); err != nil {
			t.Fatalf("queue put failed: %v", err)
		}

		rcTestWait(t, "all metadata responses to be sent", func() bool {
			return stream.sentCount() >= 4
		})
		cs.q.Dispose()
		rcTestExpectDisposed(t, errCh)
		if cs.sendMsg != 4 || client.sendMsg != 4 {
			t.Fatalf("send counters are cs.sendMsg=%d client.sendMsg=%d, want 4/4", cs.sendMsg, client.sendMsg)
		}
		if cs.errors != 0 {
			t.Fatalf("cs.errors = %d, want 0", cs.errors)
		}
	})
}

func TestRcStreamSend(t *testing.T) {
	t.Run("broadcasts to every client", func(t *testing.T) {
		rcTestStubDeviceInfo(t)
		cs := rcTestNewSubscription()
		cs.reportType = Stream
		cs.q = queue.NewPriorityQueue(1, false)
		s1, s2 := &rcTestPublishStream{}, &rcTestPublishStream{}
		cs.clients = map[string]*Client{
			"rcTest-a": {dest: "rcTest-a", publish: s1},
			"rcTest-b": {dest: "rcTest-b", publish: s2},
		}
		failedAddrs := map[string]bool{}
		reconnectTrigger := make(chan string, 16)

		errCh := make(chan error, 1)
		go func() {
			errCh <- cs.streamSend(failedAddrs, reconnectTrigger)
		}()
		if err := cs.q.Put(rcTestValue(2 * time.Second)); err != nil {
			t.Fatalf("queue put failed: %v", err)
		}
		rcTestWait(t, "the update to be broadcast to both clients", func() bool {
			return s1.sentCount() >= 1 && s2.sentCount() >= 1
		})

		for addr, stream := range map[string]*rcTestPublishStream{"rcTest-a": s1, "rcTest-b": s2} {
			notif := stream.sentAt(0).GetUpdate()
			if notif == nil {
				t.Fatalf("client %s did not receive an update: %v", addr, stream.sentAt(0))
			}
			elems := notif.GetUpdate()[0].GetPath().GetElem()
			if len(elems) != 2 || elems[1].GetName() != "DeviceInfo" {
				t.Fatalf("client %s received path %v, want the original path plus DeviceInfo", addr, elems)
			}
		}

		cs.q.Dispose()
		rcTestExpectDisposed(t, errCh)
		if cs.sendMsg != 2 {
			t.Fatalf("cs.sendMsg = %d, want 2", cs.sendMsg)
		}
		if len(failedAddrs) != 0 {
			t.Fatalf("failedAddrs = %v, want it to stay empty", failedAddrs)
		}
	})

	t.Run("marks failing destination", func(t *testing.T) {
		rcTestStubDeviceInfo(t)
		cs := rcTestNewSubscription()
		cs.reportType = Stream
		cs.q = queue.NewPriorityQueue(1, false)
		good := &rcTestPublishStream{}
		bad := &rcTestPublishStream{sendFn: func(*gpb.SubscribeResponse) error {
			return errors.New("rcTest stream failure")
		}}
		cs.clients = map[string]*Client{
			"rcTest-good": {dest: "rcTest-good", publish: good},
			"rcTest-bad":  {dest: "rcTest-bad", publish: bad},
		}
		failedAddrs := map[string]bool{}
		reconnectTrigger := make(chan string, 16)

		errCh := make(chan error, 1)
		go func() {
			errCh <- cs.streamSend(failedAddrs, reconnectTrigger)
		}()
		if err := cs.q.Put(rcTestValue(2 * time.Second)); err != nil {
			t.Fatalf("queue put failed: %v", err)
		}
		rcTestWait(t, "the update to be sent to the healthy client", func() bool {
			return good.sentCount() >= 1
		})

		cs.q.Dispose()
		rcTestExpectDisposed(t, errCh)

		if !failedAddrs["rcTest-bad"] {
			t.Fatalf("failedAddrs = %v, want the failing destination to be marked", failedAddrs)
		}
		if failedAddrs["rcTest-good"] {
			t.Fatalf("failedAddrs = %v, want the healthy destination not to be marked", failedAddrs)
		}
		if cs.errors != 1 {
			t.Fatalf("cs.errors = %d, want 1", cs.errors)
		}
		select {
		case addr := <-reconnectTrigger:
			if addr != "rcTest-bad" {
				t.Fatalf("reconnection was triggered for %q, want %q", addr, "rcTest-bad")
			}
		default:
			t.Fatal("failing destination did not trigger a reconnection")
		}
	})

	t.Run("metadata logging", func(t *testing.T) {
		rcTestStubDeviceInfo(t)
		cs := rcTestNewSubscription()
		cs.name = "METADATA-rcTest-stream"
		cs.reportType = Stream
		cs.q = queue.NewPriorityQueue(1, false)
		s1, s2 := &rcTestPublishStream{}, &rcTestPublishStream{}
		cs.clients = map[string]*Client{
			"rcTest-a": {dest: "rcTest-a", publish: s1},
			"rcTest-b": {dest: "rcTest-b", publish: s2},
		}
		failedAddrs := map[string]bool{}
		reconnectTrigger := make(chan string, 16)

		errCh := make(chan error, 1)
		go func() {
			errCh <- cs.streamSend(failedAddrs, reconnectTrigger)
		}()

		// update below the threshold, sync response (no update) and update above
		// the threshold.
		for _, item := range []queue.Item{
			rcTestValue(2 * time.Second),
			rcTestSyncValue(),
			rcTestValue(30 * time.Second),
		} {
			if err := cs.q.Put(item); err != nil {
				t.Fatalf("queue put failed: %v", err)
			}
		}
		// Both clients receive all three items; the sync response only skips the
		// metadata logging block for each of them (update == nil).
		rcTestWait(t, "all metadata responses to be sent", func() bool {
			return s1.sentCount()+s2.sentCount() >= 6
		})

		cs.q.Dispose()
		rcTestExpectDisposed(t, errCh)
		if got := s1.sentCount() + s2.sentCount(); got != 6 {
			t.Fatalf("%d responses were sent, want 6", got)
		}
		if cs.sendMsg != 6 {
			t.Fatalf("cs.sendMsg = %d, want 6", cs.sendMsg)
		}
		if cs.errors != 0 {
			t.Fatalf("cs.errors = %d, want 0", cs.errors)
		}
	})

	t.Run("unknown queue item", func(t *testing.T) {
		cs := rcTestNewSubscription()
		cs.reportType = Stream
		cs.q = queue.NewPriorityQueue(1, false)
		if err := cs.q.Put(rcTestQueueItem{ts: 1}); err != nil {
			t.Fatalf("queue put failed: %v", err)
		}
		stream := &rcTestPublishStream{}
		cs.clients = map[string]*Client{"rcTest-a": {dest: "rcTest-a", publish: stream}}
		failedAddrs := map[string]bool{}
		reconnectTrigger := make(chan string, 16)

		errCh := make(chan error, 1)
		go func() {
			errCh <- cs.streamSend(failedAddrs, reconnectTrigger)
		}()
		rcTestWait(t, "the unknown item to be consumed", func() bool {
			return cs.errors >= 1
		})
		cs.q.Dispose()
		rcTestExpectDisposed(t, errCh)
		if stream.sentCount() != 0 {
			t.Fatalf("client received %d responses for an unknown item, want 0", stream.sentCount())
		}
	})

	t.Run("reconnect trigger unavailable", func(t *testing.T) {
		rcTestStubDeviceInfo(t)
		cs := rcTestNewSubscription()
		cs.reportType = Stream
		cs.q = queue.NewPriorityQueue(1, false)
		bad := &rcTestPublishStream{sendFn: func(*gpb.SubscribeResponse) error {
			return errors.New("rcTest stream failure")
		}}
		cs.clients = map[string]*Client{"rcTest-bad": {dest: "rcTest-bad", publish: bad}}
		failedAddrs := map[string]bool{}

		// A nil trigger channel can never accept a value, so the non blocking
		// send has to fall through to its default branch.
		errCh := make(chan error, 1)
		go func() {
			errCh <- cs.streamSend(failedAddrs, nil)
		}()
		if err := cs.q.Put(rcTestValue(2 * time.Second)); err != nil {
			t.Fatalf("queue put failed: %v", err)
		}
		rcTestWait(t, "the send failure to be recorded", func() bool {
			cs.cMu.Lock()
			defer cs.cMu.Unlock()
			return cs.errors >= 1
		})

		cs.q.Dispose()
		rcTestExpectDisposed(t, errCh)
		if !failedAddrs["rcTest-bad"] {
			t.Fatalf("failedAddrs = %v, want the failing destination to be marked", failedAddrs)
		}
	})
}

func TestRcAttemptReconnection(t *testing.T) {
	rcTestSaveGlobals(t)

	t.Run("dial failure keeps the destination failed", func(t *testing.T) {
		clientCfg = &ClientConfig{RetryInterval: 100 * time.Millisecond}
		cs := rcTestNewSubscription()
		cs.clients = map[string]*Client{}
		failedAddrs := map[string]bool{"127.0.0.1:1": true}

		attemptReconnection(context.Background(), cs, "127.0.0.1:1", &failedAddrs, make(chan string, 1))

		if len(cs.clients) != 0 {
			t.Fatalf("reconnection registered %d clients, want 0", len(cs.clients))
		}
		if !failedAddrs["127.0.0.1:1"] {
			t.Fatalf("failedAddrs = %v, want the destination to stay marked", failedAddrs)
		}
	})

	t.Run("stream client is registered", func(t *testing.T) {
		srv := &rcTestDialOutServer{}
		addr := rcTestStartDialOutServer(t, srv)
		clientCfg = rcTestTLSClientConfig(2 * time.Second)

		cs := rcTestNewSubscription()
		cs.reportType = Stream
		cs.opened = true
		cs.stop = make(chan struct{})
		cs.q = queue.NewPriorityQueue(1, false)
		cs.clients = map[string]*Client{}
		failedAddrs := map[string]bool{addr: true}

		attemptReconnection(context.Background(), cs, addr, &failedAddrs, make(chan string, 1))

		if _, ok := cs.clients[addr]; !ok {
			t.Fatalf("reconnected clients = %v, want %q to be registered", cs.clients, addr)
		}
		if failedAddrs[addr] {
			t.Fatalf("failedAddrs = %v, want %q to be cleared", failedAddrs, addr)
		}
		rcTestWait(t, "the collector to accept the publish stream", func() bool {
			return srv.pubCount() >= 1
		})
		// Stream mode starts no sender goroutine.
		if rcTestWaitGroupDone(&cs.w) == false {
			t.Fatal("stream reconnection started a sender goroutine")
		}
		cs.Close()
	})

	t.Run("periodic sender is started and cleaned up", func(t *testing.T) {
		srv := &rcTestDialOutServer{}
		addr := rcTestStartDialOutServer(t, srv)
		clientCfg = rcTestTLSClientConfig(2 * time.Second)

		cs := rcTestNewSubscription()
		cs.reportType = Periodic
		cs.opened = true
		cs.stop = make(chan struct{})
		cs.q = queue.NewPriorityQueue(1, false)
		cs.clients = map[string]*Client{}
		failedAddrs := map[string]bool{addr: true}
		reconnectTrigger := make(chan string, 16)

		attemptReconnection(context.Background(), cs, addr, &failedAddrs, reconnectTrigger)

		rcTestWait(t, "the reconnected client to be registered", func() bool {
			cs.cMu.Lock()
			defer cs.cMu.Unlock()
			_, ok := cs.clients[addr]
			return ok
		})
		if failedAddrs[addr] {
			t.Fatalf("failedAddrs = %v, want %q to be cleared", failedAddrs, addr)
		}

		// The sender blocks on the empty queue; disposing it makes the sender
		// take its cleanup path.
		cs.q.Dispose()
		rcTestWaitGroup(t, "the reconnected periodic sender", &cs.w)

		if got := rcTestClientCount(cs); got != 0 {
			t.Fatalf("%d clients left after the sender exited, want 0", got)
		}
		if !failedAddrs[addr] {
			t.Fatalf("failedAddrs = %v, want %q to be marked again", failedAddrs, addr)
		}
		select {
		case got := <-reconnectTrigger:
			if got != addr {
				t.Fatalf("reconnection was triggered for %q, want %q", got, addr)
			}
		default:
			t.Fatal("sender exit did not trigger a reconnection")
		}
		cs.Close()
	})
}

func TestRcPublishRunPeriodic(t *testing.T) {
	rcTestSaveGlobals(t)
	rcTestStubDeviceInfo(t)

	newRetryInterval = time.Hour
	clientCfg = rcTestTLSClientConfig(2 * time.Second)

	srv := &rcTestDialOutServer{}
	addr := rcTestStartDialOutServer(t, srv)

	dc := &rcTestDataClient{values: []*spb.Value{rcTestSpbValue(2 * time.Second)}}
	cs := &clientSubscription{
		name:          "rcTest-periodic",
		destGroupName: "rcTest-group",
		reportType:    Periodic,
		interval:      15 * time.Millisecond,
		dc:            dc,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := rcTestStartPublishRun(t, ctx, cs, []Destination{{Addrs: addr}})

	rcTestWait(t, "periodic updates to reach the collector", func() bool {
		return srv.recvCount() >= 2
	})
	if got := dc.getCalls(); got < 2 {
		t.Fatalf("data client was polled %d times, want at least 2", got)
	}

	rcTestStopSubscription(t, cs, done)

	if got := rcTestClientCount(cs); got != 0 {
		t.Fatalf("%d clients left after Close(), want 0", got)
	}
}

func TestRcPublishRunNetworkChange(t *testing.T) {
	rcTestSaveGlobals(t)
	rcTestStubDeviceInfo(t)

	newRetryInterval = time.Hour
	networkChangeTrigger = make(chan struct{}, 16)
	clientCfg = rcTestTLSClientConfig(2 * time.Second)

	srv := &rcTestDialOutServer{}
	addr := rcTestStartDialOutServer(t, srv)

	dc := &rcTestDataClient{values: []*spb.Value{rcTestSpbValue(2 * time.Second)}}
	cs := &clientSubscription{
		name:          "rcTest-netchange",
		destGroupName: "rcTest-group",
		reportType:    Periodic,
		interval:      15 * time.Millisecond,
		dc:            dc,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := rcTestStartPublishRun(t, ctx, cs, []Destination{{Addrs: addr}})

	rcTestWait(t, "the initial publish stream", func() bool {
		return srv.pubCount() >= 1
	})

	// A hostname/mgmt IP change reconnects every destination of the client.
	networkChangeTrigger <- struct{}{}
	rcTestWait(t, "the collector to be reconnected after a network change", func() bool {
		return srv.pubCount() >= 2
	})

	rcTestStopSubscription(t, cs, done)
}

func TestRcPublishRunDataReadError(t *testing.T) {
	rcTestSaveGlobals(t)
	rcTestStubDeviceInfo(t)

	newRetryInterval = time.Hour
	clientCfg = &ClientConfig{RetryInterval: time.Second}

	dc := &rcTestDataClient{getErr: errors.New("rcTest data read failure")}
	cs := &clientSubscription{
		name:          "rcTest-data-error",
		destGroupName: "rcTest-group",
		reportType:    Periodic,
		interval:      5 * time.Millisecond,
		dc:            dc,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := rcTestStartPublishRun(t, ctx, cs, nil)

	rcTestWait(t, "the data read error branch to be taken", func() bool {
		return dc.getCalls() >= 3
	})
	rcTestStopSubscription(t, cs, done)
}

func TestRcPublishRunConnectionFailure(t *testing.T) {
	rcTestSaveGlobals(t)
	rcTestStubDeviceInfo(t)

	newRetryInterval = time.Hour
	// Without transport credentials newClient fails immediately, so the
	// connection failure branch runs before the Once publishRun returns.
	clientCfg = &ClientConfig{RetryInterval: time.Second}

	dc := &rcTestDataClient{}
	cs := &clientSubscription{
		name:          "rcTest-dial-failure",
		destGroupName: "rcTest-group",
		reportType:    Once,
		interval:      10 * time.Millisecond,
		dc:            dc,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := rcTestStartPublishRun(t, ctx, cs, []Destination{{Addrs: "127.0.0.1:1"}})

	// Once is not published, so publishRun logs the unsupported report type and
	// returns after its initial grace period.
	rcTestWaitDone(t, "publishRun for an unsupported report type to return", done)

	cs.Close()
	rcTestWaitGroup(t, "publishRun goroutines", &cs.w)
	if got := rcTestClientCount(cs); got != 0 {
		t.Fatalf("%d clients left after a failed connection attempt, want 0", got)
	}
}

func TestRcPublishRunStream(t *testing.T) {
	rcTestSaveGlobals(t)
	rcTestStubDeviceInfo(t)

	newRetryInterval = time.Hour
	clientCfg = rcTestTLSClientConfig(2 * time.Second)

	srv := &rcTestDialOutServer{}
	addr := rcTestStartDialOutServer(t, srv)

	dc := &rcTestDataClient{}
	dc.streamRun = func(q *queue.PriorityQueue, stop chan struct{}, w *sync.WaitGroup) {
		defer w.Done()
		_ = q.Put(rcTestValue(2 * time.Second))
		<-stop
	}
	cs := &clientSubscription{
		name:          "rcTest-stream",
		destGroupName: "rcTest-group",
		reportType:    Stream,
		interval:      10 * time.Millisecond,
		dc:            dc,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := rcTestStartPublishRun(t, ctx, cs, []Destination{{Addrs: addr}})

	rcTestWait(t, "the stream update to reach the collector", func() bool {
		return srv.recvCount() >= 1
	})

	// Disposing the queue makes streamSend fail; publishRun logs the error and
	// returns without restarting the stream.
	cs.cMu.Lock()
	q := cs.q
	cs.cMu.Unlock()
	if q == nil {
		t.Fatal("publishRun did not create a queue")
	}
	q.Dispose()

	rcTestWaitDone(t, "publishRun to return after a stream send error", done)

	// Close() releases the data client streaming goroutine.
	cs.Close()
	rcTestWaitGroup(t, "publishRun goroutines", &cs.w)
}

// TestRcStreamSendNilResponse covers the defensive nil response check in
// streamSend. sdc.ValToResp never returns (nil, nil), so the branch is only
// reachable by patching the converter; the patch is installed last so that it
// cannot influence any other test.
func TestRcStreamSendNilResponse(t *testing.T) {
	rcTestStubDeviceInfo(t)

	patch := gomonkey.ApplyFunc(sdc.ValToResp, func(sdc.Value) (*gpb.SubscribeResponse, error) {
		return nil, nil
	})
	defer patch.Reset()

	cs := rcTestNewSubscription()
	cs.name = "METADATA-rcTest-nil-send"
	cs.reportType = Stream
	cs.q = queue.NewPriorityQueue(1, false)
	s1, s2 := &rcTestPublishStream{}, &rcTestPublishStream{}
	cs.clients = map[string]*Client{
		"rcTest-a": {dest: "rcTest-a", publish: s1},
		"rcTest-b": {dest: "rcTest-b", publish: s2},
	}
	failedAddrs := map[string]bool{}
	reconnectTrigger := make(chan string, 16)

	errCh := make(chan error, 1)
	go func() {
		errCh <- cs.streamSend(failedAddrs, reconnectTrigger)
	}()
	if err := cs.q.Put(rcTestValue(2 * time.Second)); err != nil {
		t.Fatalf("queue put failed: %v", err)
	}
	rcTestWait(t, "both clients to receive the nil response", func() bool {
		return s1.sentCount() >= 1 && s2.sentCount() >= 1
	})

	cs.q.Dispose()
	rcTestExpectDisposed(t, errCh)
	if got := cs.sendMsg; got != 2 {
		t.Fatalf("cs.sendMsg = %d, want 2 (both clients counted the response)", got)
	}
	// The patched converter returns a nil response, which is forwarded as is;
	// the metadata logging block then skips it instead of dereferencing it.
	for addr, stream := range map[string]*rcTestPublishStream{"rcTest-a": s1, "rcTest-b": s2} {
		if got := stream.sentAt(0); got != nil {
			t.Fatalf("client %s received %v, want the nil response from the patched converter", addr, got)
		}
	}
}

// TestRcAttemptReconnectionPublishFailure covers the publish error branch of
// attemptReconnection. The generated Publish client returns no error for a
// refused RPC, so newClient is patched instead. The patch is installed last so
// that it cannot influence any other test.
func TestRcAttemptReconnectionPublishFailure(t *testing.T) {
	rcTestSaveGlobals(t)

	patch := gomonkey.ApplyFunc(newClient, func(ctx context.Context, dest string) (*Client, error) {
		return &Client{
			dest: dest,
			client: &rcTestDialOutClient{publishFn: func(context.Context, ...grpc.CallOption) (spb.GNMIDialOut_PublishClient, error) {
				return nil, errors.New("rcTest publish failure")
			}},
		}, nil
	})
	defer patch.Reset()

	cs := rcTestNewSubscription()
	cs.clients = map[string]*Client{}
	failedAddrs := map[string]bool{"rcTest-addr": true}

	attemptReconnection(context.Background(), cs, "rcTest-addr", &failedAddrs, make(chan string, 1))

	if len(cs.clients) != 0 {
		t.Fatalf("reconnection registered %d clients after a publish failure, want 0", len(cs.clients))
	}
	if !failedAddrs["rcTest-addr"] {
		t.Fatalf("failedAddrs = %v, want the destination to stay marked", failedAddrs)
	}
}

func TestRcStreamSendConverterError(t *testing.T) {
	rcTestSaveGlobals(t)
	rcTestStubDeviceInfo(t)

	failure := errors.New("rcTest converter failure")
	patch := gomonkey.ApplyFunc(sdc.ValToResp, func(sdc.Value) (*gpb.SubscribeResponse, error) {
		return nil, failure
	})
	defer patch.Reset()

	cs := rcTestNewSubscription()
	cs.reportType = Stream
	cs.q = queue.NewPriorityQueue(1, false)
	stream := &rcTestPublishStream{}
	cs.clients = map[string]*Client{"rcTest-a": {dest: "rcTest-a", publish: stream}}

	errCh := make(chan error, 1)
	go func() {
		errCh <- cs.streamSend(map[string]bool{}, make(chan string, 1))
	}()
	if err := cs.q.Put(rcTestValue(2 * time.Second)); err != nil {
		t.Fatalf("queue put failed: %v", err)
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, failure) {
			t.Fatalf("streamSend returned %v, want the converter error %v", err, failure)
		}
	case <-time.After(rcTestWaitTimeout):
		t.Fatal("timed out waiting for the converter error to abort streamSend")
	}
	if got := stream.sentCount(); got != 0 {
		t.Fatalf("client received %d responses for a failed conversion, want 0", got)
	}
	cs.cMu.Lock()
	errs := cs.errors
	cs.cMu.Unlock()
	if errs != 1 {
		t.Fatalf("cs.errors = %d, want 1", errs)
	}
}

func TestRcPublishRunPublishFailure(t *testing.T) {
	rcTestSaveGlobals(t)
	rcTestStubDeviceInfo(t)

	// A short retry interval also exercises the periodic reconnection check of
	// the reconnection goroutine.
	newRetryInterval = 20 * time.Millisecond
	clientCfg = rcTestTLSClientConfig(time.Second)

	failure := errors.New("rcTest publish failure")
	var dials int32
	patch := gomonkey.ApplyFunc(newClient, func(_ context.Context, dest string) (*Client, error) {
		atomic.AddInt32(&dials, 1)
		return &Client{
			dest: dest,
			client: &rcTestDialOutClient{publishFn: func(context.Context, ...grpc.CallOption) (spb.GNMIDialOut_PublishClient, error) {
				return nil, failure
			}},
		}, nil
	})
	defer patch.Reset()

	cs := &clientSubscription{
		name:          "rcTest-publish-failure",
		destGroupName: "rcTest-group",
		reportType:    Periodic,
		interval:      10 * time.Millisecond,
		dc:            &rcTestDataClient{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := rcTestStartPublishRun(t, ctx, cs, []Destination{{Addrs: "rcTest-dest"}})

	// A failing Publish marks the destination as failed and triggers an
	// immediate reconnection, which dials the same destination again and fails
	// in the same way.
	// The destination is dialed for the initial connection, for the immediate
	// reconnection trigger and again by the periodic retry check.
	rcTestWait(t, "the publish failure to trigger reconnections", func() bool {
		return atomic.LoadInt32(&dials) >= 3
	})
	if got := rcTestClientCount(cs); got != 0 {
		t.Fatalf("%d clients registered for a destination whose Publish failed, want 0", got)
	}

	rcTestStopSubscription(t, cs, done)
}

func TestRcPublishRunPeriodicSendError(t *testing.T) {
	rcTestSaveGlobals(t)
	rcTestStubDeviceInfo(t)

	newRetryInterval = time.Hour
	clientCfg = rcTestTLSClientConfig(time.Second)

	failure := errors.New("rcTest periodic send failure")
	var sends int32
	patch := gomonkey.ApplyFunc(newClient, func(_ context.Context, dest string) (*Client, error) {
		return &Client{
			dest: dest,
			client: &rcTestDialOutClient{publishFn: func(context.Context, ...grpc.CallOption) (spb.GNMIDialOut_PublishClient, error) {
				return &rcTestPublishStream{sendFn: func(*gpb.SubscribeResponse) error {
					atomic.AddInt32(&sends, 1)
					return failure
				}}, nil
			}},
		}, nil
	})
	defer patch.Reset()

	cs := &clientSubscription{
		name:          "rcTest-send-error",
		destGroupName: "rcTest-group",
		reportType:    Periodic,
		interval:      10 * time.Millisecond,
		dc:            &rcTestDataClient{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := rcTestStartPublishRun(t, ctx, cs, []Destination{{Addrs: "rcTest-dest"}})

	// The periodic sender marks the destination as failed and triggers a
	// reconnection instead of retrying it on the same stream.
	rcTestWait(t, "the periodic send failure path", func() bool {
		return atomic.LoadInt32(&sends) >= 1
	})

	rcTestStopSubscription(t, cs, done)
}

// -----------------------------------------------------------------------------
// helpers
// -----------------------------------------------------------------------------

// rcTestSaveGlobals restores every package level value the tests in this file
// overwrite, even when a test fails early.
func rcTestSaveGlobals(t *testing.T) {
	t.Helper()
	oldTrigger := networkChangeTrigger
	oldRetry := newRetryInterval
	oldCfg := clientCfg
	oldGlobalDeviceInfo := globalDeviceInfo
	t.Cleanup(func() {
		networkChangeTrigger = oldTrigger
		newRetryInterval = oldRetry
		clientCfg = oldCfg
		globalDeviceInfo = oldGlobalDeviceInfo
	})
}

// rcTestStubDeviceInfo replaces the device info cache with a pre-initialized
// one so that addDeviceInfoToResp does not have to talk to redis.
func rcTestStubDeviceInfo(t *testing.T) {
	t.Helper()
	stub := &deviceInfoCache{
		hostname: "rcTest-host",
		mgmtIP:   "192.0.2.42",
		updateCh: make(chan struct{}, 16),
		stopCh:   make(chan struct{}),
	}
	// Mark the cache as initialized: Get() then only returns the fields above
	// instead of starting the redis backed monitoring.
	stub.initOnce.Do(func() {})
	globalDeviceInfo = stub
}

func rcTestTLSClientConfig(retryInterval time.Duration) *ClientConfig {
	return &ClientConfig{
		RetryInterval: retryInterval,
		TLS:           &tls.Config{InsecureSkipVerify: true},
	}
}

func rcTestNewSubscription() *clientSubscription {
	return &clientSubscription{
		name:          "rcTest-sub",
		destGroupName: "rcTest-group",
		prefix:        &gpb.Path{Target: "rcTest-target"},
		paths:         []*gpb.Path{{Elem: []*gpb.PathElem{{Name: "interfaces"}}}},
		reportType:    Periodic,
		interval:      20 * time.Millisecond,
	}
}

func rcTestWait(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(rcTestWaitTimeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %v waiting for %s", rcTestWaitTimeout, what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func rcTestWaitDone(t *testing.T, what string, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(rcTestWaitTimeout):
		t.Fatalf("timed out after %v waiting for %s", rcTestWaitTimeout, what)
	}
}

func rcTestWaitGroup(t *testing.T, what string, w *sync.WaitGroup) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		w.Wait()
		close(done)
	}()
	rcTestWaitDone(t, what, done)
}

// rcTestWaitGroupDone reports whether the WaitGroup is already idle without
// blocking.
func rcTestWaitGroupDone(w *sync.WaitGroup) bool {
	done := make(chan struct{})
	go func() {
		w.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(100 * time.Millisecond):
		return false
	}
}

// rcTestExpectDisposed waits for a queue consumer to return and checks that it
// stopped because the queue was disposed.
func rcTestExpectDisposed(t *testing.T, errCh <-chan error) {
	t.Helper()
	select {
	case err := <-errCh:
		if !errors.Is(err, queue.ErrDisposed) {
			t.Fatalf("queue consumer returned %v, want %v", err, queue.ErrDisposed)
		}
	case <-time.After(rcTestWaitTimeout):
		t.Fatalf("timed out after %v waiting for the queue consumer to return", rcTestWaitTimeout)
	}
}

func rcTestStartPublishRun(t *testing.T, ctx context.Context, cs *clientSubscription, dests []Destination) chan struct{} {
	t.Helper()
	done := make(chan struct{})
	go func() {
		publishRun(ctx, cs, dests)
		close(done)
	}()
	return done
}

// rcTestStopSubscription closes a subscription that was started with
// rcTestStartPublishRun and joins all goroutines it owns.
func rcTestStopSubscription(t *testing.T, cs *clientSubscription, done chan struct{}) {
	t.Helper()
	cs.Close()
	rcTestWaitDone(t, "publishRun to return", done)
	rcTestWaitGroup(t, "publishRun goroutines", &cs.w)
}

func rcTestClientCount(cs *clientSubscription) int {
	cs.cMu.Lock()
	defer cs.cMu.Unlock()
	return len(cs.clients)
}

// rcTestSpbValue builds a plain data value with the given timestamp (the
// notification timestamp is in nanoseconds).
func rcTestSpbValue(ts time.Duration) *spb.Value {
	return &spb.Value{
		Timestamp: int64(ts),
		Path:      &gpb.Path{Elem: []*gpb.PathElem{{Name: "interfaces"}}},
		Val:       &gpb.TypedValue{Value: &gpb.TypedValue_StringVal{StringVal: "rcTest-value"}},
	}
}

func rcTestValue(ts time.Duration) sdc.Value {
	return sdc.Value{Value: rcTestSpbValue(ts)}
}

func rcTestSyncValue() sdc.Value {
	return sdc.Value{Value: &spb.Value{SyncResponse: true}}
}

func rcTestDeleteValue() sdc.Value {
	return sdc.Value{Value: &spb.Value{
		Timestamp: int64(2 * time.Second),
		Delete:    []*gpb.Path{{Elem: []*gpb.PathElem{{Name: "interfaces"}}}},
	}}
}

func rcTestFatalValue(msg string) sdc.Value {
	return sdc.Value{Value: &spb.Value{Fatal: msg}}
}

// rcTestQueueItem is an item type that the queue consumers do not know about;
// it covers the default branch of their type switch.
type rcTestQueueItem struct {
	ts int64
}

func (i rcTestQueueItem) Compare(other queue.Item) int {
	o, ok := other.(rcTestQueueItem)
	if !ok {
		return 0
	}
	switch {
	case i.ts < o.ts:
		return -1
	case i.ts > o.ts:
		return 1
	default:
		return 0
	}
}

// rcTestPublishStream is a fake spb.GNMIDialOut_PublishClient.
type rcTestPublishStream struct {
	sendFn func(*gpb.SubscribeResponse) error

	mu   sync.Mutex
	sent []*gpb.SubscribeResponse
}

func (s *rcTestPublishStream) Send(m *gpb.SubscribeResponse) error {
	if s.sendFn != nil {
		if err := s.sendFn(m); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.sent = append(s.sent, m)
	s.mu.Unlock()
	return nil
}

func (s *rcTestPublishStream) Recv() (*spb.PublishResponse, error) {
	return nil, io.EOF
}

func (s *rcTestPublishStream) Context() context.Context     { return context.Background() }
func (s *rcTestPublishStream) Header() (metadata.MD, error) { return nil, nil }
func (s *rcTestPublishStream) Trailer() metadata.MD         { return nil }
func (s *rcTestPublishStream) CloseSend() error             { return nil }
func (s *rcTestPublishStream) SendMsg(interface{}) error    { return nil }
func (s *rcTestPublishStream) RecvMsg(interface{}) error    { return nil }

func (s *rcTestPublishStream) sentCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sent)
}

func (s *rcTestPublishStream) sentAt(i int) *gpb.SubscribeResponse {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i < 0 || i >= len(s.sent) {
		return nil
	}
	return s.sent[i]
}

// rcTestDialOutClient is a fake spb.GNMIDialOutClient.
type rcTestDialOutClient struct {
	publishFn func(context.Context, ...grpc.CallOption) (spb.GNMIDialOut_PublishClient, error)
}

func (c *rcTestDialOutClient) Publish(ctx context.Context, opts ...grpc.CallOption) (spb.GNMIDialOut_PublishClient, error) {
	if c.publishFn != nil {
		return c.publishFn(ctx, opts...)
	}
	return &rcTestPublishStream{}, nil
}

// rcTestDialOutServer is a minimal dial-out collector.
type rcTestDialOutServer struct {
	mu        sync.Mutex
	publishes int
	recv      []*gpb.SubscribeResponse
}

func (s *rcTestDialOutServer) Publish(stream spb.GNMIDialOut_PublishServer) error {
	s.mu.Lock()
	s.publishes++
	s.mu.Unlock()

	for {
		req, err := stream.Recv()
		if err != nil {
			return nil
		}
		s.mu.Lock()
		s.recv = append(s.recv, req)
		s.mu.Unlock()
	}
}

func (s *rcTestDialOutServer) pubCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.publishes
}

func (s *rcTestDialOutServer) recvCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.recv)
}

// rcTestStartDialOutServer starts the fake collector on 127.0.0.1 and returns
// its address. The server is stopped when the test finishes.
func rcTestStartDialOutServer(t *testing.T, srv *rcTestDialOutServer) string {
	t.Helper()

	cert, err := testcert.NewCert()
	if err != nil {
		t.Fatalf("failed to create the test certificate: %v", err)
	}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	gs := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequestClientCert,
		MinVersion:   tls.VersionTLS12,
	})))
	spb.RegisterGNMIDialOutServer(gs, srv)
	go func() {
		_ = gs.Serve(lis)
	}()
	t.Cleanup(gs.Stop)
	return lis.Addr().String()
}

// rcTestDataClient is a fake sdc.Client.
type rcTestDataClient struct {
	values    []*spb.Value
	getErr    error
	streamRun func(q *queue.PriorityQueue, stop chan struct{}, w *sync.WaitGroup)

	mu     sync.Mutex
	calls  int
	closed bool
}

func (d *rcTestDataClient) StreamRun(q *queue.PriorityQueue, stop chan struct{}, w *sync.WaitGroup, _ *gpb.SubscriptionList) {
	if d.streamRun != nil {
		d.streamRun(q, stop, w)
		return
	}
	w.Done()
}

func (d *rcTestDataClient) PollRun(*queue.PriorityQueue, chan struct{}, *sync.WaitGroup, *gpb.SubscriptionList) {
}

func (d *rcTestDataClient) OnceRun(*queue.PriorityQueue, chan struct{}, *sync.WaitGroup, *gpb.SubscriptionList) {
}

func (d *rcTestDataClient) Get(*sync.WaitGroup) ([]*spb.Value, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls++
	if d.getErr != nil {
		return nil, d.getErr
	}
	return d.values, nil
}

func (d *rcTestDataClient) Set([]*gpb.Path, []*gpb.Update, []*gpb.Update) error { return nil }

func (d *rcTestDataClient) Capabilities() []gpb.ModelData { return nil }

func (d *rcTestDataClient) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	return nil
}

func (d *rcTestDataClient) FailedSend() {}

func (d *rcTestDataClient) SentOne(*sdc.Value) {}

// getCalls returns how often the data client was polled.
func (d *rcTestDataClient) getCalls() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}
