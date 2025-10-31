package dpuproxy

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis"
)

func TestDPUResolver_Integration_RealRedis(t *testing.T) {
	// Start in-memory Redis server
	s := miniredis.RunT(t)
	defer s.Close()

	// Select database 6 (STATE_DB)
	s.Select(6)

	// Populate test data mimicking SONiC's CHASSIS_MIDPLANE_TABLE
	s.HSet("CHASSIS_MIDPLANE_TABLE|DPU0", "ip_address", "169.254.200.1")
	s.HSet("CHASSIS_MIDPLANE_TABLE|DPU0", "access", "True")

	s.HSet("CHASSIS_MIDPLANE_TABLE|DPU1", "ip_address", "169.254.200.2")
	s.HSet("CHASSIS_MIDPLANE_TABLE|DPU1", "access", "True")

	s.HSet("CHASSIS_MIDPLANE_TABLE|DPU2", "ip_address", "169.254.200.3")
	s.HSet("CHASSIS_MIDPLANE_TABLE|DPU2", "access", "False")

	// Create real Redis client pointing to miniredis
	client := redis.NewClient(&redis.Options{
		Addr: s.Addr(),
		DB:   6,
	})
	defer client.Close()

	// Create adapter and resolver
	adapter := NewGoRedisAdapter(client)
	resolver := NewDPUResolver(adapter)

	// Test DPU0 (reachable)
	t.Run("DPU0_Reachable", func(t *testing.T) {
		info, err := resolver.GetDPUInfo(context.Background(), "0")
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		if info.Index != "0" {
			t.Errorf("Expected Index='0', got: %s", info.Index)
		}

		if info.IPAddress != "169.254.200.1" {
			t.Errorf("Expected IPAddress='169.254.200.1', got: %s", info.IPAddress)
		}

		if !info.Reachable {
			t.Error("Expected Reachable=true")
		}
	})

	// Test DPU1 (reachable)
	t.Run("DPU1_Reachable", func(t *testing.T) {
		info, err := resolver.GetDPUInfo(context.Background(), "1")
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		if info.IPAddress != "169.254.200.2" {
			t.Errorf("Expected IPAddress='169.254.200.2', got: %s", info.IPAddress)
		}

		if !info.Reachable {
			t.Error("Expected Reachable=true")
		}
	})

	// Test DPU2 (unreachable)
	t.Run("DPU2_Unreachable", func(t *testing.T) {
		info, err := resolver.GetDPUInfo(context.Background(), "2")
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		if info.IPAddress != "169.254.200.3" {
			t.Errorf("Expected IPAddress='169.254.200.3', got: %s", info.IPAddress)
		}

		if info.Reachable {
			t.Error("Expected Reachable=false for DPU2")
		}
	})

	// Test non-existent DPU
	t.Run("DPU99_NotFound", func(t *testing.T) {
		info, err := resolver.GetDPUInfo(context.Background(), "99")
		if err == nil {
			t.Error("Expected error for non-existent DPU")
		}

		if info != nil {
			t.Errorf("Expected nil DPUInfo, got: %+v", info)
		}
	})
}

func TestDPUResolver_Integration_PartialData(t *testing.T) {
	s := miniredis.RunT(t)
	defer s.Close()

	s.Select(6)

	// DPU with missing access field
	s.HSet("CHASSIS_MIDPLANE_TABLE|DPU3", "ip_address", "169.254.200.4")

	client := redis.NewClient(&redis.Options{Addr: s.Addr(), DB: 6})
	defer client.Close()

	adapter := NewGoRedisAdapter(client)
	resolver := NewDPUResolver(adapter)

	info, err := resolver.GetDPUInfo(context.Background(), "3")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if info.Reachable {
		t.Error("Expected Reachable=false when access field is missing")
	}

	if info.IPAddress != "169.254.200.4" {
		t.Errorf("Expected IPAddress='169.254.200.4', got: %s", info.IPAddress)
	}
}

func TestDPUResolver_Integration_EmptyDatabase(t *testing.T) {
	s := miniredis.RunT(t)
	defer s.Close()

	s.Select(6)
	// Don't add any data

	client := redis.NewClient(&redis.Options{Addr: s.Addr(), DB: 6})
	defer client.Close()

	adapter := NewGoRedisAdapter(client)
	resolver := NewDPUResolver(adapter)

	info, err := resolver.GetDPUInfo(context.Background(), "0")
	if err == nil {
		t.Error("Expected error for empty database")
	}

	if info != nil {
		t.Errorf("Expected nil DPUInfo, got: %+v", info)
	}
}

func TestGoRedisAdapter_HGetAll(t *testing.T) {
	s := miniredis.RunT(t)
	defer s.Close()

	s.HSet("test_key", "field1", "value1")
	s.HSet("test_key", "field2", "value2")

	client := redis.NewClient(&redis.Options{Addr: s.Addr()})
	defer client.Close()

	adapter := NewGoRedisAdapter(client)
	result, err := adapter.HGetAll(context.Background(), "test_key")

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(result) != 2 {
		t.Errorf("Expected 2 fields, got: %d", len(result))
	}

	if result["field1"] != "value1" {
		t.Errorf("Expected field1='value1', got: %s", result["field1"])
	}

	if result["field2"] != "value2" {
		t.Errorf("Expected field2='value2', got: %s", result["field2"])
	}
}

func TestNewRedisClient(t *testing.T) {
	// Test that NewRedisClient creates a client with the correct configuration
	// Note: We don't actually connect since that would require a real Unix socket
	socketPath := "/var/run/redis/redis.sock"
	db := 6

	client := NewRedisClient(socketPath, db)
	defer client.Close()

	if client == nil {
		t.Error("Expected non-nil Redis client")
	}

	// Verify the client options are set correctly
	opts := client.Options()
	if opts.Network != "unix" {
		t.Errorf("Expected Network='unix', got: %s", opts.Network)
	}

	if opts.Addr != socketPath {
		t.Errorf("Expected Addr=%s, got: %s", socketPath, opts.Addr)
	}

	if opts.DB != db {
		t.Errorf("Expected DB=%d, got: %d", db, opts.DB)
	}
}
