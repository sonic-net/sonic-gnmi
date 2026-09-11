package gnmi

// Tests for gNMI ONCE subscription stream lifecycle.
//
// gNMI spec §3.5.1.5.1 requires that the server close the RPC immediately
// after sending sync_response for a ONCE subscription.  The tests here
// directly verify that contract by reading from the raw gRPC stream and
// asserting that io.EOF arrives promptly after the sync_response — without
// relying on a client-side timeout or test cleanup to cancel the context.

import (
	"context"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// onceStreamResult holds a single message received from a ONCE subscription stream.
type onceStreamResult struct {
	resp *gnmipb.SubscribeResponse
	err  error
}

// serverPort extracts the numeric port from a Server whose address is returned
// by s.Address() (e.g. "localhost:54321").
func serverPort(t *testing.T, s *Server) int {
	t.Helper()
	_, portStr, err := net.SplitHostPort(s.Address())
	if err != nil {
		t.Fatalf("cannot parse server address %q: %v", s.Address(), err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("cannot convert port %q to int: %v", portStr, err)
	}
	return port
}

// runOnceSubscribe opens a raw gRPC Subscribe stream, sends req, and fans all
// received messages into the returned channel.  The channel is closed when the
// stream ends (io.EOF or error).
func runOnceSubscribe(t *testing.T, port int, req *gnmipb.SubscribeRequest) <-chan onceStreamResult {
	t.Helper()
	ch := make(chan onceStreamResult, 64)

	conn := createClient(t, port)
	gnmiClient := gnmipb.NewGNMIClient(conn)

	stream, err := gnmiClient.Subscribe(context.Background())
	if err != nil {
		t.Fatalf("Subscribe() stream open failed: %v", err)
	}
	if err := stream.Send(req); err != nil {
		t.Fatalf("Subscribe() Send failed: %v", err)
	}

	go func() {
		defer close(ch)
		defer conn.Close()
		for {
			resp, err := stream.Recv()
			ch <- onceStreamResult{resp, err}
			if err != nil {
				return
			}
		}
	}()

	return ch
}

// collectOnce drains ch until io.EOF or a non-nil error, with an overall
// deadline.  Returns (gotSync, closeErr):
//   - gotSync  – true if a sync_response was received before the stream ended
//   - closeErr – nil if the stream ended with io.EOF (status OK),
//     or the gRPC status error if the server returned a non-OK status,
//     or context.DeadlineExceeded if the deadline expired before the stream closed.
func collectOnce(ch <-chan onceStreamResult, timeout time.Duration) (gotSync bool, closeErr error) {
	deadline := time.After(timeout)
	for {
		select {
		case r, ok := <-ch:
			if !ok {
				// channel closed without an explicit error message
				return gotSync, nil
			}
			if r.err == io.EOF {
				return gotSync, nil // clean close, status OK
			}
			if r.err != nil {
				return gotSync, r.err // server returned a non-OK status
			}
			if r.resp.GetSyncResponse() {
				gotSync = true
			}
		case <-deadline:
			return gotSync, context.DeadlineExceeded
		}
	}
}

// onceReq builds a minimal ONCE SubscribeRequest for the given path.
func onceReq(path string) *gnmipb.SubscribeRequest {
	return &gnmipb.SubscribeRequest{
		Request: &gnmipb.SubscribeRequest_Subscribe{
			Subscribe: &gnmipb.SubscriptionList{
				Mode:   gnmipb.SubscriptionList_ONCE,
				Prefix: strToPath("openconfig:/"),
				// Empty ACL set — server will send sync_response with no updates.
				Subscription: []*gnmipb.Subscription{
					{Path: strToPath(path)},
				},
			},
		},
	}
}

// TestOnceSubscribe_StreamClosedAfterSync verifies that the server closes the
// gRPC stream promptly after sending sync_response for a ONCE subscription.
//
// Before the fix the send() loop in client_subscribe.go would block forever on
// queue.Get() once OnceRun() exited — leaving the stream open indefinitely.
// This test would time out in that scenario and fail within ~5 seconds instead
// of waiting for an external timeout.
func TestOnceSubscribe_StreamClosedAfterSync(t *testing.T) {
	s := createServer(t, 0)
	go runServer(t, s)
	defer s.s.Stop()

	ch := runOnceSubscribe(t, serverPort(t, s), onceReq("/openconfig-acl:acl/acl-sets"))

	// Allow 5 seconds total.  The server should close the stream well within 1s.
	gotSync, err := collectOnce(ch, 5*time.Second)

	switch {
	case err == context.DeadlineExceeded && !gotSync:
		t.Fatal("Timed out waiting for sync_response; server may not have sent it")
	case err == context.DeadlineExceeded:
		t.Fatal("sync_response received but stream was not closed within 5s; " +
			"server likely blocked in send() loop after OnceRun() exited")
	case err != nil:
		st, _ := status.FromError(err)
		t.Fatalf("Stream closed with unexpected status: code=%v msg=%v", st.Code(), st.Message())
	case !gotSync:
		t.Fatal("Stream closed without sending sync_response")
	}
	// err == nil and gotSync == true → PASS
}

// TestOnceSubscribe_StatusOK verifies that a successful ONCE subscription
// causes the server to close the RPC with status OK (not InvalidArgument).
//
// Before the fix, Run() always returned grpc.Errorf(codes.InvalidArgument, ...)
// unconditionally — even when send() returned nil on a clean ONCE completion.
// The client would see an InvalidArgument status instead of a clean close.
func TestOnceSubscribe_StatusOK(t *testing.T) {
	s := createServer(t, 0)
	go runServer(t, s)
	defer s.s.Stop()

	ch := runOnceSubscribe(t, serverPort(t, s), onceReq("/openconfig-acl:acl/acl-sets"))
	_, closeErr := collectOnce(ch, 5*time.Second)

	if closeErr == context.DeadlineExceeded {
		t.Fatal("Stream did not close within 5s")
	}
	if closeErr != nil {
		st, _ := status.FromError(closeErr)
		if st.Code() == codes.InvalidArgument {
			t.Fatalf("Server returned InvalidArgument instead of closing cleanly; "+
				"Run() likely still unconditionally wrapping nil err: %v", closeErr)
		}
		t.Fatalf("Stream closed with unexpected error: %v", closeErr)
	}
}

// setOnServer performs a gNMI Set against the given server port.
// It is a port-aware alternative to the package-level doSet() which hardcodes
// port 8081 and is only safe to call when that specific server is running.
func setOnServer(t *testing.T, port int, data ...interface{}) {
	t.Helper()
	req := &gnmipb.SetRequest{}
	for _, v := range data {
		switch v := v.(type) {
		case *gnmipb.Path:
			req.Delete = append(req.Delete, v)
		case *gnmipb.Update:
			req.Update = append(req.Update, v)
		default:
			t.Fatalf("Unsupported set value: %T %v", v, v)
		}
	}
	client := gnmipb.NewGNMIClient(createClient(t, port))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := client.Set(ctx, req); err != nil {
		t.Fatalf("Set failed: %v", err)
	}
}

// TestOnceSubscribe_DataDeliveredBeforeClose verifies that when data exists all
// update notifications are delivered to the client before the stream is closed.
//
// This guards against a regression where send() might exit prematurely (e.g.,
// on the first sync_response even if more data is enqueued after it).
func TestOnceSubscribe_DataDeliveredBeforeClose(t *testing.T) {
	s := createServer(t, 0)
	go runServer(t, s)
	defer s.s.Stop()

	prepareDbTranslib(t)

	port := serverPort(t, s)
	aclPath := "/openconfig-acl:acl/acl-sets"
	acl1Create := newPbUpdate("/openconfig-acl:acl/acl-sets/acl-set",
		`{"acl-set": [{"name": "ONCE_TEST", "type": "ACL_IPV4",
		  "config": {"name": "ONCE_TEST", "type": "ACL_IPV4"}}]}`)
	defer setOnServer(t, port, strToPath(aclPath))
	setOnServer(t, port, acl1Create)

	req := &gnmipb.SubscribeRequest{
		Request: &gnmipb.SubscribeRequest_Subscribe{
			Subscribe: &gnmipb.SubscriptionList{
				Mode:   gnmipb.SubscriptionList_ONCE,
				Prefix: strToPath("openconfig:/"),
				Subscription: []*gnmipb.Subscription{
					{Path: strToPath(aclPath + "/acl-set")},
				},
			},
		},
	}

	ch := runOnceSubscribe(t, port, req)

	var updates int
	gotSync := false
	deadline := time.After(5 * time.Second)

loop:
	for {
		select {
		case r, ok := <-ch:
			if !ok || r.err == io.EOF {
				break loop
			}
			if r.err != nil {
				t.Fatalf("Recv error: %v", r.err)
			}
			if r.resp.GetSyncResponse() {
				gotSync = true
			} else if r.resp.GetUpdate() != nil {
				updates++
			}
		case <-deadline:
			t.Fatal("Stream did not close within 5s")
		}
	}

	if !gotSync {
		t.Error("sync_response not received")
	}
	if updates == 0 {
		t.Error("No update notifications received; expected ACL data before sync_response")
	}
}
