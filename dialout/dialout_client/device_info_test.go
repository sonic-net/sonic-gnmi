package telemetry_dialout

// device_info_test.go covers the device info cache and helpers added by
// PR #819: deviceInfoCache (Get/refresh/ensureInitialized/startMonitoring),
// getHostnameAndMgmtIP, monitorHostnameAndMgmtIPChanges, addDeviceInfoToResp
// and handleNetworkChange.
//
// The STATE_DB/CONFIG_DB lookups run against an in-process miniredis instance.
// The sonic_db_config accessors are patched with gomonkey so that the tests
// exercise the real production code paths regardless of which swsscommon
// bindings are available (SWIG bindings in CI, a stub for local runs).
// gomonkey requires -gcflags=all=-l, which the repo Makefile sets through
// TEST_FLAGS; without it the patches below are ineffective.
//
// Every piece of package level state touched here is restored through
// t.Cleanup, so the tests in dialout_client_test.go are unaffected.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/alicebob/miniredis/v2"
	gpb "github.com/openconfig/gnmi/proto/gnmi"
	sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
)

const (
	// Database ids used by the device info code. STATE_DB and CONFIG_DB get
	// different ids so that their keyspaces stay separate in miniredis.
	diTestStateDBId  = 6
	diTestConfigDBId = 4

	// Key names used by getHostnameAndMgmtIP/monitorHostnameAndMgmtIPChanges.
	diTestNetworkInfoKey    = "NETWORK_INFO_TABLE|eth0"
	diTestDeviceMetadataKey = "DEVICE_METADATA|localhost"
)

var diTestInitDelay sync.Once

// diTestStartupDelay gives the monitor goroutine started by the package init()
// (go globalDeviceInfo.startMonitoring()) time to finish its sonic_db_config
// lookups before the tests start rewriting those functions with gomonkey.
// Rewriting a function while another goroutine executes it is not safe.
func diTestStartupDelay() {
	diTestInitDelay.Do(func() {
		time.Sleep(200 * time.Millisecond)
	})
}

// diTestStartMiniRedis starts an in-process redis listening on TCP.
func diTestStartMiniRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	diTestStartupDelay()
	mr := miniredis.NewMiniRedis()
	if err := mr.StartAddr("127.0.0.1:0"); err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	return mr
}

// diTestDeadRedisAddr returns a TCP address nothing is listening on.
func diTestDeadRedisAddr(t *testing.T) string {
	t.Helper()
	mr := miniredis.NewMiniRedis()
	if err := mr.StartAddr("127.0.0.1:0"); err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	addr := mr.Addr()
	mr.Close()
	return addr
}

// diTestStartUnixProxy listens on a unix domain socket and forwards all
// connections to addr, so the "UseRedisLocalTcpPort == false" (unix socket)
// code paths can be exercised against miniredis.
func diTestStartUnixProxy(t *testing.T, addr string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "di-test-unix")
	if err != nil {
		t.Fatalf("failed to create temp dir for unix socket: %v", err)
	}
	sock := filepath.Join(dir, "redis.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		os.RemoveAll(dir)
		t.Fatalf("failed to listen on unix socket %s: %v", sock, err)
	}

	var (
		mu     sync.Mutex
		conns  []net.Conn
		closed bool
	)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			if closed {
				mu.Unlock()
				conn.Close()
				return
			}
			conns = append(conns, conn)
			mu.Unlock()
			go func(c net.Conn) {
				defer c.Close()
				up, err := net.Dial("tcp", addr)
				if err != nil {
					return
				}
				defer up.Close()
				go func() {
					_, _ = io.Copy(c, up)
				}()
				_, _ = io.Copy(up, c)
			}(conn)
		}
	}()

	t.Cleanup(func() {
		ln.Close()
		mu.Lock()
		closed = true
		for _, c := range conns {
			c.Close()
		}
		conns = nil
		mu.Unlock()
		os.RemoveAll(dir)
	})
	return sock
}

// diTestSetRedisTcp sets sdc.UseRedisLocalTcpPort for the duration of a test.
func diTestSetRedisTcp(t *testing.T, useTcp bool) {
	t.Helper()
	old := sdc.UseRedisLocalTcpPort
	sdc.UseRedisLocalTcpPort = useTcp
	t.Cleanup(func() {
		sdc.UseRedisLocalTcpPort = old
	})
}

// diTestPatchDbId replaces sdcfg.GetDbId for the duration of a test.
func diTestPatchDbId(t *testing.T, fn func(db string, ns string) (int, error)) {
	t.Helper()
	patches := gomonkey.ApplyFunc(sdcfg.GetDbId, fn)
	t.Cleanup(patches.Reset)
}

// diTestPatchDbTcpAddr replaces sdcfg.GetDbTcpAddr for the duration of a test.
func diTestPatchDbTcpAddr(t *testing.T, fn func(db string, ns string) (string, error)) {
	t.Helper()
	patches := gomonkey.ApplyFunc(sdcfg.GetDbTcpAddr, fn)
	t.Cleanup(patches.Reset)
}

// diTestPatchDbSock replaces sdcfg.GetDbSock for the duration of a test.
func diTestPatchDbSock(t *testing.T, fn func(db string, ns string) (string, error)) {
	t.Helper()
	patches := gomonkey.ApplyFunc(sdcfg.GetDbSock, fn)
	t.Cleanup(patches.Reset)
}

// diTestStateConfigDbIds resolves STATE_DB/CONFIG_DB to the ids the device
// info code expects.
func diTestStateConfigDbIds(db string, ns string) (int, error) {
	switch db {
	case "STATE_DB":
		return diTestStateDBId, nil
	case "CONFIG_DB":
		return diTestConfigDBId, nil
	}
	return -1, fmt.Errorf("di-test: unexpected database %q", db)
}

// diTestPatchDefaultDbConfig makes the sonic_db_config lookups resolve
// STATE_DB and CONFIG_DB to addr.
func diTestPatchDefaultDbConfig(t *testing.T, addr string) {
	t.Helper()
	diTestPatchDbId(t, diTestStateConfigDbIds)
	diTestPatchDbTcpAddr(t, func(db string, ns string) (string, error) {
		if db != "STATE_DB" && db != "CONFIG_DB" {
			return "", fmt.Errorf("di-test: unexpected database %q", db)
		}
		return addr, nil
	})

	// Fail early with a clear message when the gomonkey patches did not take
	// effect (e.g. the tests were run without -gcflags=all=-l).
	if got, err := sdcfg.GetDbId("STATE_DB", ""); err != nil || got != diTestStateDBId {
		t.Fatalf("gomonkey patch of sdcfg.GetDbId is not effective (got %d, %v); run the tests with -gcflags=all=-l", got, err)
	}
	if got, err := sdcfg.GetDbTcpAddr("STATE_DB", ""); err != nil || got != addr {
		t.Fatalf("gomonkey patch of sdcfg.GetDbTcpAddr is not effective (got %q, %v); run the tests with -gcflags=all=-l", got, err)
	}
}

// diTestSeed stores a hash in the given miniredis database.
func diTestSeed(t *testing.T, mr *miniredis.Miniredis, dbId int, key string, fv ...string) {
	t.Helper()
	mr.Select(dbId)
	mr.HSet(key, fv...)
	mr.Select(0)
}

// diTestSeedDeviceInfo seeds the STATE_DB network info and CONFIG_DB device
// metadata rows used by getHostnameAndMgmtIP. Empty values are not written.
func diTestSeedDeviceInfo(t *testing.T, mr *miniredis.Miniredis, hostname, mgmtIP string) {
	t.Helper()
	if mgmtIP != "" {
		diTestSeed(t, mr, diTestStateDBId, diTestNetworkInfoKey, "ip", mgmtIP)
	}
	if hostname != "" {
		diTestSeed(t, mr, diTestConfigDBId, diTestDeviceMetadataKey, "hostname", hostname)
	}
}

// diTestStateKeyspaceChannel is the keyspace notification channel the monitor
// subscribes to for STATE_DB network info.
func diTestStateKeyspaceChannel() string {
	return fmt.Sprintf("__keyspace@%d__:%s", diTestStateDBId, diTestNetworkInfoKey)
}

// diTestConfigKeyspaceChannel is the keyspace notification channel the monitor
// subscribes to for CONFIG_DB device metadata.
func diTestConfigKeyspaceChannel() string {
	return fmt.Sprintf("__keyspace@%d__:%s", diTestConfigDBId, diTestDeviceMetadataKey)
}

// diTestNewCache returns a deviceInfoCache whose one time initialization has
// already happened, so Get() does not reach out to the database unless the
// test explicitly asks for a refresh.
func diTestNewCache(hostname string, mgmtIP string) *deviceInfoCache {
	c := &deviceInfoCache{
		hostname: hostname,
		mgmtIP:   mgmtIP,
		updateCh: make(chan struct{}, 16),
		stopCh:   make(chan struct{}),
	}
	c.initOnce.Do(func() {})
	return c
}

// diTestSwapGlobalDeviceInfo replaces the process wide device info cache for
// the duration of a test.
func diTestSwapGlobalDeviceInfo(t *testing.T, c *deviceInfoCache) {
	t.Helper()
	old := globalDeviceInfo
	globalDeviceInfo = c
	t.Cleanup(func() {
		globalDeviceInfo = old
	})
}

// diTestSwapNetworkChangeTrigger replaces the process wide network change
// trigger channel for the duration of a test and returns it.
func diTestSwapNetworkChangeTrigger(t *testing.T) chan struct{} {
	t.Helper()
	old := networkChangeTrigger
	ch := make(chan struct{}, 16)
	networkChangeTrigger = ch
	t.Cleanup(func() {
		networkChangeTrigger = old
	})
	return ch
}

// diTestWaitChan requires a value to arrive on ch within timeout.
func diTestWaitChan(t *testing.T, ch <-chan struct{}, timeout time.Duration, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// diTestDrainChan discards everything currently queued on ch.
func diTestDrainChan(ch <-chan struct{}) {
	for {
		select {
		case <-ch:
			continue
		default:
		}
		break
	}
}

// diTestWaitQuiescent drains ch until it has been silent for quiet, failing if
// that does not happen within maxWait.
func diTestWaitQuiescent(t *testing.T, ch <-chan struct{}, quiet time.Duration, maxWait time.Duration) {
	t.Helper()
	deadline := time.Now().Add(maxWait)
	for {
		diTestDrainChan(ch)
		select {
		case <-ch:
			if time.Now().After(deadline) {
				t.Fatalf("channel kept producing notifications for %s", maxWait)
			}
		case <-time.After(quiet):
			return
		}
	}
}

// diTestPublishUntilReceived publishes payload on channel until a notification
// shows up on updateCh.
func diTestPublishUntilReceived(t *testing.T, mr *miniredis.Miniredis, updateCh <-chan struct{}, channel string, payload string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		mr.Publish(channel, payload)
		select {
		case <-updateCh:
			return
		case <-time.After(50 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatalf("no keyspace notification was forwarded to updateCh within %s", timeout)
		}
	}
}

// diTestRunMonitor runs monitorHostnameAndMgmtIPChanges and requires it to
// return on its own; all callers of this helper make it fail early.
func diTestRunMonitor(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		monitorHostnameAndMgmtIPChanges(ctx, make(chan struct{}, 16))
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("monitorHostnameAndMgmtIPChanges did not return")
	}
}

func TestDiCacheGetDefaults(t *testing.T) {
	c := diTestNewCache("", "")

	hostname, mgmtIP := c.Get()
	if hostname != "unknown" {
		t.Errorf("Get() hostname on empty cache = %q, want %q", hostname, "unknown")
	}
	if mgmtIP != "unknown" {
		t.Errorf("Get() mgmt IP on empty cache = %q, want %q", mgmtIP, "unknown")
	}
}

func TestDiCacheGetCached(t *testing.T) {
	mr := diTestStartMiniRedis(t)
	diTestSetRedisTcp(t, true)
	diTestPatchDefaultDbConfig(t, mr.Addr())
	diTestSeedDeviceInfo(t, mr, "switch-1", "10.0.0.5")
	diTestSwapNetworkChangeTrigger(t)

	c := diTestNewCache("switch-1", "10.0.0.5")

	// Concurrent readers must not race with each other or with a refresh
	// triggered through updateCh.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				hostname, mgmtIP := c.Get()
				if hostname != "switch-1" || mgmtIP != "10.0.0.5" {
					t.Errorf("Get() = (%q, %q), want (%q, %q)", hostname, mgmtIP, "switch-1", "10.0.0.5")
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestDiCacheGetRefreshesOnUpdate(t *testing.T) {
	mr := diTestStartMiniRedis(t)
	diTestSetRedisTcp(t, true)
	diTestPatchDefaultDbConfig(t, mr.Addr())
	diTestSeedDeviceInfo(t, mr, "switch-2", "10.0.0.6")
	diTestSwapNetworkChangeTrigger(t)

	c := diTestNewCache("", "")
	// Simulate a keyspace notification from the monitor goroutine.
	c.updateCh <- struct{}{}

	hostname, mgmtIP := c.Get()
	if hostname != "switch-2" || mgmtIP != "10.0.0.6" {
		t.Fatalf("Get() after an update notification = (%q, %q), want (%q, %q)", hostname, mgmtIP, "switch-2", "10.0.0.6")
	}
}

func TestDiCacheEnsureInitializedOnce(t *testing.T) {
	mr := diTestStartMiniRedis(t)
	diTestSetRedisTcp(t, true)
	diTestPatchDefaultDbConfig(t, mr.Addr())
	diTestSeedDeviceInfo(t, mr, "switch-1", "10.0.0.5")
	diTestSwapNetworkChangeTrigger(t)

	c := &deviceInfoCache{
		updateCh: make(chan struct{}, 16),
		stopCh:   make(chan struct{}),
	}
	c.ensureInitialized()
	if c.hostname != "switch-1" || c.mgmtIP != "10.0.0.5" {
		t.Fatalf("cache after ensureInitialized = (%q, %q), want (%q, %q)", c.hostname, c.mgmtIP, "switch-1", "10.0.0.5")
	}

	// The database changes, but ensureInitialized must not read it again.
	diTestSeedDeviceInfo(t, mr, "switch-9", "10.0.0.9")
	c.ensureInitialized()
	if c.hostname != "switch-1" || c.mgmtIP != "10.0.0.5" {
		t.Fatalf("cache after the second ensureInitialized = (%q, %q), want (%q, %q)", c.hostname, c.mgmtIP, "switch-1", "10.0.0.5")
	}

	// Without an update notification Get() returns the cached values.
	hostname, mgmtIP := c.Get()
	if hostname != "switch-1" || mgmtIP != "10.0.0.5" {
		t.Fatalf("Get() = (%q, %q), want (%q, %q)", hostname, mgmtIP, "switch-1", "10.0.0.5")
	}
}

func TestDiCacheRefreshUnchanged(t *testing.T) {
	mr := diTestStartMiniRedis(t)
	diTestSetRedisTcp(t, true)
	diTestPatchDefaultDbConfig(t, mr.Addr())
	diTestSeedDeviceInfo(t, mr, "switch-1", "10.0.0.5")
	trigger := diTestSwapNetworkChangeTrigger(t)

	c := diTestNewCache("switch-1", "10.0.0.5")
	c.refresh()

	if c.hostname != "switch-1" || c.mgmtIP != "10.0.0.5" {
		t.Fatalf("cache after refresh = (%q, %q), want (%q, %q)", c.hostname, c.mgmtIP, "switch-1", "10.0.0.5")
	}
	diTestDrainChan(trigger)
	if len(trigger) != 0 {
		t.Fatalf("refresh of unchanged device info queued %d network change trigger(s), want 0", len(trigger))
	}
}

func TestDiCacheRefreshMgmtIPChanged(t *testing.T) {
	mr := diTestStartMiniRedis(t)
	diTestSetRedisTcp(t, true)
	diTestPatchDefaultDbConfig(t, mr.Addr())
	diTestSeedDeviceInfo(t, mr, "switch-1", "10.0.0.6")
	trigger := diTestSwapNetworkChangeTrigger(t)

	c := diTestNewCache("switch-1", "10.0.0.5")
	c.refresh()

	if c.hostname != "switch-1" {
		t.Errorf("hostname after refresh = %q, want %q", c.hostname, "switch-1")
	}
	if c.mgmtIP != "10.0.0.6" {
		t.Errorf("mgmt IP after refresh = %q, want %q", c.mgmtIP, "10.0.0.6")
	}
	// A new management IP must trigger a reconnect of the dialout clients.
	diTestWaitChan(t, trigger, time.Second, "network change trigger after the management IP changed")
}

func TestDiCacheRefreshMissingDbRows(t *testing.T) {
	mr := diTestStartMiniRedis(t)
	diTestSetRedisTcp(t, true)
	diTestPatchDefaultDbConfig(t, mr.Addr())
	trigger := diTestSwapNetworkChangeTrigger(t)

	c := diTestNewCache("switch-1", "10.0.0.5")
	c.refresh()

	// Missing rows are reported as "unknown" and cached, replacing the
	// previously known values, and the management IP change triggers a
	// reconnect.
	if c.hostname != "unknown" || c.mgmtIP != "unknown" {
		t.Fatalf("cache after refresh of an empty database = (%q, %q), want (%q, %q)", c.hostname, c.mgmtIP, "unknown", "unknown")
	}
	diTestWaitChan(t, trigger, time.Second, "network change trigger after the management IP changed to unknown")
}

func TestDiCacheRefreshErrorKeepsValues(t *testing.T) {
	mr := diTestStartMiniRedis(t)
	deadAddr := diTestDeadRedisAddr(t)
	diTestSetRedisTcp(t, true)
	// The STATE_DB lookup fails, so refresh must keep the cached values.
	diTestPatchDbId(t, diTestStateConfigDbIds)
	diTestPatchDbTcpAddr(t, func(db string, ns string) (string, error) {
		if db == "STATE_DB" {
			return deadAddr, nil
		}
		return mr.Addr(), nil
	})
	trigger := diTestSwapNetworkChangeTrigger(t)

	c := diTestNewCache("switch-1", "10.0.0.5")
	c.refresh()

	if c.hostname != "switch-1" || c.mgmtIP != "10.0.0.5" {
		t.Fatalf("cache after a failed refresh = (%q, %q), want (%q, %q)", c.hostname, c.mgmtIP, "switch-1", "10.0.0.5")
	}
	if len(trigger) != 0 {
		t.Fatalf("failed refresh queued %d network change trigger(s), want 0", len(trigger))
	}
}

func TestDiGetHostnameAndMgmtIP(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mr := diTestStartMiniRedis(t)
		diTestSetRedisTcp(t, true)
		diTestPatchDefaultDbConfig(t, mr.Addr())
		diTestSeedDeviceInfo(t, mr, "switch-1", "10.0.0.5")

		hostname, mgmtIP, err := getHostnameAndMgmtIP()
		if err != nil {
			t.Fatalf("getHostnameAndMgmtIP() returned an error: %v", err)
		}
		if hostname != "switch-1" || mgmtIP != "10.0.0.5" {
			t.Fatalf("getHostnameAndMgmtIP() = (%q, %q), want (%q, %q)", hostname, mgmtIP, "switch-1", "10.0.0.5")
		}
	})

	t.Run("missing rows", func(t *testing.T) {
		mr := diTestStartMiniRedis(t)
		diTestSetRedisTcp(t, true)
		diTestPatchDefaultDbConfig(t, mr.Addr())

		hostname, mgmtIP, err := getHostnameAndMgmtIP()
		if err != nil {
			t.Fatalf("getHostnameAndMgmtIP() returned an error: %v", err)
		}
		if hostname != "unknown" || mgmtIP != "unknown" {
			t.Fatalf("getHostnameAndMgmtIP() = (%q, %q), want (%q, %q)", hostname, mgmtIP, "unknown", "unknown")
		}
	})

	t.Run("missing ip field", func(t *testing.T) {
		mr := diTestStartMiniRedis(t)
		diTestSetRedisTcp(t, true)
		diTestPatchDefaultDbConfig(t, mr.Addr())
		diTestSeed(t, mr, diTestStateDBId, diTestNetworkInfoKey, "oper_status", "up")
		diTestSeedDeviceInfo(t, mr, "switch-1", "")

		hostname, mgmtIP, err := getHostnameAndMgmtIP()
		if err != nil {
			t.Fatalf("getHostnameAndMgmtIP() returned an error: %v", err)
		}
		if hostname != "switch-1" || mgmtIP != "unknown" {
			t.Fatalf("getHostnameAndMgmtIP() = (%q, %q), want (%q, %q)", hostname, mgmtIP, "switch-1", "unknown")
		}
	})

	t.Run("missing hostname field", func(t *testing.T) {
		mr := diTestStartMiniRedis(t)
		diTestSetRedisTcp(t, true)
		diTestPatchDefaultDbConfig(t, mr.Addr())
		diTestSeedDeviceInfo(t, mr, "", "10.0.0.5")
		diTestSeed(t, mr, diTestConfigDBId, diTestDeviceMetadataKey, "platform", "x86_64")

		hostname, mgmtIP, err := getHostnameAndMgmtIP()
		if err != nil {
			t.Fatalf("getHostnameAndMgmtIP() returned an error: %v", err)
		}
		if hostname != "unknown" || mgmtIP != "10.0.0.5" {
			t.Fatalf("getHostnameAndMgmtIP() = (%q, %q), want (%q, %q)", hostname, mgmtIP, "unknown", "10.0.0.5")
		}
	})

	t.Run("state db id error", func(t *testing.T) {
		diTestSetRedisTcp(t, true)
		diTestPatchDbId(t, func(db string, ns string) (int, error) {
			return -1, errors.New("di-test: no database config")
		})

		hostname, mgmtIP, err := getHostnameAndMgmtIP()
		if err == nil {
			t.Fatal("getHostnameAndMgmtIP() succeeded, want an error")
		}
		if hostname != "" || mgmtIP != "" {
			t.Fatalf("getHostnameAndMgmtIP() = (%q, %q), want empty values", hostname, mgmtIP)
		}
	})

	t.Run("config db id error", func(t *testing.T) {
		mr := diTestStartMiniRedis(t)
		diTestSetRedisTcp(t, true)
		diTestSeedDeviceInfo(t, mr, "", "10.0.0.5")
		diTestPatchDbId(t, func(db string, ns string) (int, error) {
			if db == "STATE_DB" {
				return diTestStateDBId, nil
			}
			return -1, errors.New("di-test: no config db")
		})
		diTestPatchDbTcpAddr(t, func(db string, ns string) (string, error) {
			return mr.Addr(), nil
		})

		hostname, mgmtIP, err := getHostnameAndMgmtIP()
		if err == nil {
			t.Fatal("getHostnameAndMgmtIP() succeeded, want an error")
		}
		if hostname != "" || mgmtIP != "" {
			t.Fatalf("getHostnameAndMgmtIP() = (%q, %q), want empty values", hostname, mgmtIP)
		}
	})

	t.Run("state db tcp addr error", func(t *testing.T) {
		diTestSetRedisTcp(t, true)
		diTestPatchDbId(t, diTestStateConfigDbIds)
		diTestPatchDbTcpAddr(t, func(db string, ns string) (string, error) {
			return "", errors.New("di-test: no tcp addr")
		})

		hostname, mgmtIP, err := getHostnameAndMgmtIP()
		if err == nil {
			t.Fatal("getHostnameAndMgmtIP() succeeded, want an error")
		}
		if hostname != "" || mgmtIP != "" {
			t.Fatalf("getHostnameAndMgmtIP() = (%q, %q), want empty values", hostname, mgmtIP)
		}
	})

	t.Run("config db tcp addr error", func(t *testing.T) {
		mr := diTestStartMiniRedis(t)
		diTestSetRedisTcp(t, true)
		diTestSeedDeviceInfo(t, mr, "", "10.0.0.5")
		diTestPatchDbId(t, diTestStateConfigDbIds)
		diTestPatchDbTcpAddr(t, func(db string, ns string) (string, error) {
			if db == "STATE_DB" {
				return mr.Addr(), nil
			}
			return "", errors.New("di-test: no config tcp addr")
		})

		hostname, mgmtIP, err := getHostnameAndMgmtIP()
		if err == nil {
			t.Fatal("getHostnameAndMgmtIP() succeeded, want an error")
		}
		if hostname != "" || mgmtIP != "" {
			t.Fatalf("getHostnameAndMgmtIP() = (%q, %q), want empty values", hostname, mgmtIP)
		}
	})

	t.Run("state db unreachable", func(t *testing.T) {
		deadAddr := diTestDeadRedisAddr(t)
		diTestSetRedisTcp(t, true)
		diTestPatchDbId(t, diTestStateConfigDbIds)
		diTestPatchDbTcpAddr(t, func(db string, ns string) (string, error) {
			return deadAddr, nil
		})

		hostname, mgmtIP, err := getHostnameAndMgmtIP()
		if err == nil {
			t.Fatal("getHostnameAndMgmtIP() succeeded, want an error")
		}
		if hostname != "unknown" || mgmtIP != "unknown" {
			t.Fatalf("getHostnameAndMgmtIP() = (%q, %q), want (%q, %q)", hostname, mgmtIP, "unknown", "unknown")
		}
	})

	t.Run("config db unreachable", func(t *testing.T) {
		mr := diTestStartMiniRedis(t)
		deadAddr := diTestDeadRedisAddr(t)
		diTestSetRedisTcp(t, true)
		diTestSeedDeviceInfo(t, mr, "", "10.0.0.5")
		diTestPatchDbId(t, diTestStateConfigDbIds)
		diTestPatchDbTcpAddr(t, func(db string, ns string) (string, error) {
			if db == "STATE_DB" {
				return mr.Addr(), nil
			}
			return deadAddr, nil
		})

		hostname, mgmtIP, err := getHostnameAndMgmtIP()
		if err == nil {
			t.Fatal("getHostnameAndMgmtIP() succeeded, want an error")
		}
		if hostname != "unknown" || mgmtIP != "unknown" {
			t.Fatalf("getHostnameAndMgmtIP() = (%q, %q), want (%q, %q)", hostname, mgmtIP, "unknown", "unknown")
		}
	})

	t.Run("unix socket success", func(t *testing.T) {
		mr := diTestStartMiniRedis(t)
		sock := diTestStartUnixProxy(t, mr.Addr())
		diTestSetRedisTcp(t, false)
		diTestPatchDbId(t, diTestStateConfigDbIds)
		diTestPatchDbSock(t, func(db string, ns string) (string, error) {
			return sock, nil
		})
		diTestSeedDeviceInfo(t, mr, "switch-1", "10.0.0.5")

		hostname, mgmtIP, err := getHostnameAndMgmtIP()
		if err != nil {
			t.Fatalf("getHostnameAndMgmtIP() over a unix socket returned an error: %v", err)
		}
		if hostname != "switch-1" || mgmtIP != "10.0.0.5" {
			t.Fatalf("getHostnameAndMgmtIP() = (%q, %q), want (%q, %q)", hostname, mgmtIP, "switch-1", "10.0.0.5")
		}
	})

	t.Run("state db sock error", func(t *testing.T) {
		diTestSetRedisTcp(t, false)
		diTestPatchDbId(t, diTestStateConfigDbIds)
		diTestPatchDbSock(t, func(db string, ns string) (string, error) {
			return "", errors.New("di-test: no unix socket path")
		})

		hostname, mgmtIP, err := getHostnameAndMgmtIP()
		if err == nil {
			t.Fatal("getHostnameAndMgmtIP() succeeded, want an error")
		}
		if hostname != "" || mgmtIP != "" {
			t.Fatalf("getHostnameAndMgmtIP() = (%q, %q), want empty values", hostname, mgmtIP)
		}
	})

	t.Run("config db sock error", func(t *testing.T) {
		mr := diTestStartMiniRedis(t)
		sock := diTestStartUnixProxy(t, mr.Addr())
		diTestSetRedisTcp(t, false)
		diTestSeedDeviceInfo(t, mr, "", "10.0.0.5")
		diTestPatchDbId(t, diTestStateConfigDbIds)
		diTestPatchDbSock(t, func(db string, ns string) (string, error) {
			if db == "STATE_DB" {
				return sock, nil
			}
			return "", errors.New("di-test: no config unix socket path")
		})

		hostname, mgmtIP, err := getHostnameAndMgmtIP()
		if err == nil {
			t.Fatal("getHostnameAndMgmtIP() succeeded, want an error")
		}
		if hostname != "" || mgmtIP != "" {
			t.Fatalf("getHostnameAndMgmtIP() = (%q, %q), want empty values", hostname, mgmtIP)
		}
	})

	t.Run("config db unix socket unreachable", func(t *testing.T) {
		mr := diTestStartMiniRedis(t)
		sock := diTestStartUnixProxy(t, mr.Addr())
		diTestSetRedisTcp(t, false)
		diTestSeedDeviceInfo(t, mr, "", "10.0.0.5")
		diTestPatchDbId(t, diTestStateConfigDbIds)
		diTestPatchDbSock(t, func(db string, ns string) (string, error) {
			if db == "STATE_DB" {
				return sock, nil
			}
			return filepath.Join(os.TempDir(), "di-test-missing", "redis.sock"), nil
		})

		hostname, mgmtIP, err := getHostnameAndMgmtIP()
		if err == nil {
			t.Fatal("getHostnameAndMgmtIP() succeeded, want an error")
		}
		if hostname != "unknown" || mgmtIP != "unknown" {
			t.Fatalf("getHostnameAndMgmtIP() = (%q, %q), want (%q, %q)", hostname, mgmtIP, "unknown", "unknown")
		}
	})
}

func TestDiMonitorHostnameAndMgmtIPChanges(t *testing.T) {
	mr := diTestStartMiniRedis(t)
	diTestSetRedisTcp(t, true)
	diTestPatchDefaultDbConfig(t, mr.Addr())

	updateCh := make(chan struct{}, 4)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan struct{})
	go func() {
		defer close(done)
		monitorHostnameAndMgmtIPChanges(ctx, updateCh)
	}()

	// Wait until the monitor subscribed and forwards keyspace events.
	diTestPublishUntilReceived(t, mr, updateCh, diTestStateKeyspaceChannel(), "hset", 10*time.Second)
	diTestWaitQuiescent(t, updateCh, 200*time.Millisecond, 10*time.Second)

	// Keyspace events with an unrelated payload must not be forwarded.
	for i := 0; i < 3; i++ {
		mr.Publish(diTestStateKeyspaceChannel(), "expire")
		mr.Publish(diTestConfigKeyspaceChannel(), "expired")
	}
	select {
	case <-updateCh:
		t.Fatal("monitor forwarded a notification for an ignored keyspace event")
	case <-time.After(400 * time.Millisecond):
	}

	// A full update channel must not block the monitor.
	updateCh <- struct{}{}
	for i := 0; i < 20; i++ {
		mr.Publish(diTestStateKeyspaceChannel(), "hset")
		mr.Publish(diTestConfigKeyspaceChannel(), "del")
		time.Sleep(10 * time.Millisecond)
	}
	diTestWaitQuiescent(t, updateCh, 200*time.Millisecond, 10*time.Second)

	// Device metadata changes are forwarded as well.
	diTestPublishUntilReceived(t, mr, updateCh, diTestConfigKeyspaceChannel(), "hdel", 10*time.Second)

	// Cancelling the context must stop the monitor.
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("monitorHostnameAndMgmtIPChanges did not exit when the context was cancelled")
	}
}

func TestDiMonitorHostnameAndMgmtIPChangesUnixSocket(t *testing.T) {
	mr := diTestStartMiniRedis(t)
	sock := diTestStartUnixProxy(t, mr.Addr())
	diTestSetRedisTcp(t, false)
	diTestPatchDbId(t, diTestStateConfigDbIds)
	diTestPatchDbSock(t, func(db string, ns string) (string, error) {
		return sock, nil
	})

	updateCh := make(chan struct{}, 4)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan struct{})
	go func() {
		defer close(done)
		monitorHostnameAndMgmtIPChanges(ctx, updateCh)
	}()

	diTestPublishUntilReceived(t, mr, updateCh, diTestConfigKeyspaceChannel(), "hset", 10*time.Second)

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("monitorHostnameAndMgmtIPChanges did not exit when the context was cancelled")
	}
}

func TestDiMonitorHostnameAndMgmtIPChangesEarlyReturns(t *testing.T) {
	mr := diTestStartMiniRedis(t)
	deadAddr := diTestDeadRedisAddr(t)
	sock := diTestStartUnixProxy(t, mr.Addr())
	diTestSetRedisTcp(t, true)

	t.Run("state db id error", func(t *testing.T) {
		diTestPatchDbId(t, func(db string, ns string) (int, error) {
			return -1, errors.New("di-test: no database config")
		})
		diTestRunMonitor(t)
	})

	t.Run("state db tcp addr error", func(t *testing.T) {
		diTestPatchDbId(t, diTestStateConfigDbIds)
		diTestPatchDbTcpAddr(t, func(db string, ns string) (string, error) {
			return "", errors.New("di-test: no tcp addr")
		})
		diTestRunMonitor(t)
	})

	t.Run("state db subscription fails", func(t *testing.T) {
		diTestPatchDbId(t, diTestStateConfigDbIds)
		diTestPatchDbTcpAddr(t, func(db string, ns string) (string, error) {
			return deadAddr, nil
		})
		diTestRunMonitor(t)
	})

	t.Run("state db sock error", func(t *testing.T) {
		diTestSetRedisTcp(t, false)
		diTestPatchDbId(t, diTestStateConfigDbIds)
		diTestPatchDbSock(t, func(db string, ns string) (string, error) {
			return "", errors.New("di-test: no unix socket path")
		})
		diTestRunMonitor(t)
	})

	t.Run("state db unix socket subscription fails", func(t *testing.T) {
		diTestSetRedisTcp(t, false)
		diTestPatchDbId(t, diTestStateConfigDbIds)
		diTestPatchDbSock(t, func(db string, ns string) (string, error) {
			return filepath.Join(os.TempDir(), "di-test-missing", "redis.sock"), nil
		})
		diTestRunMonitor(t)
	})

	t.Run("config db id error", func(t *testing.T) {
		diTestPatchDbId(t, func(db string, ns string) (int, error) {
			if db == "STATE_DB" {
				return diTestStateDBId, nil
			}
			return -1, errors.New("di-test: no config db")
		})
		diTestPatchDbTcpAddr(t, func(db string, ns string) (string, error) {
			return mr.Addr(), nil
		})
		diTestRunMonitor(t)
	})

	t.Run("config db tcp addr error", func(t *testing.T) {
		diTestPatchDbId(t, diTestStateConfigDbIds)
		diTestPatchDbTcpAddr(t, func(db string, ns string) (string, error) {
			if db == "STATE_DB" {
				return mr.Addr(), nil
			}
			return "", errors.New("di-test: no config tcp addr")
		})
		diTestRunMonitor(t)
	})

	t.Run("config db subscription fails", func(t *testing.T) {
		diTestPatchDbId(t, diTestStateConfigDbIds)
		diTestPatchDbTcpAddr(t, func(db string, ns string) (string, error) {
			if db == "STATE_DB" {
				return mr.Addr(), nil
			}
			return deadAddr, nil
		})
		diTestRunMonitor(t)
	})

	t.Run("config db sock error", func(t *testing.T) {
		diTestSetRedisTcp(t, false)
		diTestPatchDbId(t, diTestStateConfigDbIds)
		diTestPatchDbSock(t, func(db string, ns string) (string, error) {
			if db == "STATE_DB" {
				return sock, nil
			}
			return "", errors.New("di-test: no config unix socket path")
		})
		diTestRunMonitor(t)
	})

	t.Run("config db unix socket subscription fails", func(t *testing.T) {
		diTestSetRedisTcp(t, false)
		diTestPatchDbId(t, diTestStateConfigDbIds)
		diTestPatchDbSock(t, func(db string, ns string) (string, error) {
			if db == "STATE_DB" {
				return sock, nil
			}
			return filepath.Join(os.TempDir(), "di-test-missing", "redis.sock"), nil
		})
		diTestRunMonitor(t)
	})
}

func TestDiCacheStartMonitoring(t *testing.T) {
	mr := diTestStartMiniRedis(t)
	diTestSetRedisTcp(t, true)
	diTestPatchDefaultDbConfig(t, mr.Addr())

	c := &deviceInfoCache{
		updateCh: make(chan struct{}, 16),
		stopCh:   make(chan struct{}),
	}
	go c.startMonitoring()

	// Wait until the monitor subscribed and forwards keyspace events.
	diTestPublishUntilReceived(t, mr, c.updateCh, diTestStateKeyspaceChannel(), "hset", 10*time.Second)

	// Closing stopCh must shut the monitoring down.
	close(c.stopCh)
	diTestWaitQuiescent(t, c.updateCh, 300*time.Millisecond, 10*time.Second)
	for i := 0; i < 3; i++ {
		mr.Publish(diTestStateKeyspaceChannel(), "hset")
		time.Sleep(100 * time.Millisecond)
	}
	select {
	case <-c.updateCh:
		t.Fatal("device info monitor kept forwarding notifications after stopCh was closed")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestDiAddDeviceInfoToResp(t *testing.T) {
	diTestSwapGlobalDeviceInfo(t, diTestNewCache("switch-1", "10.0.0.5"))

	t.Run("nil response", func(t *testing.T) {
		addDeviceInfoToResp(nil) // must not panic
	})

	t.Run("sync response is skipped", func(t *testing.T) {
		resp := &gpb.SubscribeResponse{
			Response: &gpb.SubscribeResponse_SyncResponse{SyncResponse: true},
		}
		addDeviceInfoToResp(resp)
	})

	t.Run("update without path is skipped", func(t *testing.T) {
		notif := &gpb.Notification{
			Timestamp: 1,
			Prefix:    &gpb.Path{Target: "openconfig"},
			Update: []*gpb.Update{
				{Path: nil},
			},
		}
		resp := &gpb.SubscribeResponse{
			Response: &gpb.SubscribeResponse_Update{Update: notif},
		}
		addDeviceInfoToResp(resp)
		if notif.Update[0].Path != nil {
			t.Fatalf("path created for an update without a path: %v", notif.Update[0].Path)
		}
	})

	t.Run("device info is appended to the path", func(t *testing.T) {
		path := &gpb.Path{Elem: []*gpb.PathElem{{Name: "interfaces", Key: map[string]string{"name": "Ethernet0"}}}}
		notif := &gpb.Notification{
			Timestamp: 2,
			Prefix:    &gpb.Path{Target: "openconfig"},
			Update:    []*gpb.Update{{Path: path}},
		}
		resp := &gpb.SubscribeResponse{
			Response: &gpb.SubscribeResponse_Update{Update: notif},
		}

		addDeviceInfoToResp(resp)

		// The implementation installs a fresh gpb.Path with the device info
		// element appended, so read the path back from the update.
		newPath := notif.Update[0].Path
		if got := len(newPath.Elem); got != 2 {
			t.Fatalf("path has %d elements after addDeviceInfoToResp, want 2", got)
		}
		if newPath.Elem[0].Name != "interfaces" {
			t.Errorf("original path element was lost: %v", newPath.Elem[0])
		}
		deviceInfo := newPath.Elem[1]
		if deviceInfo.Name != "DeviceInfo" {
			t.Errorf("appended path element name = %q, want %q", deviceInfo.Name, "DeviceInfo")
		}
		if got := deviceInfo.Key["HostName"]; got != "switch-1" {
			t.Errorf("DeviceInfo HostName = %q, want %q", got, "switch-1")
		}
		if got := deviceInfo.Key["MgmtIp"]; got != "10.0.0.5" {
			t.Errorf("DeviceInfo MgmtIp = %q, want %q", got, "10.0.0.5")
		}
	})

	t.Run("device info is appended to every update", func(t *testing.T) {
		first := &gpb.Update{Path: &gpb.Path{Elem: []*gpb.PathElem{{Name: "a"}}}}
		second := &gpb.Update{Path: &gpb.Path{Elem: []*gpb.PathElem{{Name: "b"}}}}
		notif := &gpb.Notification{
			Timestamp: 3,
			Update:    []*gpb.Update{first, nil, second},
		}
		resp := &gpb.SubscribeResponse{
			Response: &gpb.SubscribeResponse_Update{Update: notif},
		}

		addDeviceInfoToResp(resp)

		for _, upd := range []*gpb.Update{first, second} {
			if got := len(upd.Path.Elem); got != 2 {
				t.Fatalf("path has %d elements after addDeviceInfoToResp, want 2", got)
			}
			if upd.Path.Elem[1].Name != "DeviceInfo" {
				t.Fatalf("appended path element name = %q, want %q", upd.Path.Elem[1].Name, "DeviceInfo")
			}
		}
	})
}

func TestDiHandleNetworkChange(t *testing.T) {
	oldTrigger := networkChangeTrigger
	oldMap := ClientSubscriptionNameMap

	trigger := make(chan struct{}, 16)
	networkChangeTrigger = trigger

	cancelled := make(chan struct{}, 16)
	cs := &clientSubscription{
		name: "di-test",
		// An empty destination group makes NewInstance fail before it tries to
		// connect to the data source.
		destGroupName: "",
		cancel:        func() { cancelled <- struct{}{} },
	}
	ClientSubscriptionNameMap = map[string]*clientSubscription{"di-test": cs}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("handleNetworkChange did not stop")
		}
		networkChangeTrigger = oldTrigger
		ClientSubscriptionNameMap = oldMap
	})
	go func() {
		defer close(done)
		handleNetworkChange(ctx)
	}()

	// A network change must reconnect every registered client subscription.
	trigger <- struct{}{}
	diTestWaitChan(t, cancelled, 5*time.Second, "client subscription to be cancelled after a network change")

	// configMu must be released again after the reconnect attempt.
	unlocked := make(chan struct{})
	go func() {
		configMu.Lock()
		close(unlocked)
		configMu.Unlock()
	}()
	diTestWaitChan(t, unlocked, 5*time.Second, "configMu to be released after a network change")

	// Cancelling the context must stop the handler.
	cancel()
	diTestWaitChan(t, done, 5*time.Second, "handleNetworkChange to exit on context cancellation")
}
