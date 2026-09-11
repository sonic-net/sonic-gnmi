package client

import (
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
)

// TestUpdateStatsClosesRedisClient verifies that update_stats() releases the
// COUNTERS_DB redis client it creates when the event subscription is torn down.
//
// This is a regression test for a resource leak: the redis client (which owns a
// TCP socket and background goroutines) was created per events subscription but
// never Close()d, so every subscribe/unsubscribe cycle leaked one client. The
// fix adds `defer rclient.Close()` right after the client is created.
func TestUpdateStatsClosesRedisClient(t *testing.T) {
	mr := miniredis.RunT(t)

	// Point the COUNTERS_DB lookups used by update_stats at the in-memory redis.
	patches := gomonkey.ApplyFunc(sdcfg.GetDbDefaultNamespace, func() (string, error) {
		return "", nil
	})
	defer patches.Reset()
	patches.ApplyFunc(sdcfg.GetDbTcpAddr, func(_ string, _ string) (string, error) {
		return mr.Addr(), nil
	})
	patches.ApplyFunc(sdcfg.GetDbId, func(_ string, _ string) (int, error) {
		return 0, nil
	})

	// Record whether the redis client gets closed. Close is deferred inside
	// update_stats, so it runs exactly when the function returns.
	var closeCount int32
	patches.ApplyMethod(reflect.TypeOf(&redis.Client{}), "Close", func(_ *redis.Client) error {
		atomic.AddInt32(&closeCount, 1)
		return nil
	})

	// A non-zero counter makes the initial "wait for activity" loop in
	// update_stats break immediately so it proceeds to create the redis client.
	evtc := &EventClient{
		counters: map[string]uint64{"COUNTERS:TEST": 1},
		wg:       &sync.WaitGroup{},
	}

	evtc.wg.Add(1)
	go update_stats(evtc)

	// Give update_stats time to create the redis client and enter its main loop
	// before signalling the subscription to stop.
	time.Sleep(300 * time.Millisecond)
	evtc.stopMutex.Lock()
	evtc.stopped = 1
	evtc.stopMutex.Unlock()

	// Wait for update_stats to return (and thus run the deferred Close).
	done := make(chan struct{})
	go func() {
		evtc.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("update_stats did not return after stop was signalled")
	}

	if atomic.LoadInt32(&closeCount) == 0 {
		t.Fatal("update_stats returned without closing its redis client (resource leak)")
	}
}
