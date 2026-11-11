//go:build e2e

package rpc_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	pb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/redis/go-redis/v9"
	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
)

type environment struct {
	db     *redis.Client
	ca     *authority
	server tls.Certificate
	broken bool
}

func newEnvironment(t *testing.T) *environment {
	t.Helper()
	if _, err := os.Stat("/.dockerenv"); err != nil {
		t.Fatal("requires a dedicated disposable native container")
	}
	root := t.TempDir()
	socket := filepath.Join(root, "redis.sock")
	config := filepath.Join(root, "database_config.json")
	data, err := json.Marshal(map[string]any{
		"INSTANCES": map[string]any{"redis": map[string]any{"hostname": "127.0.0.1", "port": 6379, "unix_socket_path": socket}},
		"DATABASES": map[string]any{"CONFIG_DB": map[string]any{"id": 4, "separator": "|", "instance": "redis"}},
		"VERSION":   "1.0",
	})
	check(t, err)
	check(t, os.WriteFile(config, data, 0600))
	// SWSS configuration is process-global: groups run sequentially, not Parallel.
	t.Setenv("DB_CONFIG_PATH", config)
	check(t, sdcfg.Init())
	check(t, sdcfg.DbInit())
	t.Cleanup(func() { check(t, sdcfg.Init()) })
	log, err := os.Create(filepath.Join(root, "redis.log"))
	check(t, err)
	command := exec.Command("redis-server", "--port", "0", "--save", "", "--appendonly", "no",
		"--unixsocket", socket, "--unixsocketperm", "700", "--dir", root)
	command.Stdout, command.Stderr = log, log
	if err := command.Start(); err != nil {
		log.Close()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	t.Cleanup(func() {
		defer log.Close()
		_ = command.Process.Signal(syscall.SIGTERM)
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Redis exit: %v", err)
			}
		case <-time.After(5 * time.Second):
			_ = command.Process.Kill()
			<-done
			t.Error("Redis required forced kill")
		}
		if _, err := os.Stat(socket); !os.IsNotExist(err) {
			t.Error("Redis socket not removed")
		}
		t.Log("group cleanup: Redis stopped and owned socket removed")
	})
	db := redis.NewClient(&redis.Options{Network: "unix", Addr: socket, DB: 4,
		DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, MaxRetries: -1})
	t.Cleanup(func() { check(t, db.Close()) })
	waitFor(t, func(ctx context.Context) error { return db.Ping(ctx).Err() })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	check(t, db.Set(ctx, "CONFIG_DB_INITIALIZED", "1", 0).Err())
	ca := newAuthority(t)
	return &environment{db: db, ca: ca, server: ca.issue(t, "server", x509.ExtKeyUsageServerAuth)}
}

func (e *environment) serverTLS() *tls.Config {
	return &tls.Config{Certificates: []tls.Certificate{e.server}, ClientCAs: e.ca.pool,
		ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS12}
}

func (e *environment) clientTLS(t *testing.T, name string) *tls.Config {
	return &tls.Config{Certificates: []tls.Certificate{e.ca.issue(t, name, x509.ExtKeyUsageClientAuth)},
		RootCAs: e.ca.pool, MinVersion: tls.VersionTLS12}
}

func (e *environment) role(t *testing.T, name, role string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	check(t, e.db.HSet(ctx, "GNMI_CLIENT_CERT|"+name, "role@", role).Err())
}

func (e *environment) ready(t *testing.T, address string, request *pb.GetRequest) {
	t.Helper()
	e.role(t, "control", "gnmi_config_db_readwrite")
	client := pb.NewGNMIClient(newClient(t, address, e.clientTLS(t, "control")))
	waitFor(t, func(ctx context.Context) error {
		_, err := client.Get(ctx, request)
		return err
	})
}

func waitFor(t *testing.T, probe func(context.Context) error) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := probe(ctx)
		cancel()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("readiness: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

type savedKey struct {
	Value string
	TTL   time.Duration
}

// checkpoint covers all keys in the fixture-owned CONFIG_DB, including roles.
// DUMP/RESTORE preserves Redis value types. TTL uses the remaining duration at
// checkpoint; this fixture does not promise wall-clock expiry restoration.
func (e *environment) checkpoint(ctx context.Context) (map[string]savedKey, error) {
	keys, err := e.db.Keys(ctx, "*").Result()
	if err != nil {
		return nil, err
	}
	state := make(map[string]savedKey, len(keys))
	for _, key := range keys {
		value, err := e.db.Dump(ctx, key).Result()
		if err != nil {
			return nil, err
		}
		ttl, err := e.db.PTTL(ctx, key).Result()
		if err != nil {
			return nil, err
		}
		if ttl < 0 {
			ttl = 0
		}
		state[key] = savedKey{Value: value, TTL: ttl}
	}
	return state, nil
}

func (e *environment) rollback(ctx context.Context, state map[string]savedKey) error {
	// FLUSHDB is restricted to this environment's private Redis process and DB4.
	if err := e.db.FlushDB(ctx).Err(); err != nil {
		return err
	}
	for key, value := range state {
		if err := e.db.Restore(ctx, key, value.TTL, value.Value).Err(); err != nil {
			return err
		}
	}
	actual, err := e.checkpoint(ctx)
	if err != nil {
		return err
	}
	if len(actual) != len(state) {
		return fmt.Errorf("rollback key count mismatch")
	}
	for key, want := range state {
		got, ok := actual[key]
		if !ok || !bytes.Equal([]byte(got.Value), []byte(want.Value)) || (got.TTL == 0) != (want.TTL == 0) {
			return fmt.Errorf("rollback mismatch for key %q", key)
		}
	}
	return nil
}

// Run shares the group's server, but restores data after each sequential case.
// Case clients and extra t.Cleanup callbacks run before the first-registered
// rollback. A failed rollback poisons the environment and aborts the parent group.
func (e *environment) Run(t *testing.T, name string, body func(*testing.T)) {
	t.Helper()
	if e.broken {
		t.Fatal("environment restoration failed; stopping group")
	}
	t.Run(name, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		state, err := e.checkpoint(ctx)
		cancel()
		if err != nil {
			e.broken = true
			t.Fatal(err)
		}
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := e.rollback(ctx, state); err != nil {
				e.broken = true
				t.Errorf("rollback: %v", err)
			} else {
				t.Log("case rollback verified")
			}
		})
		body(t)
	})
	if e.broken {
		t.Fatal("environment restoration failed; stopping group")
	}
}
