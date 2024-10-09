package common_utils

import (
	"fmt"

	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
	"github.com/go-redis/redis"
	log "github.com/golang/glog"
)

const (
	dbName              = "STATE_DB"
)

func getRedisDBClient() (*redis.Client, error) {
	rclient := redis.NewClient(&redis.Options{
		Network:     "tcp",
		Addr:        sdcfg.GetDbTcpAddr(dbName),
		Password:    "", // no password set
		DB:          sdcfg.GetDbId(dbName),
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
