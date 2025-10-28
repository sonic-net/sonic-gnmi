package interceptors

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc"
)

// mockInterceptor is a test interceptor that records when it's called
type mockInterceptor struct {
	name          string
	calls         *[]string
	shouldError   bool
	shouldReplace bool
}

func (m *mockInterceptor) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		*m.calls = append(*m.calls, m.name)

		if m.shouldError {
			return nil, errors.New(m.name + " error")
		}

		if m.shouldReplace {
			return m.name + " response", nil
		}

		return handler(ctx, req)
	}
}

func (m *mockInterceptor) StreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		*m.calls = append(*m.calls, m.name)

		if m.shouldError {
			return errors.New(m.name + " error")
		}

		if m.shouldReplace {
			return nil
		}

		return handler(srv, ss)
	}
}

func TestChain_UnaryInterceptor_EmptyChain(t *testing.T) {
	chain := NewChain()
	interceptor := chain.UnaryInterceptor()

	handlerCalled := false
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		handlerCalled = true
		return "handler response", nil
	}

	resp, err := interceptor(context.Background(), "request", &grpc.UnaryServerInfo{}, handler)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	if !handlerCalled {
		t.Error("Expected handler to be called")
	}

	if resp != "handler response" {
		t.Errorf("Expected 'handler response', got: %v", resp)
	}
}

func TestChain_UnaryInterceptor_SingleInterceptor(t *testing.T) {
	calls := []string{}
	mock := &mockInterceptor{name: "interceptor1", calls: &calls}

	chain := NewChain(mock)
	interceptor := chain.UnaryInterceptor()

	handlerCalled := false
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		handlerCalled = true
		return "handler response", nil
	}

	resp, err := interceptor(context.Background(), "request", &grpc.UnaryServerInfo{}, handler)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	if !handlerCalled {
		t.Error("Expected handler to be called")
	}

	if len(calls) != 1 || calls[0] != "interceptor1" {
		t.Errorf("Expected interceptor1 to be called, got: %v", calls)
	}

	if resp != "handler response" {
		t.Errorf("Expected 'handler response', got: %v", resp)
	}
}

func TestChain_UnaryInterceptor_MultipleInterceptors(t *testing.T) {
	calls := []string{}
	mock1 := &mockInterceptor{name: "interceptor1", calls: &calls}
	mock2 := &mockInterceptor{name: "interceptor2", calls: &calls}
	mock3 := &mockInterceptor{name: "interceptor3", calls: &calls}

	chain := NewChain(mock1, mock2, mock3)
	interceptor := chain.UnaryInterceptor()

	handlerCalled := false
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		handlerCalled = true
		return "handler response", nil
	}

	resp, err := interceptor(context.Background(), "request", &grpc.UnaryServerInfo{}, handler)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	if !handlerCalled {
		t.Error("Expected handler to be called")
	}

	// Interceptors should be called in order
	expectedCalls := []string{"interceptor1", "interceptor2", "interceptor3"}
	if len(calls) != len(expectedCalls) {
		t.Errorf("Expected %d calls, got %d: %v", len(expectedCalls), len(calls), calls)
	}

	for i, expected := range expectedCalls {
		if i >= len(calls) || calls[i] != expected {
			t.Errorf("Expected call %d to be %s, got: %v", i, expected, calls)
			break
		}
	}

	if resp != "handler response" {
		t.Errorf("Expected 'handler response', got: %v", resp)
	}
}

func TestChain_UnaryInterceptor_InterceptorError(t *testing.T) {
	calls := []string{}
	mock1 := &mockInterceptor{name: "interceptor1", calls: &calls}
	mock2 := &mockInterceptor{name: "interceptor2", calls: &calls, shouldError: true}
	mock3 := &mockInterceptor{name: "interceptor3", calls: &calls}

	chain := NewChain(mock1, mock2, mock3)
	interceptor := chain.UnaryInterceptor()

	handlerCalled := false
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		handlerCalled = true
		return "handler response", nil
	}

	resp, err := interceptor(context.Background(), "request", &grpc.UnaryServerInfo{}, handler)

	if err == nil || err.Error() != "interceptor2 error" {
		t.Errorf("Expected 'interceptor2 error', got: %v", err)
	}

	if handlerCalled {
		t.Error("Expected handler not to be called when interceptor returns error")
	}

	// interceptor1 and interceptor2 should be called, but not interceptor3
	expectedCalls := []string{"interceptor1", "interceptor2"}
	if len(calls) != len(expectedCalls) {
		t.Errorf("Expected %d calls, got %d: %v", len(expectedCalls), len(calls), calls)
	}

	if resp != nil {
		t.Errorf("Expected nil response on error, got: %v", resp)
	}
}

func TestChain_UnaryInterceptor_InterceptorShortCircuit(t *testing.T) {
	calls := []string{}
	mock1 := &mockInterceptor{name: "interceptor1", calls: &calls}
	mock2 := &mockInterceptor{name: "interceptor2", calls: &calls, shouldReplace: true}
	mock3 := &mockInterceptor{name: "interceptor3", calls: &calls}

	chain := NewChain(mock1, mock2, mock3)
	interceptor := chain.UnaryInterceptor()

	handlerCalled := false
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		handlerCalled = true
		return "handler response", nil
	}

	resp, err := interceptor(context.Background(), "request", &grpc.UnaryServerInfo{}, handler)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	if handlerCalled {
		t.Error("Expected handler not to be called when interceptor short-circuits")
	}

	// All interceptors should be called
	expectedCalls := []string{"interceptor1", "interceptor2"}
	if len(calls) != len(expectedCalls) {
		t.Errorf("Expected %d calls, got %d: %v", len(expectedCalls), len(calls), calls)
	}

	if resp != "interceptor2 response" {
		t.Errorf("Expected 'interceptor2 response', got: %v", resp)
	}
}

func TestChain_StreamInterceptor_EmptyChain(t *testing.T) {
	chain := NewChain()
	interceptor := chain.StreamInterceptor()

	handlerCalled := false
	handler := func(srv interface{}, ss grpc.ServerStream) error {
		handlerCalled = true
		return nil
	}

	err := interceptor(nil, nil, &grpc.StreamServerInfo{}, handler)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	if !handlerCalled {
		t.Error("Expected handler to be called")
	}
}

func TestChain_StreamInterceptor_MultipleInterceptors(t *testing.T) {
	calls := []string{}
	mock1 := &mockInterceptor{name: "interceptor1", calls: &calls}
	mock2 := &mockInterceptor{name: "interceptor2", calls: &calls}
	mock3 := &mockInterceptor{name: "interceptor3", calls: &calls}

	chain := NewChain(mock1, mock2, mock3)
	interceptor := chain.StreamInterceptor()

	handlerCalled := false
	handler := func(srv interface{}, ss grpc.ServerStream) error {
		handlerCalled = true
		return nil
	}

	err := interceptor(nil, nil, &grpc.StreamServerInfo{}, handler)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	if !handlerCalled {
		t.Error("Expected handler to be called")
	}

	// Interceptors should be called in order
	expectedCalls := []string{"interceptor1", "interceptor2", "interceptor3"}
	if len(calls) != len(expectedCalls) {
		t.Errorf("Expected %d calls, got %d: %v", len(expectedCalls), len(calls), calls)
	}

	for i, expected := range expectedCalls {
		if i >= len(calls) || calls[i] != expected {
			t.Errorf("Expected call %d to be %s, got: %v", i, expected, calls)
			break
		}
	}
}

func TestChain_StreamInterceptor_InterceptorError(t *testing.T) {
	calls := []string{}
	mock1 := &mockInterceptor{name: "interceptor1", calls: &calls}
	mock2 := &mockInterceptor{name: "interceptor2", calls: &calls, shouldError: true}

	chain := NewChain(mock1, mock2)
	interceptor := chain.StreamInterceptor()

	handlerCalled := false
	handler := func(srv interface{}, ss grpc.ServerStream) error {
		handlerCalled = true
		return nil
	}

	err := interceptor(nil, nil, &grpc.StreamServerInfo{}, handler)

	if err == nil || err.Error() != "interceptor2 error" {
		t.Errorf("Expected 'interceptor2 error', got: %v", err)
	}

	if handlerCalled {
		t.Error("Expected handler not to be called when interceptor returns error")
	}

	expectedCalls := []string{"interceptor1", "interceptor2"}
	if len(calls) != len(expectedCalls) {
		t.Errorf("Expected %d calls, got %d: %v", len(expectedCalls), len(calls), calls)
	}
}
