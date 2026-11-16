package localredis

import "testing"

func TestOptionsUsesUnixSocket(t *testing.T) {
	opts, err := Options("/var/run/redis0/redis.sock", 6)
	if err != nil {
		t.Fatalf("Options returned error: %v", err)
	}
	if opts.Network != "unix" {
		t.Fatalf("Network = %q, want unix", opts.Network)
	}
	if opts.Addr != "/var/run/redis0/redis.sock" {
		t.Fatalf("Addr = %q, want /var/run/redis0/redis.sock", opts.Addr)
	}
	if opts.DB != 6 {
		t.Fatalf("DB = %d, want 6", opts.DB)
	}
}

func TestOptionsRejectsEmptySocket(t *testing.T) {
	if _, err := Options("", 6); err == nil {
		t.Fatal("Options succeeded with an empty socket path")
	}
}
