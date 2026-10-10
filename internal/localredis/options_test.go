package localredis

import (
	"errors"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
)

func TestOptionsUsesConfiguredUnixSocket(t *testing.T) {
	patches := gomonkey.ApplyFunc(sdcfg.GetDbSock, func(dbName, namespace string) (string, error) {
		if dbName != "STATE_DB" || namespace != "asic0" {
			t.Fatalf("GetDbSock(%q, %q), want STATE_DB, asic0", dbName, namespace)
		}
		return "/var/run/redis0/redis.sock", nil
	})
	defer patches.Reset()
	patches.ApplyFunc(sdcfg.GetDbId, func(dbName, namespace string) (int, error) {
		if dbName != "STATE_DB" || namespace != "asic0" {
			t.Fatalf("GetDbId(%q, %q), want STATE_DB, asic0", dbName, namespace)
		}
		return 6, nil
	})

	opts, err := Options("STATE_DB", "asic0")
	if err != nil {
		t.Fatalf("Options returned error: %v", err)
	}
	if opts.Network != "unix" {
		t.Fatalf("Network = %q, want unix", opts.Network)
	}
	if opts.Addr != "/var/run/redis0/redis.sock" {
		t.Fatalf("Addr = %q, want /var/run/redis0/redis.sock", opts.Addr)
	}
	if opts.DB != 6 {
		t.Fatalf("DB = %d, want 6", opts.DB)
	}
}

func TestOptionsReturnsEndpointErrors(t *testing.T) {
	t.Run("socket", func(t *testing.T) {
		patch := gomonkey.ApplyFunc(sdcfg.GetDbSock, func(string, string) (string, error) {
			return "", errors.New("no socket")
		})
		defer patch.Reset()

		if _, err := Options("STATE_DB", ""); err == nil {
			t.Fatal("Options succeeded without a socket")
		}
	})

	t.Run("empty socket", func(t *testing.T) {
		patch := gomonkey.ApplyFunc(sdcfg.GetDbSock, func(string, string) (string, error) {
			return "", nil
		})
		defer patch.Reset()

		if _, err := Options("STATE_DB", ""); err == nil {
			t.Fatal("Options succeeded with an empty socket path")
		}
	})

	t.Run("database id", func(t *testing.T) {
		patches := gomonkey.ApplyFunc(sdcfg.GetDbSock, func(string, string) (string, error) {
			return "/var/run/redis/redis.sock", nil
		})
		defer patches.Reset()
		patches.ApplyFunc(sdcfg.GetDbId, func(string, string) (int, error) {
			return -1, errors.New("no database")
		})

		if _, err := Options("STATE_DB", ""); err == nil {
			t.Fatal("Options succeeded without a database ID")
		}
	})
}
