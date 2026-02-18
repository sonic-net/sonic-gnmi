package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/golang/glog"
)

// Client wraps the Redis client for SONiC CONFIG_DB operations.
type Client struct {
	rdb *redis.Client
}

// Config contains Redis connection configuration.
type Config struct {
	Host    string
	Port    int
	DB      int
	Timeout time.Duration
}

// DefaultConfig returns a default Redis configuration for SONiC CONFIG_DB.
func DefaultConfig() *Config {
	return &Config{
		Host:    "127.0.0.1",
		Port:    6379,
		DB:      4, // CONFIG_DB in SONiC
		Timeout: 5 * time.Second,
	}
}

// NewClient creates a new Redis client with the given configuration.
func NewClient(config *Config) (*Client, error) {
	if config == nil {
		config = DefaultConfig()
	}

	addr := fmt.Sprintf("%s:%d", config.Host, config.Port)
	glog.V(2).Infof("Connecting to Redis at %s (database %d)", addr, config.DB)

	rdb := redis.NewClient(&redis.Options{
		Addr:        addr,
		DB:          config.DB,
		DialTimeout: config.Timeout,
	})

	// Test the connection
	ctx, cancel := context.WithTimeout(context.Background(), config.Timeout)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		glog.Errorf("Failed to connect to Redis at %s: %v", addr, err)
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	glog.V(2).Info("Successfully connected to Redis")
	return &Client{rdb: rdb}, nil
}

// Close closes the Redis connection.
func (c *Client) Close() error {
	if c != nil && c.rdb != nil {
		return c.rdb.Close()
	}
	return nil
}

// HGet retrieves a field from a hash.
func (c *Client) HGet(ctx context.Context, key, field string) (string, error) {
	result, err := c.rdb.HGet(ctx, key, field).Result()
	if err == redis.Nil {
		return "", fmt.Errorf("field '%s' not found in hash '%s'", field, key)
	}
	if err != nil {
		return "", fmt.Errorf("failed to get field '%s' from hash '%s': %w", field, key, err)
	}
	return result, nil
}

// Ping tests the connection to Redis.
func (c *Client) Ping(ctx context.Context) error {
	return c.rdb.Ping(ctx).Err()
}
