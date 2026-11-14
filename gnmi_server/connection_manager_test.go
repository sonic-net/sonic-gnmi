package gnmi

import (
	"errors"
	"net"
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

func TestConnectionManagerKeysStreamsOnOneConnectionSeparately(t *testing.T) {
	previousClient := rclient
	rclient = nil
	defer func() {
		rclient = previousClient
	}()

	cm := &ConnectionManager{connections: map[string]struct{}{}, threshold: 2}
	addr := &net.TCPAddr{IP: net.ParseIP("10.0.0.5"), Port: 41234}
	query := `subscription:{path:{target:"STATE_DB"}}`

	keyA, ok := cm.Add(addr, query, 1)
	if !ok {
		t.Fatal("Add for stream 1 rejected")
	}
	keyB, ok := cm.Add(addr, query, 2)
	if !ok {
		t.Fatal("Add for stream 2 rejected")
	}
	if keyA == keyB {
		t.Fatalf("streams 1 and 2 share key %q", keyA)
	}
	if _, ok := cm.Add(addr, query, 3); ok {
		t.Error("Add for stream 3 accepted, want threshold 2 to count both live streams")
	}

	cm.Remove(keyA)
	if _, ok := cm.connections[keyB]; !ok {
		t.Errorf("removing stream 1 dropped stream 2's key %q", keyB)
	}
	if len(cm.connections) != 1 {
		t.Errorf("got %d tracked streams after removing one, want 1", len(cm.connections))
	}
}
