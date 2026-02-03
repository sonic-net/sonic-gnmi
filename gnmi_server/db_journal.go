package gnmi

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	log "github.com/golang/glog"
	"github.com/redis/go-redis/v9"

	"github.com/Azure/sonic-mgmt-common/translib/db"
	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
)

const (
	maxFileSize = 2000000 // Bytes
	maxBackups  = 1
)

type DbJournal struct {
	database      string
	rc            *redis.Client
	ps            *redis.PubSub
	notifications <-chan *redis.Message
	cache         map[string]map[string]string
	file          *os.File
	fileName      string
	done          chan bool
}

var dbNums = map[string]db.DBNum{
	"CONFIG_DB": db.ConfigDB,
	"STATE_DB":  db.StateDB,
}

// NewDbJournal returns a new DbJournal for the specified database.
func NewDbJournal(database string) (*DbJournal, error) {
	var err error
	journal := &DbJournal{}
	journal.database = database
	dbNum, ok := dbNums[journal.database]
	if !ok {
		return nil, errors.New("Invalid database passed into NewDbJournal")
	}

	ns, _ := sdcfg.GetDbDefaultNamespace()
	addr, _ := sdcfg.GetDbTcpAddr(journal.database, ns)
	dbId, _ := sdcfg.GetDbId(journal.database, ns)
	journal.rc = db.TransactionalRedisClientWithOpts(&redis.Options{
		Network:     "tcp",
		Addr:        addr,
		Password:    "",
		DB:          dbId,
		DialTimeout: 0,
	})

	if err = journal.init(); err != nil {
		return nil, err
	}

	keyspace := fmt.Sprintf("__keyspace@%d__:*", dbNum)
	keyevent := fmt.Sprintf("__keyevent@%d__:*", dbNum)
	journal.ps = journal.rc.PSubscribe(context.Background(), keyspace, keyevent)
	if _, err = journal.ps.Receive(context.Background()); err != nil {
		return nil, err
	}

	journal.notifications = journal.ps.Channel()

	journal.fileName = filepath.Join(HostVarLogPath, strings.ToLower(journal.database)+".txt")
	if journal.file, err = os.OpenFile(journal.fileName, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0644); err != nil {
		return nil, err
	}

	journal.done = make(chan bool, 1)

	go journal.journal()
	log.V(2).Infof("Successfully started the DbJournal for %v", journal.database)
	return journal, nil
}

// Close closes the redis objects and the journal file.
func (dbj *DbJournal) Close() {
	if dbj == nil {
		return
	}
	dbj.done <- true
}

func (dbj *DbJournal) cleanup() {
	if dbj == nil {
		return
	}
	if dbj.ps != nil {
		dbj.ps.Close()
	}
	if dbj.rc != nil {
		db.CloseRedisClient(dbj.rc)
		dbj.rc = nil
	}
	if dbj.file != nil {
		dbj.file.Close()
	}
	if dbj.cache != nil {
		dbj.cache = map[string]map[string]string{}
	}
	log.V(2).Infof("DbJournal closed successfully!")
}

// init initializes the journal's cache.
func (dbj *DbJournal) init() error {
	if dbj == nil || dbj.rc == nil {
		return errors.New("DbJournal: redis client is nil")
	}
	dbj.cache = map[string]map[string]string{}
	keys, kErr := dbj.rc.Keys(context.Background(), "*").Result()
	if kErr != nil {
		return kErr
	}
	for _, key := range keys {
		entry, eErr := dbj.rc.HGetAll(context.Background(), key).Result()
		if eErr != nil {
			entry = map[string]string{}
		}
		dbj.cache[key] = entry
	}
	return nil
}

// journal monitors the database notifications and logs events to the file.
func (dbj *DbJournal) journal() {
	if dbj == nil {
		return
	}
	defer dbj.cleanup()
	var event []string
	for {
		select {
		case msg := <-dbj.notifications:
			event = append(event, msg.Payload)
			if len(event) != 2 {
				continue
			}
			op := event[0]
			table := event[1]
			entry := fmt.Sprintf("%v: %v %v", time.Now().Format("2006-01-02.15:04:05.000000"), op, table)
			diff, dErr := dbj.updateCache(event)
			if dErr != nil {
				log.V(0).Infof("Shutting down %v Journal: %v", dbj.database, dErr)
				return
			}
			event = []string{}

			if diff != "" {
				entry += " " + diff
			}
			// If no fields were changed or the operation is a set on a table that contains the DB name, don't log the event.
			if (diff == "" && (op == "hset" || op == "hdel")) || (op == "set" && strings.Contains(table, dbj.database)) {
				continue
			}

			if err := dbj.rotateFile(); err != nil {
				log.V(0).Infof("Shutting down DbJournal, failed to manage file rotation: %v", err)
				return
			}
			_, writeErr := dbj.file.Write([]byte(entry + "\n"))
			if writeErr != nil {
				log.V(0).Infof("Failed to write to DbJournal file: %v", writeErr)
			}
		case <-dbj.done:
			return
		}
	}
}

// updateCache updates the cache with the latest database entry and returns the diff.
func (dbj *DbJournal) updateCache(event []string) (string, error) {
	op := event[0]
	table := event[1]
	if dbj == nil || dbj.cache == nil || dbj.rc == nil {
		return "", errors.New("nil members present in DbJournal")
	}
	oldEntry, ok := dbj.cache[table]
	if !ok {
		oldEntry = map[string]string{}
	}
	newEntry, err := dbj.rc.HGetAll(context.Background(), table).Result()
	if err != nil {
		newEntry = map[string]string{}
	}
	// Update the cache
	dbj.cache[table] = newEntry

	if op == "del" {
		return "", nil
	}

	diff := ""
	// Find deleted and changed fields
	for k, v := range oldEntry {
		newVal, ok := newEntry[k]
		if !ok {
			diff += "-" + k + " "
			continue
		}
		if newVal != v {
			diff += k + "=" + newVal + " "
		}
	}

	// Find added fields
	for k, v := range newEntry {
		if _, ok := oldEntry[k]; !ok {
			diff += "+" + k + ":" + v + " "
		}
	}

	return diff, nil
}

// rotateFile makes sure the journal file is opened correctly and rotates it
// if it exceeds the maximum size.
func (dbj *DbJournal) rotateFile() error {
	if dbj == nil {
		return errors.New("Couldn't rotate file, DbJournal is nil")
	}
	fileStat, err := os.Stat(dbj.fileName)
	if err != nil || dbj.file == nil {
		// File does not exist or it is closed, create/open it
		if dbj.file, err = os.OpenFile(dbj.fileName, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0644); err != nil {
			return err
		}
		return nil
	}

	if fileStat.Size() >= maxFileSize {
		// Close the journal file and open it as read-only to copy it
		dbj.file.Close()
		if dbj.file, err = os.OpenFile(dbj.fileName, os.O_RDONLY, 0644); err != nil {
			return err
		}

		// Remove a rotated, zipped file if the maxBackups limit is reached
		files, err := os.ReadDir(HostVarLogPath)
		if err != nil {
			return err
		}
		var count uint
		var oldest string
		for _, file := range files {
			if strings.HasPrefix(file.Name(), strings.ToLower(dbj.database)) && strings.HasSuffix(file.Name(), ".gz") {
				count++
				if strings.Compare(file.Name(), oldest) == -1 || oldest == "" {
					oldest = file.Name()
				}
			}
		}
		if count >= maxBackups {
			if err := os.Remove(filepath.Join(HostVarLogPath, oldest)); err != nil {
				return err
			}
		}

		// Compress the file
		zipName := filepath.Join(HostVarLogPath, strings.ToLower(dbj.database)+"_"+time.Now().Format("20060102150405")+".gz")
		zipFile, err := os.Create(zipName)
		if err != nil {
			return err
		}
		defer zipFile.Close()
		zipWriter := gzip.NewWriter(zipFile)
		defer zipWriter.Close()

		if _, err = io.Copy(zipWriter, dbj.file); err != nil {
			return err
		}
		if err = zipWriter.Flush(); err != nil {
			return err
		}

		// Recreate the journal file
		if dbj.file, err = os.Create(dbj.fileName); err != nil {
			return err
		}
	}
	return nil
}
