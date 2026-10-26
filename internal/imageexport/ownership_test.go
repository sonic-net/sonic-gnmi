package imageexport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func setTestIdentity(t *testing.T, store Store, id int) {
	t.Helper()
	data := fmt.Sprintf("sonic-image:x:%d:%d:Image worker:/nonexistent:/usr/sbin/nologin\n", id, id)
	if err := os.WriteFile(store.physical("/etc/passwd"), []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestOwnershipUpgradeRollbackAndInterruptedMigration(t *testing.T) {
	store := testStore(t)
	path := ExportRoot + "/release/sonic.bin"
	upload, err := store.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := upload.File.Write([]byte("image survives identity changes")); err != nil {
		t.Fatal(err)
	}
	if err := upload.Commit(); err != nil {
		t.Fatal(err)
	}
	base, err := openDirectory(store.physical(StoreRoot), unix.AT_FDCWD)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(base)
	// Simulate power loss after one inode was changed with a durable journal.
	record := ownershipRecord{Version: 1, UID: 65534, GID: 65534, NextUID: 65533, NextGID: 65533}
	if err := writeOwnership(base, record); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(store.physical(path), 65533, 65533); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{65533, 65534, 65532, 65534} {
		setTestIdentity(t, store, id)
		source, err := store.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		data, readErr := io.ReadAll(source)
		source.Close()
		if readErr != nil || string(data) != "image survives identity changes" {
			t.Fatalf("migration lost image: data=%q error=%v", data, readErr)
		}
		for _, logical := range []string{ExportRoot, ExportRoot + "/release", path, StoreRoot + "/staging"} {
			var info unix.Stat_t
			if err := unix.Stat(store.physical(logical), &info); err != nil {
				t.Fatal(err)
			}
			mode := uint32(0700)
			if logical == path {
				mode = 0600
			}
			if info.Uid != uint32(id) || info.Gid != uint32(id) || info.Mode&07777 != mode {
				t.Fatalf("incorrect migrated ownership/mode: %s %+v", logical, info)
			}
		}
		current, err := readOwnership(base)
		if err != nil || current.UID != uint32(id) || current.NextUID != 0 {
			t.Fatalf("incomplete journal: record=%+v error=%v", current, err)
		}
	}
}

func TestOwnershipMigrationRejectsUnsafeTreesBeforeChangingAnyInode(t *testing.T) {
	for _, kind := range []string{"root-owner", "unknown-owner", "public-mode", "symlink", "hardlink", "fifo", "hidden", "staging-directory"} {
		t.Run(kind, func(t *testing.T) {
			store := testStore(t)
			safe := store.physical(ExportRoot + "/safe.bin")
			if err := os.WriteFile(safe, []byte("safe"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chown(safe, 65534, 65534); err != nil {
				t.Fatal(err)
			}
			unsafe := store.physical(ExportRoot + "/unsafe.bin")
			switch kind {
			case "symlink":
				if err := os.Symlink(safe, unsafe); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(safe, unsafe); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := unix.Mkfifo(unsafe, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chown(unsafe, 65534, 65534); err != nil {
					t.Fatal(err)
				}
			case "staging-directory":
				unsafe = store.physical(StoreRoot + "/staging/0123456789abcdef0123456789abcdef")
				if err := os.Mkdir(unsafe, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Chown(unsafe, 65534, 65534); err != nil {
					t.Fatal(err)
				}
			default:
				if kind == "hidden" {
					unsafe = store.physical(ExportRoot + "/.hidden")
				}
				if err := os.WriteFile(unsafe, []byte("unsafe"), 0600); err != nil {
					t.Fatal(err)
				}
				id := 65534
				if kind == "root-owner" {
					id = 0
				}
				if kind == "unknown-owner" {
					id = 65532
				}
				if err := os.Chown(unsafe, id, id); err != nil {
					t.Fatal(err)
				}
				if kind == "public-mode" {
					if err := os.Chmod(unsafe, 0644); err != nil {
						t.Fatal(err)
					}
				}
			}
			setTestIdentity(t, store, 65533)
			if err := store.Prepare(); err == nil {
				t.Fatal("unsafe tree was migrated")
			}
			var info unix.Stat_t
			if err := unix.Stat(safe, &info); err != nil || info.Uid != 65534 {
				t.Fatalf("validation partially changed valid artifact: %+v error=%v", info, err)
			}
		})
	}
}

func TestOwnershipJournalFailsClosed(t *testing.T) {
	for _, data := range []string{"{}", "{", `{"version":1,"uid":0,"gid":0,"next_uid":0,"next_gid":0}`,
		`{"version":1,"uid":65534,"gid":65534,"next_uid":65533,"next_gid":0}`} {
		t.Run(data, func(t *testing.T) {
			store := testStore(t)
			if err := os.WriteFile(store.physical(StoreRoot+"/ownership.json"), []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			if err := store.Prepare(); err == nil {
				t.Fatal("malformed ownership journal accepted")
			}
		})
	}
}

func TestStatBatchesThousandEntriesInOneWorker(t *testing.T) {
	store := testStore(t)
	for index := 0; index < 1000; index++ {
		name := store.physical(fmt.Sprintf("%s/image-%04d.bin", ExportRoot, index))
		if err := os.WriteFile(name, []byte("image"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(name, 65534, 65534); err != nil {
			t.Fatal(err)
		}
	}
	previous := newWorkerCommand
	starts := 0
	newWorkerCommand = func(ctx context.Context, args ...string) *exec.Cmd {
		starts++
		return previous(ctx, args...)
	}
	t.Cleanup(func() { newWorkerCommand = previous })
	metadata, err := store.Stat(context.Background(), ExportRoot)
	if err != nil || len(metadata) != 1000 || starts != 1 {
		t.Fatalf("batching: entries=%d workers=%d error=%v", len(metadata), starts, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Stat(ctx, ExportRoot); !errors.Is(err, context.Canceled) || starts != 1 {
		t.Fatalf("canceled Stat spawned a worker: workers=%d error=%v", starts, err)
	}
}

func TestRequestCancellationKillsRunningWorker(t *testing.T) {
	store := testStore(t)
	previous := newWorkerCommand
	var command *exec.Cmd
	newWorkerCommand = func(ctx context.Context, _ ...string) *exec.Cmd {
		command = exec.CommandContext(ctx, "sleep", "60")
		return command
	}
	t.Cleanup(func() { newWorkerCommand = previous })
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := store.Stat(ctx, ExportRoot); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline not propagated: %v", err)
	}
	if time.Since(started) > 3*time.Second || command == nil || command.ProcessState == nil ||
		!command.ProcessState.Exited() && command.ProcessState.String() != "signal: killed" {
		t.Fatalf("worker not reaped promptly: command=%v", command)
	}
}

func TestOwnershipLockWaitHonorsRPCDeadline(t *testing.T) {
	store := testStore(t)
	lock, err := os.OpenFile(store.physical(StoreRoot+"/ownership.lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := store.Stat(ctx, ExportRoot); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("journal lock ignored cancellation: %v", err)
	}
}

func TestStatFiltersUnavailableEntriesAndRemoveUsesWorker(t *testing.T) {
	store := testStore(t)
	path := ExportRoot + "/release/image.bin"
	upload, err := store.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := upload.File.Write([]byte("image")); err != nil {
		t.Fatal(err)
	}
	if err := upload.Commit(); err != nil {
		t.Fatal(err)
	}
	metadata, err := store.Stat(context.Background(), path)
	if err != nil || len(metadata) != 1 || metadata[0].Size() != 5 || metadata[0].Mode() != 0600 {
		t.Fatalf("incorrect private metadata: %+v error=%v", metadata, err)
	}
	if metadata[0].Name() != "image.bin" || metadata[0].ModTime().UnixNano() <= 0 || metadata[0].Sys() != nil {
		t.Fatalf("invalid portable FileInfo metadata: %+v", metadata[0])
	}
	if err := os.WriteFile(store.physical(ExportRoot+"/root-private.bin"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(store.physical(StoreRoot), store.physical(ExportRoot+"/alias")); err != nil {
		t.Fatal(err)
	}
	metadata, err = store.Stat(context.Background(), ExportRoot)
	if err != nil || len(metadata) != 1 || metadata[0].Path != ExportRoot+"/release" || !metadata[0].IsDir() {
		t.Fatalf("directory disclosed unavailable entries: %+v error=%v", metadata, err)
	}
	if err := store.Remove(context.Background(), ExportRoot+"/alias/ownership.lock"); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("Remove followed a private-directory alias: %v", err)
	}
	for _, target := range []string{path, ExportRoot + "/release"} {
		if err := store.Remove(context.Background(), target); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Remove(context.Background(), path); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("missing removal not denied: %v", err)
	}
	if err := store.Remove(context.Background(), ExportRoot); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("fixed store directory was removable: %v", err)
	}
}
