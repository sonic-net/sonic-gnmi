package imageexport

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"golang.org/x/sys/unix"
)

func holdOwnershipLock(t *testing.T, store Store) func() {
	t.Helper()
	file, err := os.OpenFile(store.physical(StoreRoot+"/ownership.lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { file.Close() })
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	return func() {
		if err := unix.Flock(int(file.Fd()), unix.LOCK_UN); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUploadCreateCancellationStopsOwnershipLockWait(t *testing.T) {
	store := testStore(t)
	unlock := holdOwnershipLock(t, store)
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if upload, err := store.CreateContext(ctx, ExportRoot+"/never-created.bin"); upload != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Create lost deadline: upload=%v error=%v", upload, err)
	}
	entries, err := os.ReadDir(store.physical(StoreRoot + "/staging"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("canceled creation left staging: entries=%v error=%v", entries, err)
	}
}

func TestUploadCommitCancellationPreservesImageAndCleanupIgnoresCallerCancellation(t *testing.T) {
	store := testStore(t)
	path := ExportRoot + "/image.bin"
	original, err := store.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := original.File.Write([]byte("previous image")); err != nil {
		t.Fatal(err)
	}
	if err := original.Commit(); err != nil {
		t.Fatal(err)
	}
	replacement, err := store.CreateContext(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := replacement.File.Write([]byte("canceled replacement")); err != nil {
		t.Fatal(err)
	}
	unlock := holdOwnershipLock(t, store)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := replacement.CommitContext(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Commit lost deadline: %v", err)
	}
	// Cleanup must work while the caller is canceled and the ownership lock is held.
	if err := replacement.Abort(); err != nil {
		t.Fatal(err)
	}
	unlock()
	source, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(source)
	source.Close()
	if err != nil || string(data) != "previous image" {
		t.Fatalf("canceled publication replaced prior image: data=%q error=%v", data, err)
	}
	entries, err := os.ReadDir(store.physical(StoreRoot + "/staging"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("canceled publication left staging: entries=%v error=%v", entries, err)
	}
}

func TestUploadCommitRejectsAlreadyCanceledContext(t *testing.T) {
	store := testStore(t)
	upload, err := store.Create(ExportRoot + "/never-published.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := upload.Abort(); err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := upload.CommitContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("already canceled commit proceeded: %v", err)
	}
	if upload.closed || upload.committed {
		t.Fatal("already canceled commit modified upload state")
	}
}

func TestCreateCancellationAfterDescriptorReceiptReclaimsStaging(t *testing.T) {
	store := testStore(t)
	for attempt := 0; attempt < 3; attempt++ {
		ctx, cancel := context.WithCancel(context.Background())
		receivedFD := -1
		patches := gomonkey.NewPatches()
		patches.ApplyFunc(unix.Recvmsg, func(fd int, payload, ancillary []byte, flags int) (int, int, int, unix.Sockaddr, error) {
			patches.Reset()
			n, ancillaryN, resultFlags, address, err := unix.Recvmsg(fd, payload, ancillary, flags)
			messages, parseErr := unix.ParseSocketControlMessage(ancillary[:ancillaryN])
			if parseErr == nil {
				for _, message := range messages {
					rights, rightsErr := unix.ParseUnixRights(&message)
					if rightsErr == nil && len(rights) == 1 {
						receivedFD = rights[0]
					}
				}
			}
			cancel()
			return n, ancillaryN, resultFlags, address, err
		})
		upload, err := store.CreateContext(ctx, ExportRoot+"/canceled.bin")
		patches.Reset()
		cancel()
		if upload != nil || !errors.Is(err, context.Canceled) || receivedFD < 0 {
			t.Fatalf("cancel after receiving descriptor: upload=%v receivedFD=%d error=%v", upload, receivedFD, err)
		}
		if _, err := unix.FcntlInt(uintptr(receivedFD), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
			t.Fatalf("received descriptor leaked after cancellation: %v", err)
		}
		entries, err := os.ReadDir(store.physical(StoreRoot + "/staging"))
		if err != nil || len(entries) != 0 {
			t.Fatalf("canceled creation left staging: entries=%v error=%v", entries, err)
		}
	}
}

func TestCreateFailedResponseReclaimsParentSelectedToken(t *testing.T) {
	for _, failure := range []string{"missing", "mismatch"} {
		t.Run(failure, func(t *testing.T) {
			store := testStore(t)
			command := newWorkerCommand
			newWorkerCommand = func(ctx context.Context, args ...string) *exec.Cmd {
				cmd := command(ctx, args...)
				cmd.Env = append(cmd.Env, "SONIC_IMAGE_TEST_CREATE_RESPONSE="+failure)
				return cmd
			}
			if upload, err := store.Create(ExportRoot + "/failed.bin"); upload != nil || err == nil {
				t.Fatalf("failed worker response accepted: upload=%v error=%v", upload, err)
			}
			entries, err := os.ReadDir(store.physical(StoreRoot + "/staging"))
			if err != nil || len(entries) != 0 {
				t.Fatalf("failed response left staging: entries=%v error=%v", entries, err)
			}
		})
	}
}
