package gnmi

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestMonitorAuthenticationClosesClientOnFailure(t *testing.T) {
	stop := make(chan struct{})
	authErr := make(chan error, 1)
	done := make(chan struct{})
	var calls int32
	var closes int32
	authenticate := func() error {
		if atomic.AddInt32(&calls, 1) == 2 {
			return status.Error(codes.Unauthenticated, "credentials revoked")
		}
		return nil
	}

	go func() {
		defer close(done)
		monitorAuthentication(context.Background(), time.Millisecond, authenticate,
			func() { atomic.AddInt32(&closes, 1) }, stop, authErr)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("periodic authentication did not terminate after failure")
	}

	select {
	case err := <-authErr:
		if status.Code(err) != codes.Unauthenticated {
			t.Fatalf("got authentication status %v, want %v", status.Code(err), codes.Unauthenticated)
		}
	default:
		t.Fatal("periodic authentication failure was not reported")
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("authentication called %d times, want 2", got)
	}
	if got := atomic.LoadInt32(&closes); got != 1 {
		t.Fatalf("client closed %d times, want 1", got)
	}
}

func TestPollSignalDoesNotRaceClose(t *testing.T) {
	for i := 0; i < 1000; i++ {
		client := NewClient(nil)
		client.polled = make(chan struct{}, 1)
		client.pollStop = make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			client.signalPoll()
		}()
		go func() {
			defer wg.Done()
			client.Close()
		}()
		wg.Wait()
		if client.signalPoll() {
			t.Fatal("poll signal succeeded after client close")
		}
	}
}

func TestQueuedPollSignalsArePreserved(t *testing.T) {
	client := NewClient(nil)
	client.polled = make(chan struct{}, 1)
	client.pollStop = make(chan struct{})
	client.polled <- struct{}{}

	done := make(chan bool, 1)
	go func() {
		done <- client.signalPoll()
	}()

	<-client.polled
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("poll signal was rejected while client was open")
		}
	case <-time.After(time.Second):
		t.Fatal("second poll signal was not queued")
	}
	select {
	case <-client.polled:
	case <-time.After(time.Second):
		t.Fatal("second poll notification was dropped")
	}
	client.Close()
}

func TestBlockedPollSignalIsCancelledByClose(t *testing.T) {
	client := NewClient(nil)
	client.polled = make(chan struct{}, 1)
	client.pollStop = make(chan struct{})
	client.polled <- struct{}{}

	signalDone := make(chan bool, 1)
	go func() {
		signalDone <- client.signalPoll()
	}()
	time.Sleep(10 * time.Millisecond)

	closeDone := make(chan struct{})
	go func() {
		client.Close()
		close(closeDone)
	}()
	select {
	case ok := <-signalDone:
		if ok {
			t.Fatal("blocked poll signal succeeded while the client was closing")
		}
	case <-time.After(time.Second):
		t.Fatal("blocked poll signal was not cancelled by close")
	}
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("client close waited behind a blocked poll signal")
	}
}

func TestMonitorAuthenticationStopsCleanly(t *testing.T) {
	stop := make(chan struct{})
	authErr := make(chan error, 1)
	authCalled := make(chan struct{}, 1)
	done := make(chan struct{})
	var closes int32
	authenticate := func() error {
		select {
		case authCalled <- struct{}{}:
		default:
		}
		return nil
	}

	go func() {
		defer close(done)
		monitorAuthentication(context.Background(), time.Millisecond, authenticate,
			func() { atomic.AddInt32(&closes, 1) }, stop, authErr)
	}()

	select {
	case <-authCalled:
	case <-time.After(time.Second):
		t.Fatal("periodic authentication was not called")
	}
	close(stop)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("periodic authentication did not stop")
	}
	if got := atomic.LoadInt32(&closes); got != 0 {
		t.Fatalf("stopping periodic authentication closed client %d times", got)
	}
	select {
	case err := <-authErr:
		t.Fatalf("stopping periodic authentication reported error %v", err)
	default:
	}
}
