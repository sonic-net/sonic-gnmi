package gnmi

import (
	"errors"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/redis/go-redis/v9"
	"github.com/sonic-net/sonic-gnmi/localredis"
)

func TestPrepareRedisReturnsWhenOptionsFail(t *testing.T) {
	wantErr := errors.New("local Redis options failed")
	calls := 0
	patches := gomonkey.ApplyFunc(localredis.Options, func(dbName, namespace string) (*redis.Options, error) {
		calls++
		if dbName != "STATE_DB" {
			t.Errorf("Options database = %q, want STATE_DB", dbName)
		}
		return nil, wantErr
	})
	defer patches.Reset()

	previousClient := rclient
	rclient = nil
	defer func() {
		rclient = previousClient
	}()

	(&ConnectionManager{}).PrepareRedis()

	if calls != 1 {
		t.Fatalf("Options called %d times, want 1", calls)
	}
	if rclient != nil {
		t.Fatal("PrepareRedis created a client after Options failed")
	}
}
