package gnmi

// Tests for gNMI ONCE subscription mode against raw DB targets (COUNTERS_DB,
// STATE_DB, etc.) - paths served by DbClient rather than TranslClient.
//
// Per gNMI spec §3.5.1.5.2, ONCE samples each subscribed path once, emits
// sync_response, and the server closes the RPC. For the openconfig gnmi/client
// consumer the sync_response triggers client.ErrStopReading on the receive
// side, which closes the stream and ends Subscribe() -- so these tests assert:
//
//   1. Connected + Update(s) + Sync are observed in that order.
//   2. Subscribe() returns (stream is closed, RPC does not hang).
//
// Failure mode: if DbClient.OnceRun never enqueues sync_response, c.Subscribe
// hangs and the test deadline kicks in producing a clear failure instead of
// an indefinite wait.

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"testing"
	"time"

	"crypto/tls"

	"github.com/openconfig/gnmi/client"
	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
)

// onceTimeout is the upper bound on how long ONCE Subscribe() may block.
// Normal completion is sub-second; the timeout exists only to surface a hang
// (e.g., missing sync_response) quickly rather than stalling CI.
const onceTimeout = 5 * time.Second

// runOnce executes a ONCE Subscribe against the given server and returns the
// ordered notifications. It fails the test if Subscribe does not return within
// onceTimeout. The server address is read from s.Address() so tests bound to
// an OS-assigned port (0) can still connect - avoiding collisions on fixed
// ports like 8081 that are shared by other tests and can flake under CI load.
func runOnce(t *testing.T, s *Server, q client.Query) []client.Notification {
	t.Helper()

	var (
		mu    sync.Mutex
		notis []client.Notification
	)
	q.Addrs = []string{s.Address()}
	q.Type = client.Once
	q.TLS = &tls.Config{InsecureSkipVerify: true}
	q.NotificationHandler = func(n client.Notification) error {
		mu.Lock()
		// Normalise Update timestamps so test assertions are deterministic,
		// matching the convention used by poll_mode_test.go.
		if up, ok := n.(client.Update); ok {
			up.TS = time.Unix(0, 200)
			notis = append(notis, up)
		} else {
			notis = append(notis, n)
		}
		mu.Unlock()
		return nil
	}

	c := client.New()
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), onceTimeout)
	defer cancel()

	if err := c.Subscribe(ctx, q); err != nil {
		t.Fatalf("ONCE subscribe failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	out := make([]client.Notification, len(notis))
	copy(out, notis)
	return out
}

// TestOnceSubscribeCountersDB verifies ONCE mode against COUNTERS_DB for a
// table that is pre-populated by prepareDb (COUNTERS_PORT_NAME_MAP lives in
// COUNTERS_DB as a flat hash).
func TestOnceSubscribeCountersDB(t *testing.T) {
	s := createServer(t, 0)
	go runServer(t, s)
	defer s.ForceStop()

	ns, _ := sdcfg.GetDbDefaultNamespace()
	prepareDb(t, ns)

	got := runOnce(t, s, client.Query{
		Target:  "COUNTERS_DB",
		Queries: []client.Path{{"COUNTERS_PORT_NAME_MAP"}},
	})

	// Expected sequence: Connected -> Update (the port name map) -> Sync.
	if len(got) < 3 {
		t.Fatalf("want >=3 notifications (Connected, Update, Sync); got %d: %+v", len(got), got)
	}
	if _, ok := got[0].(client.Connected); !ok {
		t.Errorf("notification[0] = %T, want client.Connected", got[0])
	}

	var (
		sawUpdate, sawSync bool
		updatePath         []string
	)
	for _, n := range got[1:] {
		switch v := n.(type) {
		case client.Update:
			sawUpdate = true
			updatePath = v.Path
		case client.Sync:
			sawSync = true
		}
	}
	if !sawUpdate {
		t.Errorf("expected an Update for COUNTERS_PORT_NAME_MAP, got: %+v", got)
	} else if len(updatePath) < 2 || updatePath[0] != "COUNTERS_DB" || updatePath[1] != "COUNTERS_PORT_NAME_MAP" {
		t.Errorf("update path = %v, want prefix [COUNTERS_DB COUNTERS_PORT_NAME_MAP]", updatePath)
	}
	if !sawSync {
		t.Errorf("expected Sync, got: %+v", got)
	}
}

// TestOnceSubscribeStateDB verifies ONCE mode against STATE_DB using a key
// written into NEIGH_STATE_TABLE (same table used by the POLL mode tests).
func TestOnceSubscribeStateDB(t *testing.T) {
	s := createServer(t, 0)
	go runServer(t, s)
	defer s.ForceStop()

	ns, _ := sdcfg.GetDbDefaultNamespace()
	// STATE_DB is redis logical DB 6 in the default sonic layout.
	rclient := getRedisClientN(t, 6, ns)
	defer rclient.Close()
	rclient.FlushDB(context.Background())

	rclient.HSet(context.Background(), "NEIGH_STATE_TABLE|10.0.0.57", "peerType", "e-BGP")
	rclient.HSet(context.Background(), "NEIGH_STATE_TABLE|10.0.0.57", "state", "Established")

	var wantVal interface{}
	if err := json.Unmarshal([]byte(`{"peerType":"e-BGP","state":"Established"}`), &wantVal); err != nil {
		t.Fatalf("failed to build expected value: %v", err)
	}

	got := runOnce(t, s, client.Query{
		Target:  "STATE_DB",
		Queries: []client.Path{{"NEIGH_STATE_TABLE", "10.0.0.57"}},
	})

	wantPath := client.Path{"STATE_DB", "NEIGH_STATE_TABLE", "10.0.0.57"}
	var (
		sawSync bool
		updates []client.Update
	)
	for _, n := range got {
		switch v := n.(type) {
		case client.Update:
			updates = append(updates, v)
		case client.Sync:
			sawSync = true
		}
	}
	if len(updates) != 1 {
		t.Fatalf("want exactly 1 Update for STATE_DB key, got %d: %+v", len(updates), got)
	}
	if !reflect.DeepEqual(updates[0].Path, wantPath) {
		t.Errorf("update path = %v, want %v", updates[0].Path, wantPath)
	}
	if !reflect.DeepEqual(updates[0].Val, wantVal) {
		t.Errorf("update value = %v, want %v", updates[0].Val, wantVal)
	}
	if !sawSync {
		t.Errorf("expected Sync after Update, got: %+v", got)
	}
}

// TestOnceSubscribeStateDBMissingKey verifies ONCE mode with a missing key:
// no Update is sent, Sync is still produced, and the RPC closes cleanly. This
// matches PollRun's first-poll semantics for absent data (see
// TestPollStateDBMissingKey in poll_mode_test.go).
func TestOnceSubscribeStateDBMissingKey(t *testing.T) {
	s := createServer(t, 0)
	go runServer(t, s)
	defer s.ForceStop()

	ns, _ := sdcfg.GetDbDefaultNamespace()
	rclient := getRedisClientN(t, 6, ns)
	defer rclient.Close()
	rclient.FlushDB(context.Background())

	got := runOnce(t, s, client.Query{
		Target:  "STATE_DB",
		Queries: []client.Path{{"NEIGH_STATE_TABLE", "10.0.0.57"}},
	})

	var sawSync bool
	for _, n := range got {
		if _, ok := n.(client.Update); ok {
			t.Errorf("unexpected Update for missing STATE_DB key: %+v", n)
		}
		if _, ok := n.(client.Sync); ok {
			sawSync = true
		}
	}
	if !sawSync {
		t.Errorf("expected Sync even when key is absent, got: %+v", got)
	}
}
