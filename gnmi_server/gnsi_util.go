package gnmi

import (
	"fmt"
	"github.com/Azure/sonic-mgmt-common/translib/db"
	"github.com/go-redis/redis/v7"
	log "github.com/golang/glog"
	lvl "github.com/sonic-net/sonic-gnmi/gnmi_server/log"
	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
	"strings"
	"sync"
)

const (
	credentialsTbl string = "CREDENTIALS"
)

var (
	// Mutex for DB writes
	dbWriteMutex sync.Mutex
)

// writeCredentialsMetadataToDB writes the credentials freshness data to the DB.
func writeCredentialsMetadataToDB(tbl, key, fld, val string) error {
	sc, err := getRedisDBClient(stateDB)
	if err != nil {
		log.V(lvl.ERROR).Info(err.Error())
		return fmt.Errorf("REDIS is not available.")
	}
	defer db.CloseRedisClient(sc)
	// Write metadata.
	path := getKey([]string{credentialsTbl, tbl})
	if len(key) > 0 {
		path = getKey([]string{path, key})
	}
	dbWriteMutex.Lock()
	err = sc.HSet(path, fld, val).Err()
	dbWriteMutex.Unlock()
	if err != nil {
		log.V(lvl.ERROR).Infof("Cannot write credentials metadata to the DB. [path:'%v', fld:'%v', val:'%v']", path, fld, val)
		return err
	}
	log.V(lvl.DEBUG).Infof("Successfully wrote credentials metadata to the DB. [path:'%v', fld:'%v', val:'%v']", path, fld, val)
	return nil
}

// Creates and returns a new REDIS client for the supplied DB.
func getRedisDBClient(dbName string) (*redis.Client, error) {
	ns, _ := sdcfg.GetDbDefaultNamespace()
	addr, _ := sdcfg.GetDbTcpAddr(dbName, ns)
	id, _ := sdcfg.GetDbId(dbName, ns)
	rclient := db.TransactionalRedisClientWithOpts(&redis.Options{
		Network:     "tcp",
		Addr:        addr,
		Password:    "", // no password set
		DB:          id,
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

// getKey generates the hash key from the supplied string array.
func getKey(k []string) string {
	return strings.Join(k, "|")
}
