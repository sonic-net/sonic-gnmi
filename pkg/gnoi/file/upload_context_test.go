package file

import (
	"context"
	"crypto/md5"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/openconfig/gnoi/common"
	gnoi_file_pb "github.com/openconfig/gnoi/file"
	"github.com/sonic-net/sonic-gnmi/internal/imageexport"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestManagedPutAndTransferDeadlinesStopOwnershipLockWait(t *testing.T) {
	store := realImageStore(t)
	path := publishLegacyTestImage(t, store)
	lock, err := os.OpenFile(filepath.Join(store.HostRoot, imageexport.StoreRoot, "ownership.lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"put", "transfer"} {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		if operation == "put" {
			stream := newMockPutStream()
			stream.ctx = ctx
			stream.addOpenRequest(path, 0)
			err = HandlePut(stream)
		} else {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Write([]byte("replacement"))
			}))
			_, err = HandleTransferToRemote(ctx, &gnoi_file_pb.TransferToRemoteRequest{
				LocalPath:      path,
				RemoteDownload: &common.RemoteDownload{Path: server.URL, Protocol: common.RemoteDownload_HTTP},
			})
			server.Close()
		}
		cancel()
		if status.Code(err) != codes.DeadlineExceeded {
			t.Fatalf("%s lost upload deadline: %v", operation, err)
		}
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	assertLegacyImageUnchanged(t, store, path)
	assertNoPrivateStaging(t, store)
}

type cancelBeforePublicationStream struct {
	*mockPutStream
	cancel context.CancelFunc
}

func (s *cancelBeforePublicationStream) Recv() (*gnoi_file_pb.PutRequest, error) {
	request, err := s.mockPutStream.Recv()
	if err == nil && request.GetHash() != nil {
		s.cancel()
	}
	return request, err
}

func TestManagedPutCancellationBeforeCommitCleansStaging(t *testing.T) {
	store := realImageStore(t)
	path := publishLegacyTestImage(t, store)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	content := []byte("must not replace prior image")
	digest := md5.Sum(content)
	stream := newMockPutStream()
	stream.ctx = ctx
	stream.addOpenRequest(path, 0)
	stream.addContentRequest(content)
	stream.addHashRequest(digest[:])
	if err := HandlePut(&cancelBeforePublicationStream{mockPutStream: stream, cancel: cancel}); status.Code(err) != codes.Canceled {
		t.Fatalf("canceled Put published: %v", err)
	}
	assertLegacyImageUnchanged(t, store, path)
	assertNoPrivateStaging(t, store)
}
