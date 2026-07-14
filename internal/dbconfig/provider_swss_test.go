//go:build !pure

package dbconfig

import "testing"

func TestSwssProviderContract(t *testing.T) {
	if err := Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	runProviderContract(t, providerContract{
		database:  "CONFIG_DB",
		namespace: DefaultNamespace,
		id:        4,
		separator: "|",
		socket:    "/var/run/redis/redis.sock",
		address:   "127.0.0.1:6379",
	})
}
