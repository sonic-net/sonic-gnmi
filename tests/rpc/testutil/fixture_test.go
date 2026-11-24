//go:build e2e

package testutil

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestFixtureRollback(t *testing.T) {
	env := NewFixture(t)
	ctx := context.Background()
	count, err := env.DB.DBSize(ctx).Result()
	check(t, err)
	if count != 0 {
		t.Fatal("fixture must start with an empty database")
	}
	check(t, env.DB.HSet(ctx, "original", "field", "before").Err())
	before, err := env.checkpoint(ctx)
	check(t, err)
	extraCleaned := false
	env.Run(t, "changes", func(t *testing.T) {
		check(t, env.DB.HSet(ctx, "original", "field", "after").Err())
		check(t, env.DB.Set(ctx, "new", "value", 0).Err())
		t.Cleanup(func() {
			value, err := env.DB.HGet(ctx, "original", "field").Result()
			check(t, err)
			if value != "after" {
				t.Error("rollback ran before extra cleanup")
			}
			extraCleaned = true
		})
	})
	env.Run(t, "next", func(t *testing.T) {
		after, err := env.checkpoint(ctx)
		check(t, err)
		if !extraCleaned || !reflect.DeepEqual(before, after) {
			t.Fatal("checkpoint was not restored")
		}
	})
}

type captureHook struct {
	before func(redis.Cmder)
	after  func(redis.Cmder)
}

func (h *captureHook) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) { return next(ctx, network, addr) }
}
func (h *captureHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h *captureHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if h.before != nil {
			h.before(cmd)
		}
		err := next(ctx, cmd)
		if h.after != nil {
			h.after(cmd)
		}
		return err
	}
}

func TestCheckpointExpiry(t *testing.T) {
	for _, stage := range []string{"dump", "pttl", "zero"} {
		t.Run(stage, func(t *testing.T) {
			env := NewFixture(t)
			ctx := context.Background()
			control := redis.NewClient(&redis.Options{Network: "unix", Addr: env.Socket, DB: 4})
			defer control.Close()
			check(t, env.DB.Set(ctx, "expiring", "value", time.Minute).Err())
			hook := &captureHook{}
			hook.before = func(cmd redis.Cmder) {
				if cmd.Name() == stage {
					check(t, control.Del(ctx, "expiring").Err())
				}
			}
			hook.after = func(cmd redis.Cmder) {
				if stage == "zero" && cmd.Name() == "pttl" {
					cmd.(*redis.DurationCmd).SetVal(0)
				}
			}
			env.DB.AddHook(hook)
			state, err := env.checkpoint(ctx)
			check(t, err)
			hook.before, hook.after = nil, nil
			if stage == "zero" {
				if state["expiring"].TTL <= 0 {
					t.Fatalf("expiring key captured as persistent: %+v", state)
				}
			} else if len(state) != 0 {
				t.Fatalf("disappeared key retained: %+v", state)
			}
			check(t, env.rollback(ctx, state))
			if ttl := env.DB.PTTL(ctx, "expiring").Val(); ttl == -1 {
				t.Fatal("expired key resurrected permanently")
			}
		})
	}
}

func TestRollbackHashEncoding(t *testing.T) {
	env := NewFixture(t)
	ctx := context.Background()
	for i := 0; i < 600; i++ {
		check(t, env.DB.HSet(ctx, "hash", fmt.Sprint(i), "value").Err())
	}
	for i := 1; i < 600; i++ {
		check(t, env.DB.HDel(ctx, "hash", fmt.Sprint(i)).Err())
	}
	if encoding := env.DB.ObjectEncoding(ctx, "hash").Val(); encoding != "hashtable" {
		t.Fatalf("expected promoted hash, got %s", encoding)
	}
	state, err := env.checkpoint(ctx)
	check(t, err)
	check(t, env.DB.HSet(ctx, "hash", "0", "changed").Err())
	check(t, env.rollback(ctx, state))
	if value := env.DB.HGet(ctx, "hash", "0").Val(); value != "value" {
		t.Fatalf("restored hash value = %q", value)
	}
}

func TestRollbackLogicalValues(t *testing.T) {
	env := NewFixture(t)
	ctx := context.Background()
	check(t, env.DB.Set(ctx, "string", "value", 0).Err())
	check(t, env.DB.Set(ctx, "ttl", "value", time.Minute).Err())
	check(t, env.DB.RPush(ctx, "list", "b", "a").Err())
	check(t, env.DB.SAdd(ctx, "set", "b", "a").Err())
	check(t, env.DB.ZAdd(ctx, "zset", redis.Z{Score: 2, Member: "b"}, redis.Z{Score: 1, Member: "a"}).Err())
	check(t, env.DB.XAdd(ctx, &redis.XAddArgs{Stream: "stream", Values: map[string]any{"field": "value"}}).Err())
	state, err := env.checkpoint(ctx)
	check(t, err)
	check(t, env.DB.FlushDB(ctx).Err())
	check(t, env.rollback(ctx, state))
	if env.DB.PTTL(ctx, "string").Val() != -1 || env.DB.PTTL(ctx, "ttl").Val() <= 0 {
		t.Fatal("rollback changed persistence")
	}
	// RESTORE still succeeds, but a subsequent writer corrupts the value.
	control := redis.NewClient(&redis.Options{Network: "unix", Addr: env.Socket, DB: 4})
	defer control.Close()
	hook := &captureHook{after: func(cmd redis.Cmder) {
		if cmd.Name() == "restore" && cmd.Args()[1] == "string" {
			check(t, control.Set(ctx, "string", "corrupted", 0).Err())
		}
	}}
	env.DB.AddHook(hook)
	if err := env.rollback(ctx, state); err == nil {
		t.Fatal("rollback accepted changed logical contents")
	}
}

func TestRollbackFailureStopsGroup(t *testing.T) {
	if os.Getenv("RPC_ROLLBACK_FAILURE_CHILD") == "1" {
		env := NewFixture(t)
		env.Run(t, "break-rollback", func(t *testing.T) { check(t, env.DB.Close()) })
		t.Fatal("FOLLOWING_CASE_EXECUTED")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRollbackFailureStopsGroup$", "-test.v")
	command.Env = append(os.Environ(), "RPC_ROLLBACK_FAILURE_CHILD=1")
	output, err := command.CombinedOutput()
	if err == nil || ctx.Err() != nil || strings.Contains(string(output), "FOLLOWING_CASE_EXECUTED") ||
		!strings.Contains(string(output), "environment restoration failed; stopping group") {
		t.Fatalf("expected bounded group abort after rollback failure, got %v: %s", err, output)
	}
}
