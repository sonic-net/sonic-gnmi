package dpuproxy

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGetDPUConnection_NilProxy(t *testing.T) {
	old := defaultProxy
	defaultProxy = nil
	defer func() { defaultProxy = old }()

	conn, err := GetDPUConnection(context.Background(), "0")
	if err == nil {
		t.Fatal("expected error when defaultProxy is nil")
	}
	if conn != nil {
		t.Fatal("expected nil connection when defaultProxy is nil")
	}
	if err.Error() != "DPU proxy not initialized" {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestSetDefaultProxy_And_GetDPUConnection(t *testing.T) {
	old := defaultProxy
	defer func() { defaultProxy = old }()

	proxy := NewDPUProxy(nil)
	SetDefaultProxy(proxy)

	if defaultProxy != proxy {
		t.Fatal("SetDefaultProxy did not set the singleton")
	}

	// With nil resolver, GetDPUConnection should return "resolver not available"
	conn, err := GetDPUConnection(context.Background(), "0")
	if err == nil {
		t.Fatal("expected error with nil resolver")
	}
	if conn != nil {
		t.Fatal("expected nil connection with nil resolver")
	}
	if err.Error() != "resolver not available" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSetDefaultProxy_Nil(t *testing.T) {
	old := defaultProxy
	defer func() { defaultProxy = old }()

	SetDefaultProxy(nil)
	if defaultProxy != nil {
		t.Fatal("SetDefaultProxy(nil) should set singleton to nil")
	}
}

func TestDPUProxy_GetDPUConnection_NilResolver(t *testing.T) {
	proxy := NewDPUProxy(nil)
	conn, err := proxy.GetDPUConnection(context.Background(), "0")
	if err == nil {
		t.Fatal("expected error with nil resolver")
	}
	if conn != nil {
		t.Fatal("expected nil connection")
	}
}

func TestDPUProxy_GetDPUConnection_ResolverError(t *testing.T) {
	stateClient := &mockRedisClient{data: map[string]map[string]string{}}
	configClient := &mockRedisClient{data: map[string]map[string]string{}}
	resolver := NewDPUResolver(stateClient, configClient)
	proxy := NewDPUProxy(resolver)

	// DPU "99" doesn't exist in mock data, so GetDPUInfo will fail
	conn, err := proxy.GetDPUConnection(context.Background(), "99")
	if err == nil {
		t.Fatal("expected error for non-existent DPU")
	}
	if conn != nil {
		t.Fatal("expected nil connection")
	}
}

func TestDPUProxy_GetDPUConnection_Unreachable(t *testing.T) {
	stateClient := &mockRedisClient{
		data: map[string]map[string]string{
			"CHASSIS_MIDPLANE_TABLE|DPU0": {
				"ip_address": "169.254.200.1",
				"access":     "False",
			},
		},
	}
	configClient := &mockRedisClient{
		data: map[string]map[string]string{},
	}
	resolver := NewDPUResolver(stateClient, configClient)
	proxy := NewDPUProxy(resolver)

	conn, err := proxy.GetDPUConnection(context.Background(), "0")
	if err == nil {
		t.Fatal("expected error for unreachable DPU")
	}
	if conn != nil {
		t.Fatal("expected nil connection")
	}
	s, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got: %v", err)
	}
	if s.Code() != codes.Unavailable {
		t.Errorf("expected Unavailable, got: %v", s.Code())
	}
}
