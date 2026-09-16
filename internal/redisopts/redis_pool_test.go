package redisopts

import (
	"os"
	"testing"

	"github.com/redis/go-redis/v9"
)

func TestApplyPoolSize(t *testing.T) {
	tests := []struct {
		name     string
		set      bool
		value    string
		expected int
	}{
		{name: "unset leaves default", expected: 0},
		{name: "empty leaves default", set: true, value: "", expected: 0},
		{name: "valid positive value applied", set: true, value: "5", expected: 5},
		{name: "zero leaves default", set: true, value: "0", expected: 0},
		{name: "negative leaves default", set: true, value: "-3", expected: 0},
		{name: "non-integer leaves default", set: true, value: "abc", expected: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv(PoolSizeEnvVar, tt.value)
			} else {
				os.Unsetenv(PoolSizeEnvVar)
			}

			opts := &redis.Options{}
			ApplyPoolSize(opts)
			if opts.PoolSize != tt.expected {
				t.Errorf("PoolSize = %d, want %d", opts.PoolSize, tt.expected)
			}
		})
	}
}

func TestApplyPoolSizeNilOptions(t *testing.T) {
	ApplyPoolSize(nil)
}
