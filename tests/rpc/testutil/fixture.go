//go:build e2e

package testutil

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

type Fixture struct {
	DB     *redis.Client
	Socket string
	broken bool
}

func NewFixture(t *testing.T) *Fixture {
	t.Helper()
	root := t.TempDir()
	socket := filepath.Join(root, "redis.sock")
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
	return &Fixture{DB: db, Socket: socket}
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
func (e *Fixture) checkpoint(ctx context.Context) (map[string]savedKey, error) {
	keys, err := e.DB.Keys(ctx, "*").Result()
	if err != nil {
		return nil, err
	}
	state := make(map[string]savedKey, len(keys))
	for _, key := range keys {
		value, err := e.DB.Dump(ctx, key).Result()
		if err != nil {
			return nil, err
		}
		ttl, err := e.DB.PTTL(ctx, key).Result()
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

func (e *Fixture) rollback(ctx context.Context, state map[string]savedKey) error {
	// FLUSHDB is restricted to this environment's private Redis process and DB4.
	if err := e.DB.FlushDB(ctx).Err(); err != nil {
		return err
	}
	for key, value := range state {
		if err := e.DB.Restore(ctx, key, value.TTL, value.Value).Err(); err != nil {
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
func (e *Fixture) Run(t *testing.T, name string, body func(*testing.T)) {
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
