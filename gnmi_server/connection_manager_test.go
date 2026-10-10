package gnmi

import (
	"errors"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
)

func TestPrepareRedisReturnsWhenSocketLookupFails(t *testing.T) {
	wantErr := errors.New("local Redis socket lookup failed")
	calls := 0
	patches := gomonkey.ApplyFunc(sdcfg.GetDbSock, func(dbName, namespace string) (string, error) {
		calls++
		if dbName != "STATE_DB" {
			t.Errorf("GetDbSock database = %q, want STATE_DB", dbName)
		}
		return "", wantErr
	})
	defer patches.Reset()

	previousClient := rclient
	rclient = nil
	defer func() {
		rclient = previousClient
	}()

	(&ConnectionManager{}).PrepareRedis()

	if calls != 1 {
		t.Fatalf("GetDbSock called %d times, want 1", calls)
	}
	if rclient != nil {
		t.Fatal("PrepareRedis created a client after socket lookup failed")
	}
}
