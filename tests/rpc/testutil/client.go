//go:build e2e

package testutil

import (
	"crypto/tls"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// NewClientConn creates no RPCs or application data. The testcase selects its stub.
func NewClientConn(t *testing.T, address string, tlsConfig *tls.Config) *grpc.ClientConn {
	t.Helper()
	connection, err := grpc.NewClient(address,
		grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)), grpc.WithDisableRetry())
	check(t, err)
	t.Cleanup(func() { check(t, connection.Close()) })
	return connection
}
