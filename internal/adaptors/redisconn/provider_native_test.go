//go:build !pure

package redisconn_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sonic-net/sonic-gnmi/internal/redisopts"
)

func TestNewPreservesSuppliedTLSWithDefaultDialer(t *testing.T) {
	server := httptest.NewTLSServer(nil)
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	config := &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	options := redisopts.New(redis.Options{Addr: server.Listener.Addr().String(), TLSConfig: config})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	connection, err := options.Dialer(ctx, "tcp", options.Addr)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	secured, ok := connection.(*tls.Conn)
	if !ok || !secured.ConnectionState().HandshakeComplete {
		t.Fatal("supplied TLS settings did not produce a verified TLS connection")
	}
	if options.TLSConfig != config || config.ServerName != "" || config.RootCAs != roots {
		t.Fatal("the caller's TLS settings changed")
	}
}
