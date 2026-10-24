// Package redisopts provides shared configuration for Redis client options.
package redisopts

import (
	"os"
	"strconv"

	"github.com/Azure/sonic-mgmt-common/redis_auth"
	log "github.com/golang/glog"
	"github.com/redis/go-redis/v9"
)

// PoolSizeEnvVar is the environment variable used to override the Redis
// client connection pool size.
const PoolSizeEnvVar = "GNMI_REDIS_POOL_SIZE"

// New returns Redis options with all shared configuration applied.
func New(opts redis.Options) *redis.Options {
	ApplyPoolSize(&opts)
	opts.Dialer = redis_auth.WrapDialer(opts.Dialer, opts.DialTimeout, opts.TLSConfig)
	return &opts
}

// ApplyPoolSize sets opts.PoolSize from PoolSizeEnvVar when it contains a
// positive integer. Otherwise, opts is unchanged so go-redis uses its default.
func ApplyPoolSize(opts *redis.Options) {
	if opts == nil {
		return
	}

	value, ok := os.LookupEnv(PoolSizeEnvVar)
	if !ok || value == "" {
		return
	}

	poolSize, err := strconv.Atoi(value)
	if err != nil {
		log.Warningf("Ignoring invalid %s=%q (must be a positive integer): %v",
			PoolSizeEnvVar, value, err)
		return
	}
	if poolSize <= 0 {
		log.Warningf("Ignoring non-positive %s=%q (must be a positive integer)",
			PoolSizeEnvVar, value)
		return
	}

	opts.PoolSize = poolSize
}
