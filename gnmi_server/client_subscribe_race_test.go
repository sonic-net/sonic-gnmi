package gnmi

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Workiva/go-datastructures/queue"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
	"google.golang.org/grpc"
)

type failingSubscribeStream struct {
	grpc.ServerStream
	recvCalled chan struct{}
	sendErr    error
}

func (s *failingSubscribeStream) Context() context.Context {
	return context.Background()
}

func (s *failingSubscribeStream) Recv() (*gnmipb.SubscribeRequest, error) {
	close(s.recvCalled)
	return nil, errors.New("test receive error")
}

func (s *failingSubscribeStream) Send(*gnmipb.SubscribeResponse) error {
	return s.sendErr
}

type testSubscribeQueueItem struct{}

func (testSubscribeQueueItem) Compare(queue.Item) int {
	return 0
}

func TestClientCountersAreSafeDuringClose(t *testing.T) {
	const iterations = 100

	for i := 0; i < iterations; i++ {
		client := NewClient(&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: i})
		recvCalled := make(chan struct{})

		// Hold Close before it reads the counters, then let Run update them.
		// The sleep leaves those operations unsynchronized so the race detector
		// can catch a non-atomic counter access.
		client.mu.Lock()

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			client.Close()
		}()
		go func() {
			defer wg.Done()
			_ = client.Run(&failingSubscribeStream{recvCalled: recvCalled}, &Config{})
		}()

		<-recvCalled
		time.Sleep(time.Millisecond)
		client.mu.Unlock()
		wg.Wait()
	}
}

func TestClientSendCountersOnSendError(t *testing.T) {
	client := NewClient(&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err := client.q.Put(testSubscribeQueueItem{}); err != nil {
		t.Fatalf("failed to enqueue test item: %v", err)
	}

	stream := &failingSubscribeStream{sendErr: errors.New("test send error")}
	err := client.send(stream, &sdc.DbClient{})
	if err == nil {
		t.Fatal("send() returned nil, want send error")
	}
	if got := client.sendMsg.Load(); got != 1 {
		t.Errorf("sendMsg = %d, want 1", got)
	}
	if got := client.errors.Load(); got != 2 {
		t.Errorf("errors = %d, want 2 (unknown queue item and failed send)", got)
	}
}
