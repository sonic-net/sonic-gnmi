package dpuproxy

import (
	"context"
	"errors"
	"testing"
)

// mockRedisClient implements RedisClient for testing
type mockRedisClient struct {
	data map[string]map[string]string
	err  error
}

func (m *mockRedisClient) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	if m.err != nil {
		return nil, m.err
	}
	if data, ok := m.data[key]; ok {
		return data, nil
	}
	return map[string]string{}, nil // Empty map indicates key not found
}

func TestDPUResolver_GetDPUInfo_Success(t *testing.T) {
	stateMock := &mockRedisClient{
		data: map[string]map[string]string{
			"CHASSIS_MIDPLANE_TABLE|DPU0": {
				"ip_address": "169.254.200.1",
				"access":     "True",
			},
		},
	}
	configMock := &mockRedisClient{
		data: map[string]map[string]string{
			"DPU|dpu0": {
				"gnmi_port": "8080",
			},
		},
	}

	resolver := NewDPUResolver(stateMock, configMock)
	info, err := resolver.GetDPUInfo(context.Background(), "0")

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	if info == nil {
		t.Fatal("Expected DPUInfo, got nil")
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
}

func TestDPUResolver_GetDPUInfo_Unreachable(t *testing.T) {
	stateMock := &mockRedisClient{
		data: map[string]map[string]string{
			"CHASSIS_MIDPLANE_TABLE|DPU2": {
				"ip_address": "169.254.200.3",
				"access":     "False",
			},
		},
	}
	configMock := &mockRedisClient{
		data: map[string]map[string]string{},
	}

	resolver := NewDPUResolver(stateMock, configMock)
	info, err := resolver.GetDPUInfo(context.Background(), "2")

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	if info.Reachable {
		t.Error("Expected Reachable=false for DPU2")
	}

	if info.IPAddress != "169.254.200.3" {
		t.Errorf("Expected IPAddress='169.254.200.3', got: %s", info.IPAddress)
	}
}

func TestDPUResolver_GetDPUInfo_MissingAccessField(t *testing.T) {
	stateMock := &mockRedisClient{
		data: map[string]map[string]string{
			"CHASSIS_MIDPLANE_TABLE|DPU1": {
				"ip_address": "169.254.200.2",
				// No "access" field
			},
		},
	}
	configMock := &mockRedisClient{
		data: map[string]map[string]string{},
	}

	resolver := NewDPUResolver(stateMock, configMock)
	info, err := resolver.GetDPUInfo(context.Background(), "1")

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	if info.Reachable {
		t.Error("Expected Reachable=false when access field is missing")
	}
}

func TestDPUResolver_GetDPUInfo_NotFound(t *testing.T) {
	stateMock := &mockRedisClient{
		data: map[string]map[string]string{},
	}
	configMock := &mockRedisClient{
		data: map[string]map[string]string{},
	}

	resolver := NewDPUResolver(stateMock, configMock)
	info, err := resolver.GetDPUInfo(context.Background(), "99")

	if err == nil {
		t.Error("Expected error for non-existent DPU")
	}

	if info != nil {
		t.Errorf("Expected nil DPUInfo, got: %+v", info)
	}

	expectedErr := "DPU99 not found in StateDB"
	if err.Error() != expectedErr {
		t.Errorf("Expected error '%s', got: %v", expectedErr, err)
	}
}

func TestDPUResolver_GetDPUInfo_MissingIPAddress(t *testing.T) {
	stateMock := &mockRedisClient{
		data: map[string]map[string]string{
			"CHASSIS_MIDPLANE_TABLE|DPU3": {
				"access": "True",
				// Missing ip_address field
			},
		},
	}
	configMock := &mockRedisClient{
		data: map[string]map[string]string{},
	}

	resolver := NewDPUResolver(stateMock, configMock)
	info, err := resolver.GetDPUInfo(context.Background(), "3")

	if err == nil {
		t.Error("Expected error for missing ip_address field")
	}

	if info != nil {
		t.Errorf("Expected nil DPUInfo, got: %+v", info)
	}
}

func TestDPUResolver_GetDPUInfo_EmptyIPAddress(t *testing.T) {
	stateMock := &mockRedisClient{
		data: map[string]map[string]string{
			"CHASSIS_MIDPLANE_TABLE|DPU4": {
				"ip_address": "",
				"access":     "True",
			},
		},
	}
	configMock := &mockRedisClient{
		data: map[string]map[string]string{},
	}

	resolver := NewDPUResolver(stateMock, configMock)
	info, err := resolver.GetDPUInfo(context.Background(), "4")

	if err == nil {
		t.Error("Expected error for empty ip_address")
	}

	if info != nil {
		t.Errorf("Expected nil DPUInfo, got: %+v", info)
	}
}

func TestDPUResolver_GetDPUInfo_RedisError(t *testing.T) {
	stateMock := &mockRedisClient{
		err: errors.New("connection refused"),
	}
	configMock := &mockRedisClient{
		data: map[string]map[string]string{},
	}

	resolver := NewDPUResolver(stateMock, configMock)
	info, err := resolver.GetDPUInfo(context.Background(), "0")

	if err == nil {
		t.Error("Expected error from Redis")
	}

	if info != nil {
		t.Errorf("Expected nil DPUInfo on error, got: %+v", info)
	}
}

func TestDPUResolver_GetDPUInfo_MultipleDPUs(t *testing.T) {
	stateMock := &mockRedisClient{
		data: map[string]map[string]string{
			"CHASSIS_MIDPLANE_TABLE|DPU0": {
				"ip_address": "169.254.200.1",
				"access":     "True",
			},
			"CHASSIS_MIDPLANE_TABLE|DPU1": {
				"ip_address": "169.254.200.2",
				"access":     "True",
			},
			"CHASSIS_MIDPLANE_TABLE|DPU2": {
				"ip_address": "169.254.200.3",
				"access":     "False",
			},
		},
	}
	configMock := &mockRedisClient{
		data: map[string]map[string]string{},
	}

	resolver := NewDPUResolver(stateMock, configMock)

	// Test DPU0
	info0, err := resolver.GetDPUInfo(context.Background(), "0")
	if err != nil || info0.IPAddress != "169.254.200.1" || !info0.Reachable {
		t.Errorf("DPU0 failed: %v, %+v", err, info0)
	}

	// Test DPU1
	info1, err := resolver.GetDPUInfo(context.Background(), "1")
	if err != nil || info1.IPAddress != "169.254.200.2" || !info1.Reachable {
		t.Errorf("DPU1 failed: %v, %+v", err, info1)
	}

	// Test DPU2 (unreachable)
	info2, err := resolver.GetDPUInfo(context.Background(), "2")
	if err != nil || info2.IPAddress != "169.254.200.3" || info2.Reachable {
		t.Errorf("DPU2 failed: %v, %+v", err, info2)
	}
}

func TestNewDPUResolver(t *testing.T) {
	stateMock := &mockRedisClient{}
	configMock := &mockRedisClient{}
	resolver := NewDPUResolver(stateMock, configMock)

	if resolver == nil {
		t.Error("Expected non-nil resolver")
	}

	if resolver.stateClient == nil {
		t.Error("Expected resolver to have stateClient set")
	}

	if resolver.configClient == nil {
		t.Error("Expected resolver to have configClient set")
	}
}
