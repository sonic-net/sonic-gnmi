//go:build !pure

package redisconn

import (
	"github.com/redis/go-redis/v9"
	"github.com/sonic-net/sonic-swss-common/goext/redisconn"
)

// Apply installs the connection dialer for the selected build provider.
func Apply(options *redis.Options) {
	options.Dialer = redisconn.WrapDialer(options.Dialer, options.DialTimeout, options.TLSConfig)
}
