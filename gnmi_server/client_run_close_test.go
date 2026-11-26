package gnmi

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"

	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The real client receives EOF while another subscription closes this peer.
// Unused stream methods remain outside this initial-receive error path.
type closingSubscribeStream struct {
	gnmipb.GNMI_SubscribeServer
	receiving chan struct{}
	closePeer chan struct{}
}

func (s *closingSubscribeStream) Context() context.Context { return context.Background() }

func (s *closingSubscribeStream) Recv() (*gnmipb.SubscribeRequest, error) {
	close(s.receiving)
	<-s.closePeer
	return nil, io.EOF
}

func TestClientRunConcurrentClose(t *testing.T) {
	for i := 0; i < 32; i++ {
		client := NewClient(&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8081})
		stream := &closingSubscribeStream{receiving: make(chan struct{}), closePeer: make(chan struct{})}
		var closing sync.WaitGroup
		closing.Add(1)
		go func() {
			defer closing.Done()
			<-stream.receiving
			close(stream.closePeer)
			for j := 0; j < 64; j++ {
				client.Close()
			}
		}()
		err := client.Run(stream, &Config{})
		closing.Wait()
		if status.Code(err) != codes.Aborted {
			t.Fatalf("initial stream EOF: got %v, want Aborted", err)
		}
	}
}
