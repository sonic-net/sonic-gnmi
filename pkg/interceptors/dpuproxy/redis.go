package dpuproxy

import (
	"context"

	"github.com/redis/go-redis/v9"
)

// RedisClient defines the interface for Redis operations needed by DPU resolver.
// This interface allows for easy mocking in tests.
type RedisClient interface {
	// HGetAll returns all fields and values in a hash stored at key.
	HGetAll(ctx context.Context, key string) (map[string]string, error)
}

// GoRedisAdapter adapts the go-redis client to our RedisClient interface.
type GoRedisAdapter struct {
	client *redis.Client
}

// NewGoRedisAdapter creates a new adapter for the go-redis client.
func NewGoRedisAdapter(client *redis.Client) *GoRedisAdapter {
	return &GoRedisAdapter{client: client}
}

// HGetAll implements RedisClient interface.
func (a *GoRedisAdapter) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	return a.client.HGetAll(ctx, key).Result()
}

// NewRedisClient creates a new Redis client connected to SONiC's Redis instance.
// It connects via Unix socket to the specified database.
func NewRedisClient(socketPath string, db int) *redis.Client {
	return redis.NewClient(&redis.Options{
		Network:  "unix",
		Addr:     socketPath,
		Password: "", // SONiC Redis has no password
		DB:       db,
	})
}
