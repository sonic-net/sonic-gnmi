package dbconfig

import (
	"os"
	"testing"

	"github.com/redis/go-redis/v9"
)

func TestApplyRedisPoolSize(t *testing.T) {
	tests := []struct {
		name     string
		set      bool
		value    string
		expected int // expected PoolSize after apply; 0 means left at default
	}{
		{name: "unset leaves default", set: false, expected: 0},
		{name: "empty leaves default", set: true, value: "", expected: 0},
		{name: "valid positive value applied", set: true, value: "5", expected: 5},
		{name: "zero leaves default", set: true, value: "0", expected: 0},
		{name: "negative leaves default", set: true, value: "-3", expected: 0},
		{name: "non-integer leaves default", set: true, value: "abc", expected: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv(RedisPoolSizeEnvVar, tt.value)
			} else {
				os.Unsetenv(RedisPoolSizeEnvVar)
			}

			opts := &redis.Options{}
			ApplyRedisPoolSize(opts)
			if opts.PoolSize != tt.expected {
				t.Errorf("PoolSize = %d, want %d", opts.PoolSize, tt.expected)
			}
		})
	}
}

func TestApplyRedisPoolSizeNilOptions(t *testing.T) {
	// Must not panic on nil options.
	ApplyRedisPoolSize(nil)
}
