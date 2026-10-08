package gnmi

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc"
)

type failingSubscribeStream struct {
	grpc.ServerStream
	recvCalled chan struct{}
}

func (s *failingSubscribeStream) Context() context.Context {
	return context.Background()
}

func (s *failingSubscribeStream) Recv() (*gnmipb.SubscribeRequest, error) {
	close(s.recvCalled)
	return nil, errors.New("test receive error")
}

func (s *failingSubscribeStream) Send(*gnmipb.SubscribeResponse) error {
	return nil
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
