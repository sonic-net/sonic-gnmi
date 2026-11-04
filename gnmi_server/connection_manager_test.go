package gnmi

import (
	"errors"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
)

func TestPrepareRedisReturnsWhenOptionsAreUnavailable(t *testing.T) {
	tests := []struct {
		name      string
		socket    string
		socketErr error
		dbID      int
		dbErr     error
	}{
		{name: "SocketLookupError", socketErr: errors.New("local Redis socket lookup failed")},
		{name: "DbLookupError", socket: "/var/run/redis/redis.sock", dbErr: errors.New("local Redis DB lookup failed")},
		{name: "EmptySocket", dbID: 6},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			patches := gomonkey.NewPatches()
			patches.ApplyFunc(sdcfg.GetDbSock, func(dbName, namespace string) (string, error) {
				if dbName != "STATE_DB" {
					t.Errorf("GetDbSock database = %q, want STATE_DB", dbName)
				}
				return test.socket, test.socketErr
			})
			patches.ApplyFunc(sdcfg.GetDbId, func(dbName, namespace string) (int, error) {
				return test.dbID, test.dbErr
			})
			defer patches.Reset()

			previousClient := rclient
			rclient = nil
			defer func() {
				rclient = previousClient
			}()

			(&ConnectionManager{}).PrepareRedis()

			if rclient != nil {
				t.Fatal("PrepareRedis created a client without valid Redis options")
			}
		})
	}
}
