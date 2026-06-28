// redis_pool.go provides optional, environment-driven tuning of the redis
// client connection pool used by gNMI's redis clients.

package dbconfig

import (
	"os"
	"strconv"

	log "github.com/golang/glog"
	"github.com/redis/go-redis/v9"
)

// RedisPoolSizeEnvVar is the environment variable used to override the redis
// client connection pool size. When it is not set, callers must leave the
// go-redis default pool size unchanged.
const RedisPoolSizeEnvVar = "GNMI_REDIS_POOL_SIZE"

// ApplyRedisPoolSize sets opts.PoolSize from the GNMI_REDIS_POOL_SIZE
// environment variable.
//
// If the variable is unset, empty, or not a positive integer, opts is left
// unchanged so go-redis keeps its default pool size (10 * runtime.GOMAXPROCS),
// which scales with the number of logical CPUs. Setting the variable lets
// operators cap per-client connections (and their buffers) on high-CPU
// systems to reduce memory consumption.
func ApplyRedisPoolSize(opts *redis.Options) {
	if opts == nil {
		return
	}
	v, ok := os.LookupEnv(RedisPoolSizeEnvVar)
	if !ok || v == "" {
		// Not set: do not modify the default pool size.
		return
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		log.Warningf("Ignoring invalid %s=%q (must be a positive integer): %v",
			RedisPoolSizeEnvVar, v, err)
		return
	}
	if n <= 0 {
		log.Warningf("Ignoring non-positive %s=%q (must be a positive integer)",
			RedisPoolSizeEnvVar, v)
		return
	}
	opts.PoolSize = n
}
