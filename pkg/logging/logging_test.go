package logging_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/sonic-net/sonic-gnmi/pkg/logging"
)

type testAddr struct {
	network string
	address string
}

func (a testAddr) Network() string { return a.network }
func (a testAddr) String() string  { return a.address }

// TestGRPCCode covers TEST-017(a): nil error, status error, and context errors.
func TestGRPCCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want codes.Code
	}{
		{name: "nil", err: nil, want: codes.OK},
		{name: "status NotFound", err: status.Error(codes.NotFound, ""), want: codes.NotFound},
		{name: "context.DeadlineExceeded", err: context.DeadlineExceeded, want: codes.DeadlineExceeded},
		{name: "context.Canceled", err: context.Canceled, want: codes.Canceled},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := logging.GRPCCode(tc.err)
			if got != tc.want {
				t.Fatalf("GRPCCode(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestPeerTypeAddr covers TEST-017(b): TCP, unix, unknown, and missing peers.
func TestPeerTypeAddr(t *testing.T) {
	tcpAddr := &net.TCPAddr{IP: net.ParseIP("10.0.0.1"), Port: 1234}
	unixAddr := &net.UnixAddr{Name: "/var/run/gnmi.sock", Net: "unix"}
	unknownAddr := testAddr{network: "quic", address: "10.0.0.99:9999"}

	tests := []struct {
		name         string
		ctx          context.Context
		wantPeerType string
		wantPeerAddr string
	}{
		{
			name:         "tcp peer",
			ctx:          peer.NewContext(context.Background(), &peer.Peer{Addr: tcpAddr}),
			wantPeerType: "tcp",
			wantPeerAddr: tcpAddr.String(),
		},
		{
			name:         "unix peer",
			ctx:          peer.NewContext(context.Background(), &peer.Peer{Addr: unixAddr}),
			wantPeerType: "unix",
			wantPeerAddr: unixAddr.String(),
		},
		{
			name:         "unknown network peer",
			ctx:          peer.NewContext(context.Background(), &peer.Peer{Addr: unknownAddr}),
			wantPeerType: "unknown",
			wantPeerAddr: unknownAddr.String(),
		},
		{
			name:         "no peer",
			ctx:          context.Background(),
			wantPeerType: "unknown",
			wantPeerAddr: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotType, gotAddr := logging.PeerTypeAddr(tc.ctx)
			if gotType != tc.wantPeerType || gotAddr != tc.wantPeerAddr {
				t.Fatalf("PeerTypeAddr() = (%q, %q), want (%q, %q)",
					gotType, gotAddr, tc.wantPeerType, tc.wantPeerAddr)
			}
		})
	}
}

// TestWriteJSON covers TEST-017(c): success, marshal error, sink panic, sink error.
func TestWriteJSON(t *testing.T) {
	t.Run("success round-trip", func(t *testing.T) {
		type payload struct {
			Key string `json:"key"`
		}
		var got string
		sink := func(line string) error {
			got = line
			return nil
		}
		err := logging.WriteJSON("PREFIX", payload{Key: "val"}, sink)
		if err != nil {
			t.Fatalf("WriteJSON() error = %v, want nil", err)
		}
		const wantPrefix = "PREFIX "
		if !strings.HasPrefix(got, wantPrefix) {
			t.Fatalf("sink received %q, want prefix %q", got, wantPrefix)
		}
		jsonPart := strings.TrimPrefix(got, wantPrefix)
		if jsonPart != `{"key":"val"}` {
			t.Fatalf("json part = %q, want %q", jsonPart, `{"key":"val"}`)
		}
	})

	t.Run("non-marshallable returns ErrMarshal", func(t *testing.T) {
		sinkCalled := false
		sink := func(string) error {
			sinkCalled = true
			return nil
		}
		// channels cannot be JSON-marshalled
		err := logging.WriteJSON("PREFIX", make(chan int), sink)
		if err == nil {
			t.Fatal("WriteJSON() error = nil, want ErrMarshal")
		}
		if !errors.Is(err, logging.ErrMarshal) {
			t.Fatalf("WriteJSON() error = %v, want errors.Is(err, ErrMarshal)", err)
		}
		if sinkCalled {
			t.Fatal("sink was called on marshal failure, want no call")
		}
	})

	t.Run("panicking sink returns ErrSinkPanic", func(t *testing.T) {
		sink := func(string) error { panic("sink exploded") }
		err := logging.WriteJSON("PREFIX", "ok", sink)
		if err == nil {
			t.Fatal("WriteJSON() error = nil, want ErrSinkPanic")
		}
		if !errors.Is(err, logging.ErrSinkPanic) {
			t.Fatalf("WriteJSON() error = %v, want errors.Is(err, ErrSinkPanic)", err)
		}
	})

	t.Run("sink error propagated unchanged", func(t *testing.T) {
		wantErr := fmt.Errorf("disk full")
		sink := func(string) error { return wantErr }
		err := logging.WriteJSON("PREFIX", "ok", sink)
		if !errors.Is(err, wantErr) {
			t.Fatalf("WriteJSON() error = %v, want %v", err, wantErr)
		}
	})

	t.Run("sink called exactly once on success", func(t *testing.T) {
		calls := 0
		sink := func(string) error {
			calls++
			return nil
		}
		_ = logging.WriteJSON("P", struct{}{}, sink)
		if calls != 1 {
			t.Fatalf("sink called %d times, want 1", calls)
		}
	})
}
