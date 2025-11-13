package gnmi

import (
	"fmt"
	"github.com/Azure/sonic-mgmt-common/translib/db"
	"github.com/go-redis/redis/v7"
	log "github.com/golang/glog"
	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
	"io"
	"os"
	"path/filepath"
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
		log.V(0).Info(err.Error())
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
		log.V(0).Infof("Cannot write credentials metadata to the DB. [path:'%v', fld:'%v', val:'%v']", path, fld, val)
		return err
	}
	log.V(3).Infof("Successfully wrote credentials metadata to the DB. [path:'%v', fld:'%v', val:'%v']", path, fld, val)
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

func copyFile(srcPath, dstPath string) error {
	srcStat, err := os.Stat(srcPath)
	if err != nil {
		return err
	}
	if !srcStat.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", srcPath)
	}
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()
	tmpDst, err := os.CreateTemp(filepath.Dir(dstPath), filepath.Base(dstPath))
	if err != nil {
		return err
	}
	if _, err := io.Copy(tmpDst, src); err != nil {
		if e := os.Remove(tmpDst.Name()); e != nil {
			log.V(2).Infof("Failed to cleanup file: %v: %v", tmpDst.Name(), e)
		}
		return err
	}
	if err := tmpDst.Close(); err != nil {
		if e := os.Remove(tmpDst.Name()); e != nil {
			log.V(2).Infof("Failed to cleanup file: %v: %v", tmpDst.Name(), e)
		}
		return err
	}
	if err := os.Rename(tmpDst.Name(), dstPath); err != nil {
		if e := os.Remove(tmpDst.Name()); e != nil {
			log.V(2).Infof("Failed to cleanup file: %v: %v", tmpDst.Name(), e)
		}
		return err
	}
	return os.Chmod(dstPath, 0600)
}

func fileCheck(f string) error {
	srcStat, err := os.Stat(f)
	if err != nil {
		return err
	}
	if !srcStat.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", f)
	}
	return nil
}
