package dpuproxy

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestExtractTargetMetadata_NoMetadata(t *testing.T) {
	ctx := context.Background()
	meta := ExtractTargetMetadata(ctx)

	if meta.HasMetadata {
		t.Error("Expected HasMetadata to be false when no metadata present")
	}

	if meta.TargetType != "" {
		t.Errorf("Expected empty TargetType, got: %s", meta.TargetType)
	}

	if meta.TargetIndex != "" {
		t.Errorf("Expected empty TargetIndex, got: %s", meta.TargetIndex)
	}

	if meta.IsDPUTarget() {
		t.Error("Expected IsDPUTarget to be false when no metadata present")
	}
}

func TestExtractTargetMetadata_OnlyTargetType(t *testing.T) {
	md := metadata.New(map[string]string{
		MetadataKeyTargetType: "dpu",
	})
	ctx := metadata.NewIncomingContext(context.Background(), md)
	meta := ExtractTargetMetadata(ctx)

	if !meta.HasMetadata {
		t.Error("Expected HasMetadata to be true")
	}

	if meta.TargetType != "dpu" {
		t.Errorf("Expected TargetType='dpu', got: %s", meta.TargetType)
	}

	if meta.TargetIndex != "" {
		t.Errorf("Expected empty TargetIndex, got: %s", meta.TargetIndex)
	}

	if !meta.IsDPUTarget() {
		t.Error("Expected IsDPUTarget to be true")
	}
}

func TestExtractTargetMetadata_OnlyTargetIndex(t *testing.T) {
	md := metadata.New(map[string]string{
		MetadataKeyTargetIndex: "0",
	})
	ctx := metadata.NewIncomingContext(context.Background(), md)
	meta := ExtractTargetMetadata(ctx)

	if !meta.HasMetadata {
		t.Error("Expected HasMetadata to be true")
	}

	if meta.TargetType != "" {
		t.Errorf("Expected empty TargetType, got: %s", meta.TargetType)
	}

	if meta.TargetIndex != "0" {
		t.Errorf("Expected TargetIndex='0', got: %s", meta.TargetIndex)
	}

	if meta.IsDPUTarget() {
		t.Error("Expected IsDPUTarget to be false when TargetType is not 'dpu'")
	}
}

func TestExtractTargetMetadata_BothFields(t *testing.T) {
	md := metadata.New(map[string]string{
		MetadataKeyTargetType:  "dpu",
		MetadataKeyTargetIndex: "3",
	})
	ctx := metadata.NewIncomingContext(context.Background(), md)
	meta := ExtractTargetMetadata(ctx)

	if !meta.HasMetadata {
		t.Error("Expected HasMetadata to be true")
	}

	if meta.TargetType != "dpu" {
		t.Errorf("Expected TargetType='dpu', got: %s", meta.TargetType)
	}

	if meta.TargetIndex != "3" {
		t.Errorf("Expected TargetIndex='3', got: %s", meta.TargetIndex)
	}

	if !meta.IsDPUTarget() {
		t.Error("Expected IsDPUTarget to be true")
	}
}

func TestExtractTargetMetadata_NonDPUTarget(t *testing.T) {
	md := metadata.New(map[string]string{
		MetadataKeyTargetType:  "npu",
		MetadataKeyTargetIndex: "0",
	})
	ctx := metadata.NewIncomingContext(context.Background(), md)
	meta := ExtractTargetMetadata(ctx)

	if !meta.HasMetadata {
		t.Error("Expected HasMetadata to be true")
	}

	if meta.TargetType != "npu" {
		t.Errorf("Expected TargetType='npu', got: %s", meta.TargetType)
	}

	if meta.IsDPUTarget() {
		t.Error("Expected IsDPUTarget to be false for non-dpu target type")
	}
}

func TestExtractTargetMetadata_MultipleValues(t *testing.T) {
	md := metadata.MD{
		MetadataKeyTargetType:  []string{"dpu", "npu"},
		MetadataKeyTargetIndex: []string{"0", "1", "2"},
	}
	ctx := metadata.NewIncomingContext(context.Background(), md)
	meta := ExtractTargetMetadata(ctx)

	if !meta.HasMetadata {
		t.Error("Expected HasMetadata to be true")
	}

	// Should take the first value
	if meta.TargetType != "dpu" {
		t.Errorf("Expected TargetType='dpu' (first value), got: %s", meta.TargetType)
	}

	if meta.TargetIndex != "0" {
		t.Errorf("Expected TargetIndex='0' (first value), got: %s", meta.TargetIndex)
	}
}

func TestDPUProxy_UnaryInterceptor_NoMetadata(t *testing.T) {
	proxy := NewDPUProxy(nil) // No resolver needed for this test
	interceptor := proxy.UnaryInterceptor()

	handlerCalled := false
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		handlerCalled = true
		return "response", nil
	}

	ctx := context.Background()
	resp, err := interceptor(ctx, "request", &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}, handler)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	if !handlerCalled {
		t.Error("Expected handler to be called")
	}

	if resp != "response" {
		t.Errorf("Expected 'response', got: %v", resp)
	}
}

func TestDPUProxy_UnaryInterceptor_WithDPUMetadata(t *testing.T) {
	proxy := NewDPUProxy(nil)
	interceptor := proxy.UnaryInterceptor()

	handlerCalled := false
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		handlerCalled = true
		return "response", nil
	}

	md := metadata.New(map[string]string{
		MetadataKeyTargetType:  "dpu",
		MetadataKeyTargetIndex: "0",
	})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	resp, err := interceptor(ctx, "request", &grpc.UnaryServerInfo{FullMethod: "/gnmi.gNMI/Get"}, handler)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	if !handlerCalled {
		t.Error("Expected handler to be called (pass-through in Phase 1)")
	}

	if resp != "response" {
		t.Errorf("Expected 'response', got: %v", resp)
	}
}

func TestDPUProxy_UnaryInterceptor_WithNonDPUMetadata(t *testing.T) {
	proxy := NewDPUProxy(nil)
	interceptor := proxy.UnaryInterceptor()

	handlerCalled := false
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		handlerCalled = true
		return "response", nil
	}

	md := metadata.New(map[string]string{
		MetadataKeyTargetType:  "npu",
		MetadataKeyTargetIndex: "0",
	})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	resp, err := interceptor(ctx, "request", &grpc.UnaryServerInfo{FullMethod: "/gnmi.gNMI/Get"}, handler)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	if !handlerCalled {
		t.Error("Expected handler to be called")
	}

	if resp != "response" {
		t.Errorf("Expected 'response', got: %v", resp)
	}
}

// mockServerStream is a minimal implementation of grpc.ServerStream for testing
type mockServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (m *mockServerStream) Context() context.Context {
	return m.ctx
}

func TestDPUProxy_StreamInterceptor_NoMetadata(t *testing.T) {
	proxy := NewDPUProxy(nil)
	interceptor := proxy.StreamInterceptor()

	handlerCalled := false
	handler := func(srv interface{}, ss grpc.ServerStream) error {
		handlerCalled = true
		return nil
	}

	ctx := context.Background()
	ss := &mockServerStream{ctx: ctx}

	err := interceptor(nil, ss, &grpc.StreamServerInfo{FullMethod: "/test.Service/Method"}, handler)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	if !handlerCalled {
		t.Error("Expected handler to be called")
	}
}

func TestDPUProxy_StreamInterceptor_WithDPUMetadata(t *testing.T) {
	proxy := NewDPUProxy(nil)
	interceptor := proxy.StreamInterceptor()

	handlerCalled := false
	handler := func(srv interface{}, ss grpc.ServerStream) error {
		handlerCalled = true
		return nil
	}

	md := metadata.New(map[string]string{
		MetadataKeyTargetType:  "dpu",
		MetadataKeyTargetIndex: "2",
	})
	ctx := metadata.NewIncomingContext(context.Background(), md)
	ss := &mockServerStream{ctx: ctx}

	err := interceptor(nil, ss, &grpc.StreamServerInfo{FullMethod: "/gnmi.gNMI/Subscribe"}, handler)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	if !handlerCalled {
		t.Error("Expected handler to be called (pass-through in Phase 1)")
	}
}

func TestNewDPUProxy(t *testing.T) {
	proxy := NewDPUProxy(nil)

	if proxy == nil {
		t.Error("Expected non-nil DPUProxy")
	}

	// Verify it implements the Interceptor interface by checking methods exist
	_ = proxy.UnaryInterceptor()
	_ = proxy.StreamInterceptor()
}
