// Package logging provides shared low-level primitives for structured JSON
// line emission, canonical gRPC status mapping, and peer type/address
// extraction.
//
// Allowed imports: standard library, google.golang.org/grpc/{codes,peer,status}.
// This package MUST NOT import log/syslog, sonic-gnmi domain types, or rate-limiting packages.
package logging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// LineSink is an injectable line-delivery function. WriteJSON calls it at most
// once per invocation. The returned error is propagated to the WriteJSON caller.
type LineSink func(string) error

// ErrMarshal is returned by WriteJSON when json.Marshal fails. It wraps the
// underlying marshal error.
var ErrMarshal = errors.New("logging: marshal failure")

// ErrSinkPanic is returned by WriteJSON when the LineSink panics.
var ErrSinkPanic = errors.New("logging: sink panic")

// GRPCCode returns the canonical gRPC codes.Code for err. A nil error returns
// codes.OK. A gRPC status error returns its embedded code. A context error
// (context.DeadlineExceeded or context.Canceled) is mapped via
// status.FromContextError.
func GRPCCode(err error) codes.Code {
	if err == nil {
		return codes.OK
	}
	if s, ok := status.FromError(err); ok {
		return s.Code()
	}
	return status.FromContextError(err).Code()
}

// PeerTypeAddr extracts the peer type and address from ctx. The peer type is
// one of "tcp", "unix", or "unknown". The address is the Addr.String() value
// whenever a non-nil peer address exists, including unknown network types.
// It is empty only when the context has no peer address.
func PeerTypeAddr(ctx context.Context) (peerType, peerAddress string) {
	p, ok := peer.FromContext(ctx)
	if !ok || p.Addr == nil {
		return "unknown", ""
	}
	network := p.Addr.Network()
	switch {
	case strings.HasPrefix(network, "tcp"):
		return "tcp", p.Addr.String()
	case strings.HasPrefix(network, "unix"):
		return "unix", p.Addr.String()
	default:
		return "unknown", p.Addr.String()
	}
}

// WriteJSON serializes v as JSON, prepends prefix+" ", and calls sink exactly
// once. It returns ErrMarshal (wrapping the json.Marshal cause) on marshal
// failure, ErrSinkPanic on a recovered sink panic, or the sink's error
// otherwise. sink MUST NOT be retained after WriteJSON returns.
func WriteJSON(prefix string, v any, sink LineSink) (retErr error) {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrMarshal, err)
	}
	line := prefix + " " + string(data)
	defer func() {
		if r := recover(); r != nil {
			retErr = ErrSinkPanic
		}
	}()
	return sink(line)
}
