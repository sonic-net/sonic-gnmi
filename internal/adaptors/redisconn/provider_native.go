//go:build !pure

package redisconn

import (
	"context"
	"github.com/redis/go-redis/v9"
	"github.com/sonic-net/sonic-swss-common/goext/redisconn"
	"net"
)

// Apply installs the connection dialer for the selected build provider.
func Apply(options *redis.Options) {
	if options.Network == "unix" && options.TLSConfig == nil {
		options.Dialer = func(ctx context.Context, _ string, address string) (net.Conn, error) {
			return redisconn.DialUnixContext(ctx, address, options.DialTimeout, true)
		}
	}
}
