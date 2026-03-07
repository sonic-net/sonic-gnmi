package gnmi

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-redis/redis/v7"
	"github.com/google/go-cmp/cmp" // Corrected import path for external builds
	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
)

// --- Test Helper to Restore Originals ---
func restoreGnmiFuncs() {
	getRedisDBClientFunc = getRedisDBClient
	closeRedisClientFunc = closeRedis
	newClientFunc = newRedisClient
	redisHSetFunc = func(c *redis.Client, key string, fields ...interface{}) error { return c.HSet(key, fields...).Err() }
	redisPingFunc = func(c *redis.Client) error { return c.Ping().Err() }
	getDbDefaultNamespaceFunc = sdcfg.GetDbDefaultNamespace
	getDbTcpAddrFunc = sdcfg.GetDbTcpAddr
	getDbIdFunc = sdcfg.GetDbId
}

// --- Test writeCredentialsMetadataToDB ---
func TestWriteCredentialsMetadataToDB(t *testing.T) {
	tests := []struct {
		name               string
		tbl, key, fld, val string
		getDBClientErr     error
		hsetErr            error
		wantErr            bool
		wantHSetKey        string
		wantHSetFld        string
		wantHSetVal        string
		wantCloseCalled    bool
	}{
		{
			name: "Success_NoKey",
			tbl:  "testTbl", key: "", fld: "field1", val: "value1",
			wantErr:     false,
			wantHSetKey: "CREDENTIALS|testTbl", wantHSetFld: "field1", wantHSetVal: "value1",
			wantCloseCalled: true,
		},
		{
			name: "Success_WithKey",
			tbl:  "testTbl", key: "someKey", fld: "field2", val: "value2",
			wantErr:     false,
			wantHSetKey: "CREDENTIALS|testTbl|someKey", wantHSetFld: "field2", wantHSetVal: "value2",
			wantCloseCalled: true,
		},
		{
			name: "Failure_GetRedisDBClientError",
			tbl:  "testTbl", fld: "f", val: "v",
			getDBClientErr:  errors.New("redis connection failed"),
			wantErr:         true,
			wantCloseCalled: false,
		},
		{
			name: "Failure_HSetError",
			tbl:  "testTbl", fld: "fld1", val: "val1",
			hsetErr:     errors.New("redis HSet write error"),
			wantErr:     true,
			wantHSetKey: "CREDENTIALS|testTbl", wantHSetFld: "fld1", wantHSetVal: "val1",
			wantCloseCalled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer restoreGnmiFuncs()

			// Fakes for redisHSetFunc
			var fakeHSetCalled bool
			var fakeHSetKey, fakeHSetFld, fakeHSetVal string
			redisHSetFunc = func(c *redis.Client, key string, fields ...interface{}) error {
				fakeHSetCalled = true
				fakeHSetKey = key
				if len(fields) > 0 {
					fakeHSetFld = fmt.Sprint(fields[0])
				}
				if len(fields) > 1 {
					fakeHSetVal = fmt.Sprint(fields[1])
				}
				return tt.hsetErr
			}

			// Fakes for getRedisDBClientFunc
			getRedisDBClientFunc = func(dbName string) (*redis.Client, error) {
				if tt.getDBClientErr != nil {
					return nil, tt.getDBClientErr
				}
				return new(redis.Client), nil
			}
			// Fakes for closeRedisClientFunc
			closeCalled := false
			closeRedisClientFunc = func(sc *redis.Client) error {
				closeCalled = true
				return nil
			}
			err := writeCredentialsMetadataToDB(tt.tbl, tt.key, tt.fld, tt.val)

			if tt.wantErr {
				if err == nil {
					t.Errorf("writeCredentialsMetadataToDB(%q, %q, %q, %q) got nil error, want error", tt.tbl, tt.key, tt.fld, tt.val)
				}
			} else {
				if err != nil {
					t.Errorf("writeCredentialsMetadataToDB(%q, %q, %q, %q) got error %v, want nil", tt.tbl, tt.key, tt.fld, tt.val, err)
				}
				if tt.getDBClientErr == nil {
					if !fakeHSetCalled {
						t.Errorf("redisHSetFunc was not called")
					} else {
						if got, want := fakeHSetKey, tt.wantHSetKey; got != want {
							t.Errorf("redisHSetFunc called with key %q, want %q", got, want)
						}
						if got, want := fakeHSetFld, tt.wantHSetFld; got != want {
							t.Errorf("redisHSetFunc called with fld %q, want %q", got, want)
						}
						if got, want := fakeHSetVal, tt.wantHSetVal; got != want {
							t.Errorf("redisHSetFunc called with val %q, want %q", got, want)
						}
					}
				}
			}
			if closeCalled != tt.wantCloseCalled {
				t.Errorf("closeRedisClientFunc called = %v, want %v", closeCalled, tt.wantCloseCalled)
			}
		})
	}
}

// --- Test getRedisDBClient ---
func TestGetRedisDBClientReal(t *testing.T) {
	tests := []struct {
		name           string
		dbName         string
		sdcfgNS        string
		sdcfgAddr      string
		sdcfgID        int
		newClientNil   bool
		pingErr        error
		wantErr        bool
		wantPingCalled bool
	}{
		{
			name:   "Success",
			dbName: "testDB", sdcfgNS: "ns1", sdcfgAddr: "127.0.0.1:6379", sdcfgID: 0,
			wantErr:        false,
			wantPingCalled: true,
		},
		{
			name:   "NewClientNil",
			dbName: "testDB", sdcfgNS: "ns1", sdcfgAddr: "127.0.0.1:6379", sdcfgID: 0,
			newClientNil:   true,
			wantErr:        true,
			wantPingCalled: false,
		},
		{
			name:   "PingError",
			dbName: "testDB", sdcfgNS: "ns1", sdcfgAddr: "127.0.0.1:6379", sdcfgID: 0,
			pingErr:        errors.New("ping failed"),
			wantErr:        true,
			wantPingCalled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer restoreGnmiFuncs()

			// Fake sdcfg functions
			getDbDefaultNamespaceFunc = func() (string, error) { return tt.sdcfgNS, nil }
			getDbTcpAddrFunc = func(string, string) (string, error) { return tt.sdcfgAddr, nil }
			getDbIdFunc = func(string, string) (int, error) { return tt.sdcfgID, nil }

			// Fake newClientFunc to return a dummy *redis.Client.
			newClientFunc = func(dbName string) *redis.Client {
				if tt.newClientNil {
					return nil
				}
				return new(redis.Client)
			}
			// Fakes for redisPingFunc
			fakePingCalled := false
			redisPingFunc = func(c *redis.Client) error {
				fakePingCalled = true
				return tt.pingErr
			}

			_, err := getRedisDBClient(tt.dbName)

			if tt.wantErr {
				if err == nil {
					t.Errorf("getRedisDBClient(%q) got nil error, want error", tt.dbName)
				}
			} else {
				if err != nil {
					t.Errorf("getRedisDBClient(%q) got error %v, want nil", tt.dbName, err)
				}
			}

			if fakePingCalled != tt.wantPingCalled {
				t.Errorf("redisPingFunc called = %v, want %v", fakePingCalled, tt.wantPingCalled)
			}
		})
	}
}

// --- Test getKey ---
func TestGetKey(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  string
	}{
		{"Empty", []string{}, ""},
		{"Single", []string{"one"}, "one"},
		{"Multiple", []string{"one", "two", "three"}, "one|two|three"},
		{"WithEmpty", []string{"one", "", "three"}, "one||three"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getKey(tt.input); got != tt.want {
				t.Errorf("getKey(%v) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// --- Test copyFile ---
func TestCopyFile(t *testing.T) {
	tests := []struct {
		name           string
		srcContent     string
		srcFile        string
		dstFile        string
		makeSrc        bool
		makeSrcDir     bool
		wantErr        bool
		wantDstContent string
		wantDstMode    os.FileMode
	}{
		{
			name:           "Success",
			srcContent:     "test content",
			srcFile:        "src.txt",
			dstFile:        "dst.txt",
			makeSrc:        true,
			wantErr:        false,
			wantDstContent: "test content",
			wantDstMode:    0600,
		},
		{
			name:    "SrcNotExist",
			srcFile: "nonexistent.txt",
			dstFile: "dst.txt",
			makeSrc: false,
			wantErr: true,
		},
		{
			name:       "SrcIsNotRegularFile",
			srcFile:    "srcDir",
			dstFile:    "dst.txt",
			makeSrc:    true,
			makeSrcDir: true,
			wantErr:    true,
		},
		{
			name:       "DstParentNotExist",
			srcContent: "test",
			srcFile:    "src.txt",
			dstFile:    "subdir/dst.txt",
			makeSrc:    true,
			wantErr:    true, // Expect error because "subdir" doesn't exist
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subTmpDir := t.TempDir() // Create a unique temp directory for each subtest

			srcPath := filepath.Join(subTmpDir, tt.srcFile)
			dstPath := filepath.Join(subTmpDir, tt.dstFile)

			if tt.makeSrc {
				if tt.makeSrcDir {
					if err := os.MkdirAll(srcPath, 0755); err != nil {
						t.Fatalf("Failed to create src directory: %v", err)
					}
				} else {
					if err := os.MkdirAll(filepath.Dir(srcPath), 0755); err != nil {
						t.Fatalf("Failed to create parent dir for src: %v", err)
					}
					if err := os.WriteFile(srcPath, []byte(tt.srcContent), 0644); err != nil {
						t.Fatalf("Failed to create src file: %v", err)
					}
				}
			}

			// This block ensures the destination parent exists ONLY for cases where no error is expected.
			if !tt.wantErr {
				dstParent := filepath.Dir(dstPath)
				if dstParent != subTmpDir {
					if err := os.MkdirAll(dstParent, 0755); err != nil {
						t.Fatalf("Failed to create parent dir for dst: %v", err)
					}
				}
			}

			err := copyFile(srcPath, dstPath)

			if tt.wantErr {
				if err == nil {
					t.Errorf("copyFile(%q, %q) got nil error, want error", tt.srcFile, tt.dstFile)
				}
				// Verify destination file was not created. os.Stat should return an error.
				if _, statErr := os.Stat(dstPath); statErr == nil {
					t.Errorf("copyFile(%q, %q) destination file exists, but should not on error", tt.srcFile, tt.dstFile)
				}
			} else {
				if err != nil {
					t.Errorf("copyFile(%q, %q) got error %v, want nil", tt.srcFile, tt.dstFile, err)
				}
				gotContent, err := os.ReadFile(dstPath)
				if err != nil {
					t.Fatalf("Failed to read destination file: %v", err)
				}
				if diff := cmp.Diff(tt.wantDstContent, string(gotContent)); diff != "" {
					t.Errorf("copyFile(%q, %q) content mismatch (-want +got):\n%s", tt.srcFile, tt.dstFile, diff)
				}
				stat, err := os.Stat(dstPath)
				if err != nil {
					t.Fatalf("Failed to stat destination file: %v", err)
				}
				if got, want := stat.Mode().Perm(), os.FileMode(0600); got != want {
					t.Errorf("copyFile(%q, %q) got permissions %v, want %v", tt.srcFile, tt.dstFile, got, want)
				}
			}
		})
	}
}

// --- Test fileCheck ---
func TestFileCheck(t *testing.T) {
	tests := []struct {
		name     string
		filePath string
		setup    func(string, string) error // Function to set up the file/dir, takes tmpDir and filePath
		wantErr  bool
	}{
		{
			name:     "RegularFile",
			filePath: "regular.txt",
			setup:    func(tmp, p string) error { return os.WriteFile(p, []byte("test"), 0644) },
			wantErr:  false,
		},
		{
			name:     "NonExistentFile",
			filePath: "nonexistent.txt",
			setup:    func(tmp, p string) error { return nil },
			wantErr:  true,
		},
		{
			name:     "IsDirectory",
			filePath: "a_directory",
			setup:    func(tmp, p string) error { return os.Mkdir(p, 0755) },
			wantErr:  true,
		},
		{
			name:     "SymlinkToFile",
			filePath: "link.txt",
			setup: func(tmp, p string) error {
				targetPath := filepath.Join(tmp, "target.txt")
				if err := os.WriteFile(targetPath, []byte("data"), 0644); err != nil {
					return err
				}
				return os.Symlink(targetPath, p)
			},
			wantErr: true, // os.Lstat will show this is a symlink, not a regular file.
		},
		{
			name:     "SymlinkToDir",
			filePath: "link_to_dir",
			setup: func(tmp, p string) error {
				targetPath := filepath.Join(tmp, "target_dir")
				if err := os.Mkdir(targetPath, 0755); err != nil {
					return err
				}
				return os.Symlink(targetPath, p)
			},
			wantErr: true, // os.Lstat will show this is a symlink, not a regular file.
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subTmpDir := t.TempDir()
			fullPath := filepath.Join(subTmpDir, tt.filePath)

			if tt.setup != nil {
				if err := tt.setup(subTmpDir, fullPath); err != nil {
					t.Fatalf("Failed to setup test case: %v", err)
				}
			}

			err := fileCheck(fullPath)

			if tt.wantErr {
				if err == nil {
					t.Errorf("fileCheck(%q) got nil error, want error", tt.filePath)
				}
			} else {
				if err != nil {
					t.Errorf("fileCheck(%q) got error %v, want nil", tt.filePath, err)
				}
			}
		})
	}
}
