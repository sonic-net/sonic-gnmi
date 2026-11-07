//go:build pure

package redisconn

import (
	"context"
	"errors"
	"net"

	"github.com/redis/go-redis/v9"
)

var errNativeUnavailable = errors.New("native Redis connections are unavailable in pure builds")

// Apply rejects connections because pure builds have no native authentication
// provider. It never invokes another dialer or opens a socket.
func Apply(options *redis.Options) {
	options.Dialer = func(ctx context.Context, network, address string) (net.Conn, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, errNativeUnavailable
	}
}
