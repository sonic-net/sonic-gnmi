package common_utils

import (
	"fmt"

	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
	"github.com/go-redis/redis"
)

const (
	dbName              = "STATE_DB"
)

func getRedisDBClient() (*redis.Client, error) {
	ns, _ := sdcfg.GetDbDefaultNamespace()
	addr, err := sdcfg.GetDbTcpAddr(dbName, ns)
	if err != nil {
		log.Errorf("Addr err: %v", err)
		return
	}
	db, err := sdcfg.GetDbId("STATE_DB", ns)
	if err != nil {
		log.Errorf("DB err: %v", err)
		return
	}
	rclient := redis.NewClient(&redis.Options{
		Network:     "tcp",
		Addr:        addr,
		Password:    "", // no password set
		DB:          db,
		DialTimeout: 0,
	})
	if rclient == nil {
		return nil, fmt.Errorf("Cannot create redis client.")
	}
	if _, err := rclient.Ping().Result(); err != nil {
		return nil, err
	}
	return rclient, nil
}
