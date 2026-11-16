package localredis

import (
	"errors"

	"github.com/redis/go-redis/v9"
	"github.com/sonic-net/sonic-gnmi/internal/redisopts"
)

// Options returns Redis options for a database on the local Unix socket.
// The caller resolves the socket path and database ID from SONiC metadata.
func Options(socketPath string, dbID int) (*redis.Options, error) {
	if socketPath == "" {
		return nil, errors.New("empty Redis socket path")
	}

	return redisopts.New(redis.Options{
		Network:     "unix",
		Addr:        socketPath,
		DB:          dbID,
		DialTimeout: 0,
	}), nil
}
