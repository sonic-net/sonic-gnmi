package redisopts

import (
	"fmt"

	"github.com/redis/go-redis/v9"
	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
)

// LocalDB returns Redis options for a database in the same SONiC host or
// namespace. The socket path and database ID come from SONiC database
// metadata, so the caller does not need a TCP listener for local access.
func LocalDB(dbName, namespace string) (*redis.Options, error) {
	addr, err := sdcfg.GetDbSock(dbName, namespace)
	if err != nil {
		return nil, fmt.Errorf("get %s socket for namespace %q: %w", dbName, namespace, err)
	}
	if addr == "" {
		return nil, fmt.Errorf("get %s socket for namespace %q: empty socket path", dbName, namespace)
	}

	dbID, err := sdcfg.GetDbId(dbName, namespace)
	if err != nil {
		return nil, fmt.Errorf("get %s id for namespace %q: %w", dbName, namespace, err)
	}

	return New(redis.Options{
		Network:     "unix",
		Addr:        addr,
		DB:          dbID,
		DialTimeout: 0,
	}), nil
}
