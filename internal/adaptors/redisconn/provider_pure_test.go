//go:build pure

package redisconn

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/redis/go-redis/v9"
)

func TestPureDialerRejectsConnectionsBeforeCallingExistingDialer(t *testing.T) {
	for _, network := range []string{"unix", "tcp"} {
		t.Run(network, func(t *testing.T) {
			called := false
			options := &redis.Options{
				Dialer: func(context.Context, string, string) (net.Conn, error) {
					called = true
					return nil, nil
				},
			}
			Apply(options)
			connection, err := options.Dialer(context.Background(), network, "unused")
			if connection != nil || !errors.Is(err, errNativeUnavailable) || called {
				t.Fatalf("pure dialer did not reject the connection: conn=%v err=%v called=%v", connection, err, called)
			}
		})
	}
}
