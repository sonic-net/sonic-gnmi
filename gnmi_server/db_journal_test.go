package gnmi

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/redis/go-redis/v9"
	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
)

func TestNewDbJournalReturnsWhenOptionsAreUnavailable(t *testing.T) {
	socketErr := errors.New("local Redis socket lookup failed")
	dbErr := errors.New("local Redis DB lookup failed")
	tests := []struct {
		name      string
		socket    string
		socketErr error
		dbID      int
		dbErr     error
		wantErr   error
	}{
		{name: "SocketLookupError", socketErr: socketErr, wantErr: socketErr},
		{name: "DbLookupError", socket: "/var/run/redis/redis.sock", dbErr: dbErr, wantErr: dbErr},
		{name: "EmptySocket", dbID: 4},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			patches := gomonkey.NewPatches()
			patches.ApplyFunc(sdcfg.GetDbSock, func(dbName, namespace string) (string, error) {
				if dbName != "CONFIG_DB" {
					t.Errorf("GetDbSock database = %q, want CONFIG_DB", dbName)
				}
				return test.socket, test.socketErr
			})
			patches.ApplyFunc(sdcfg.GetDbId, func(dbName, namespace string) (int, error) {
				return test.dbID, test.dbErr
			})
			defer patches.Reset()

			journal, err := NewDbJournal("CONFIG_DB")
			if journal != nil {
				t.Fatal("NewDbJournal returned a journal without valid Redis options")
			}
			if err == nil {
				t.Fatal("NewDbJournal succeeded without valid Redis options")
			}
			if test.wantErr != nil && !errors.Is(err, test.wantErr) {
				t.Fatalf("NewDbJournal error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestNewDbJournal(t *testing.T) {
	tests := []struct {
		desc    string
		db      string
		wantErr bool
	}{
		{
			desc:    "Success",
			db:      "CONFIG_DB",
			wantErr: false,
		},
		{
			desc:    "InvalidDb",
			db:      "INVALID_DB",
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			journal, err := NewDbJournal(test.db)
			if err == nil {
				journal.Close()
			}

			if test.wantErr != (err != nil) {
				t.Fatalf("NewDbJournal did not return the expected error - wantErr=%v, err=%v", test.wantErr, err)
			}
		})
	}
}

func TestDbJournalInit(t *testing.T) {
	tests := []struct {
		desc    string
		dbj     *DbJournal
		wantErr bool
	}{
		{
			desc:    "Success",
			dbj:     nil,
			wantErr: false,
		},
		{
			desc: "NilRedisClient",
			dbj: &DbJournal{
				database: "CONFIG_DB",
				rc:       nil,
			},
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			var err error
			if test.dbj == nil {
				if test.dbj, err = NewDbJournal("CONFIG_DB"); err != nil {
					t.Fatalf("Failed to create new DbJournal: %v", err)
				}
				defer test.dbj.Close()
			}
			err = test.dbj.init()

			if test.wantErr != (err != nil) {
				t.Fatalf("init did not return the expected error - wantErr=%v, err=%v", test.wantErr, err)
			}
		})
	}
}

func TestDbJournalUpdateCache(t *testing.T) {
	tests := []struct {
		desc    string
		dbj     *DbJournal
		event   []string
		wantErr bool
	}{
		{
			desc:    "SuccessHSet",
			dbj:     nil,
			event:   []string{"hset", "PORT|Ethernet1"},
			wantErr: false,
		},
		{
			desc:    "SuccessDel",
			dbj:     nil,
			event:   []string{"del", "PORT|Ethernet1"},
			wantErr: false,
		},
		{
			desc: "NilCache",
			dbj: &DbJournal{
				rc:    &redis.Client{},
				cache: nil,
			},
			event:   []string{"hset", "PORT|Ethernet1"},
			wantErr: true,
		},
		{
			desc: "NilRedisClient",
			dbj: &DbJournal{
				rc:    nil,
				cache: map[string]map[string]string{},
			},
			event:   []string{"hset", "PORT|Ethernet1"},
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			var err error
			if test.dbj == nil {
				if test.dbj, err = NewDbJournal("CONFIG_DB"); err != nil {
					t.Fatalf("Failed to create new DbJournal: %v", err)
				}
				defer test.dbj.Close()
			}
			_, err = test.dbj.updateCache(test.event)

			if test.wantErr != (err != nil) {
				t.Fatalf("init did not return the expected error - wantErr=%v, err=%v", test.wantErr, err)
			}
		})
	}
}

func TestDbJournalRotateFile(t *testing.T) {
	// Set up a DbJournal
	dbj, err := NewDbJournal("CONFIG_DB")
	if err != nil {
		t.Fatalf("Failed to create NewDbJournal: %v", err)
	}
	defer dbj.Close()

	// If DbJournal has a nil file pointer, it should be handled by rotateFile()
	dbj.file = nil
	if err := dbj.rotateFile(); err != nil {
		t.Fatalf("Rotate failed because of nil file pointer: %v", err)
	}

	// Fill the file a few times to make sure rotate is working correctly
	for i := 0; i < maxBackups+2; i++ {
		// Make sure the file was created and open it
		file, err := os.OpenFile(HostVarLogPath+"/config_db.txt", os.O_APPEND|os.O_CREATE|os.O_RDWR, 0644)
		if err != nil {
			t.Fatalf("Failed to open DbJournal file: %v", err)
		}

		// Fill the file to reach 5MB
		if err := file.Truncate(maxFileSize); err != nil {
			t.Fatalf("Failed to write to DbJournal file: %v", err)
		}

		if err := dbj.rotateFile(); err != nil {
			t.Fatalf("rotateFile failed: %v", err)
		}

		time.Sleep(1 * time.Second)

		// Make sure the file was rotated
		fileStat, err := os.Stat(HostVarLogPath + "/config_db.txt")
		if err != nil {
			t.Fatalf("Couldn't find DbJournal file: %v", err)
		}
		if fileStat.Size() >= 10000 {
			t.Fatalf("DbJournal file was not rotated: size=%v", fileStat.Size())
		}
	}

	zippedFiles := 0
	journalFiles := 0
	files, err := os.ReadDir(HostVarLogPath)
	if err != nil {
		t.Fatalf("Failed to read HostVarLog dir: %v", err)
	}
	for _, file := range files {
		if file.Name() == "config_db.txt" {
			journalFiles++
		}
		if strings.HasPrefix(file.Name(), "config_db") && strings.HasSuffix(file.Name(), ".gz") {
			zippedFiles++
		}
	}
	if journalFiles != 1 || zippedFiles != maxBackups {
		t.Fatalf("Files not rotated correctly: journalFiles=%v, zippedFiles=%v", journalFiles, zippedFiles)
	}

}
