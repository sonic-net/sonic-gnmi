//go:build e2e

package testutil

import (
	"context"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
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
