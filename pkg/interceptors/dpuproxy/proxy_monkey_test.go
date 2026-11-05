package dpuproxy

import (
	"context"
	"io"
	"testing"
	"unsafe"

	"github.com/agiledragon/gomonkey/v2"
	gnoi_file_pb "github.com/openconfig/gnoi/file"
	system "github.com/openconfig/gnoi/system"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Simple mock for system client that only implements Time method
type mockSystemClient struct{}

func (m *mockSystemClient) Time(ctx context.Context, in *system.TimeRequest, opts ...grpc.CallOption) (*system.TimeResponse, error) {
	return &system.TimeResponse{Time: 1234567890}, nil
}

// Implement other required methods as stubs
func (m *mockSystemClient) Reboot(ctx context.Context, in *system.RebootRequest, opts ...grpc.CallOption) (*system.RebootResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockSystemClient) KillProcess(ctx context.Context, in *system.KillProcessRequest, opts ...grpc.CallOption) (*system.KillProcessResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockSystemClient) SetPackage(ctx context.Context, opts ...grpc.CallOption) (system.System_SetPackageClient, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockSystemClient) SwitchControlProcessor(ctx context.Context, in *system.SwitchControlProcessorRequest, opts ...grpc.CallOption) (*system.SwitchControlProcessorResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockSystemClient) CancelReboot(ctx context.Context, in *system.CancelRebootRequest, opts ...grpc.CallOption) (*system.CancelRebootResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockSystemClient) Ping(ctx context.Context, in *system.PingRequest, opts ...grpc.CallOption) (system.System_PingClient, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockSystemClient) RebootStatus(ctx context.Context, in *system.RebootStatusRequest, opts ...grpc.CallOption) (*system.RebootStatusResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockSystemClient) Traceroute(ctx context.Context, in *system.TracerouteRequest, opts ...grpc.CallOption) (system.System_TracerouteClient, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func TestDPUProxy_forwardTimeRequest_Success(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock system.NewSystemClient to return our simple mock
	patches.ApplyFunc(system.NewSystemClient, func(cc grpc.ClientConnInterface) system.SystemClient {
		return &mockSystemClient{}
	})

	proxy := NewDPUProxy(nil)
	ctx := context.Background()

	req := &system.TimeRequest{}
	resp, err := proxy.forwardTimeRequest(ctx, nil, req)
	if err != nil {
		t.Fatalf("forwardTimeRequest() returned error: %v", err)
	}

	timeResp, ok := resp.(*system.TimeResponse)
	if !ok {
		t.Fatalf("Expected *system.TimeResponse, got %T", resp)
	}

	if timeResp.Time != 1234567890 {
		t.Errorf("Expected time 1234567890, got %d", timeResp.Time)
	}
}

func TestDPUProxy_forwardTimeRequest_InvalidRequestType(t *testing.T) {
	proxy := NewDPUProxy(nil)
	ctx := context.Background()

	// Pass wrong request type (string instead of *system.TimeRequest)
	resp, err := proxy.forwardTimeRequest(ctx, nil, "invalid request")
	if err == nil {
		t.Fatal("forwardTimeRequest() should return error for invalid request type")
	}

	if resp != nil {
		t.Error("forwardTimeRequest() should return nil response on error")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Errorf("Expected gRPC status error, got: %v", err)
	}
	if st.Code() != codes.Internal {
		t.Errorf("Expected Internal code, got: %v", st.Code())
	}
}

func TestDPUProxy_forwardTimeRequest_DPUError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock system.NewSystemClient to return a client that fails
	patches.ApplyFunc(system.NewSystemClient, func(cc grpc.ClientConnInterface) system.SystemClient {
		return &mockSystemClientWithError{}
	})

	proxy := NewDPUProxy(nil)
	ctx := context.Background()

	req := &system.TimeRequest{}
	resp, err := proxy.forwardTimeRequest(ctx, nil, req)
	if err == nil {
		t.Fatal("forwardTimeRequest() should return error when DPU fails")
	}

	if resp != nil {
		t.Error("forwardTimeRequest() should return nil response on error")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Errorf("Expected gRPC status error, got: %v", err)
	}
	if st.Code() != codes.Unavailable {
		t.Errorf("Expected Unavailable code, got: %v", st.Code())
	}
}

func TestDPUProxy_getForwardingMode_Success(t *testing.T) {
	proxy := NewDPUProxy(nil)

	// Test known forwardable method
	mode, found := proxy.getForwardingMode("/gnoi.system.System/Time")
	if !found {
		t.Error("Expected to find /gnoi.system.System/Time method")
	}
	if mode != ForwardToDPU {
		t.Errorf("Expected ForwardToDPU mode, got: %v", mode)
	}

	// Test HandleLocally method
	mode, found = proxy.getForwardingMode("/gnoi.system.System/Reboot")
	if !found {
		t.Error("Expected to find /gnoi.system.System/Reboot method")
	}
	if mode != HandleLocally {
		t.Errorf("Expected HandleLocally mode, got: %v", mode)
	}
}

func TestDPUProxy_getForwardingMode_NotFound(t *testing.T) {
	proxy := NewDPUProxy(nil)

	// Test unknown method
	mode, found := proxy.getForwardingMode("/unknown.Service/Method")
	if found {
		t.Error("Expected not to find unknown method")
	}
	if mode != "" {
		t.Errorf("Expected empty mode for unknown method, got: %v", mode)
	}
}

// Mock system client that returns errors
type mockSystemClientWithError struct{}

func (m *mockSystemClientWithError) Time(ctx context.Context, in *system.TimeRequest, opts ...grpc.CallOption) (*system.TimeResponse, error) {
	return nil, status.Error(codes.Unavailable, "DPU is down")
}

func (m *mockSystemClientWithError) Reboot(ctx context.Context, in *system.RebootRequest, opts ...grpc.CallOption) (*system.RebootResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockSystemClientWithError) KillProcess(ctx context.Context, in *system.KillProcessRequest, opts ...grpc.CallOption) (*system.KillProcessResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockSystemClientWithError) SetPackage(ctx context.Context, opts ...grpc.CallOption) (system.System_SetPackageClient, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockSystemClientWithError) SwitchControlProcessor(ctx context.Context, in *system.SwitchControlProcessorRequest, opts ...grpc.CallOption) (*system.SwitchControlProcessorResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockSystemClientWithError) CancelReboot(ctx context.Context, in *system.CancelRebootRequest, opts ...grpc.CallOption) (*system.CancelRebootResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockSystemClientWithError) Ping(ctx context.Context, in *system.PingRequest, opts ...grpc.CallOption) (system.System_PingClient, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockSystemClientWithError) RebootStatus(ctx context.Context, in *system.RebootStatusRequest, opts ...grpc.CallOption) (*system.RebootStatusResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockSystemClientWithError) Traceroute(ctx context.Context, in *system.TracerouteRequest, opts ...grpc.CallOption) (system.System_TracerouteClient, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

// Mock file client that returns errors
type mockFileClientWithError struct{}

func (m *mockFileClientWithError) Put(ctx context.Context, opts ...grpc.CallOption) (gnoi_file_pb.File_PutClient, error) {
	return nil, status.Error(codes.Internal, "failed to create client stream")
}

func (m *mockFileClientWithError) Get(ctx context.Context, in *gnoi_file_pb.GetRequest, opts ...grpc.CallOption) (gnoi_file_pb.File_GetClient, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockFileClientWithError) Remove(ctx context.Context, in *gnoi_file_pb.RemoveRequest, opts ...grpc.CallOption) (*gnoi_file_pb.RemoveResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockFileClientWithError) Stat(ctx context.Context, in *gnoi_file_pb.StatRequest, opts ...grpc.CallOption) (*gnoi_file_pb.StatResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockFileClientWithError) TransferToRemote(ctx context.Context, in *gnoi_file_pb.TransferToRemoteRequest, opts ...grpc.CallOption) (*gnoi_file_pb.TransferToRemoteResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

// Mock system client with SetPackage error
type mockSystemClientWithSetPackageError struct{}

func (m *mockSystemClientWithSetPackageError) Time(ctx context.Context, in *system.TimeRequest, opts ...grpc.CallOption) (*system.TimeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockSystemClientWithSetPackageError) Reboot(ctx context.Context, in *system.RebootRequest, opts ...grpc.CallOption) (*system.RebootResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockSystemClientWithSetPackageError) KillProcess(ctx context.Context, in *system.KillProcessRequest, opts ...grpc.CallOption) (*system.KillProcessResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockSystemClientWithSetPackageError) SetPackage(ctx context.Context, opts ...grpc.CallOption) (system.System_SetPackageClient, error) {
	return nil, status.Error(codes.Internal, "failed to create SetPackage client stream")
}

func (m *mockSystemClientWithSetPackageError) SwitchControlProcessor(ctx context.Context, in *system.SwitchControlProcessorRequest, opts ...grpc.CallOption) (*system.SwitchControlProcessorResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockSystemClientWithSetPackageError) CancelReboot(ctx context.Context, in *system.CancelRebootRequest, opts ...grpc.CallOption) (*system.CancelRebootResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockSystemClientWithSetPackageError) Ping(ctx context.Context, in *system.PingRequest, opts ...grpc.CallOption) (system.System_PingClient, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockSystemClientWithSetPackageError) RebootStatus(ctx context.Context, in *system.RebootStatusRequest, opts ...grpc.CallOption) (*system.RebootStatusResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockSystemClientWithSetPackageError) Traceroute(ctx context.Context, in *system.TracerouteRequest, opts ...grpc.CallOption) (system.System_TracerouteClient, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func TestDPUProxy_forwardStream_UnknownMethod(t *testing.T) {
	proxy := NewDPUProxy(nil)
	ctx := context.Background()

	info := &grpc.StreamServerInfo{FullMethod: "/unknown.Service/Method"}
	err := proxy.forwardStream(ctx, nil, nil, info)
	if err == nil {
		t.Fatal("forwardStream() should return error for unknown method")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Errorf("Expected gRPC status error, got: %v", err)
	}
	if st.Code() != codes.Unimplemented {
		t.Errorf("Expected Unimplemented code, got: %v", st.Code())
	}

	expectedMsg := "stream forwarding for method /unknown.Service/Method not implemented"
	if !containsSubstring(st.Message(), expectedMsg) {
		t.Errorf("Expected error message to contain '%s', got: %s", expectedMsg, st.Message())
	}
}

func TestDPUProxy_forwardFilePutStream_CreateClientError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock gnoi_file_pb.NewFileClient to return a client that fails
	patches.ApplyFunc(gnoi_file_pb.NewFileClient, func(cc grpc.ClientConnInterface) gnoi_file_pb.FileClient {
		return &mockFileClientWithError{}
	})

	proxy := NewDPUProxy(nil)
	ctx := context.Background()

	err := proxy.forwardFilePutStream(ctx, nil, nil)
	if err == nil {
		t.Fatal("forwardFilePutStream() should return error when client creation fails")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Errorf("Expected gRPC status error, got: %v", err)
	}
	if st.Code() != codes.Internal {
		t.Errorf("Expected Internal code, got: %v", st.Code())
	}
}

func TestDPUProxy_forwardSetPackageStream_CreateClientError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock system.NewSystemClient to return a client that fails
	patches.ApplyFunc(system.NewSystemClient, func(cc grpc.ClientConnInterface) system.SystemClient {
		return &mockSystemClientWithSetPackageError{}
	})

	proxy := NewDPUProxy(nil)
	ctx := context.Background()

	err := proxy.forwardSetPackageStream(ctx, nil, nil)
	if err == nil {
		t.Fatal("forwardSetPackageStream() should return error when client creation fails")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Errorf("Expected gRPC status error, got: %v", err)
	}
	if st.Code() != codes.Internal {
		t.Errorf("Expected Internal code, got: %v", st.Code())
	}
}

// Simple mock connection approach - just use a fixed instance and monkey patch all operations on it
var globalMockConn = func() *grpc.ClientConn {
	// Create a dummy connection by casting an empty struct
	var dummy struct{}
	return (*grpc.ClientConn)(unsafe.Pointer(&dummy))
}()

func TestDPUProxy_getConnection_CachedConnection(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock grpc.NewClient to return the global connection
	patches.ApplyFunc(grpc.NewClient, func(target string, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
		return globalMockConn, nil
	})

	// Mock the Close method to do nothing
	patches.ApplyMethod(&grpc.ClientConn{}, "Close", func(conn *grpc.ClientConn) error {
		return nil
	})

	// Mock system.NewSystemClient to return successful response
	patches.ApplyFunc(system.NewSystemClient, func(cc grpc.ClientConnInterface) system.SystemClient {
		return &mockSystemClient{}
	})

	proxy := NewDPUProxy(nil)
	ctx := context.Background()

	// First call should create and cache connection
	conn1, err := proxy.getConnection(ctx, "0", "192.168.1.10", []string{"8080"})
	if err != nil {
		t.Fatalf("getConnection() first call returned error: %v", err)
	}

	// Second call should return cached connection
	conn2, err := proxy.getConnection(ctx, "0", "192.168.1.10", []string{"8080"})
	if err != nil {
		t.Fatalf("getConnection() second call returned error: %v", err)
	}

	if conn1 != conn2 {
		t.Error("getConnection() should return same cached connection on second call")
	}
}

func TestDPUProxy_getConnection_NewClientError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock grpc.NewClient to return error
	patches.ApplyFunc(grpc.NewClient, func(target string, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
		return nil, status.Error(codes.Internal, "connection failed")
	})

	proxy := NewDPUProxy(nil)
	ctx := context.Background()

	conn, err := proxy.getConnection(ctx, "0", "192.168.1.10", []string{"8080", "9339"})
	if err == nil {
		t.Fatal("getConnection() should return error when grpc.NewClient fails")
	}

	if conn != nil {
		t.Error("getConnection() should return nil connection on failure")
	}

	expectedErrSubstr := "failed to connect to DPU0 on any port"
	if !containsSubstring(err.Error(), expectedErrSubstr) {
		t.Errorf("getConnection() error = %v, should contain '%s'", err, expectedErrSubstr)
	}
}

func TestDPUProxy_getConnection_FirstPortFailsSecondSucceeds(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	callCount := 0

	// Mock grpc.NewClient to succeed
	patches.ApplyFunc(grpc.NewClient, func(target string, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
		return globalMockConn, nil
	})

	// Mock the Close method to do nothing
	patches.ApplyMethod(&grpc.ClientConn{}, "Close", func(conn *grpc.ClientConn) error {
		return nil
	})

	// Mock system.NewSystemClient - first call fails, second succeeds
	patches.ApplyFunc(system.NewSystemClient, func(cc grpc.ClientConnInterface) system.SystemClient {
		return &mockSystemClientConditional{callCount: &callCount}
	})

	proxy := NewDPUProxy(nil)
	ctx := context.Background()

	conn, err := proxy.getConnection(ctx, "0", "192.168.1.10", []string{"8080", "9339"})
	if err != nil {
		t.Fatalf("getConnection() returned error: %v", err)
	}

	if conn == nil {
		t.Fatal("getConnection() returned nil connection")
	}

	// Should have cached the successful port (second one)
	if proxy.connPorts["0"] != "9339" {
		t.Errorf("Expected cached port to be '9339', got '%s'", proxy.connPorts["0"])
	}

	// Health check should have been called twice (once fail, once succeed)
	if callCount != 2 {
		t.Errorf("Expected 2 health check calls, got %d", callCount)
	}
}

func TestDPUProxy_getConnection_HealthCheckFails(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock grpc.NewClient to succeed (so we test the health check failure)
	patches.ApplyFunc(grpc.NewClient, func(target string, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
		return globalMockConn, nil
	})

	// Mock the Close method to do nothing
	patches.ApplyMethod(&grpc.ClientConn{}, "Close", func(conn *grpc.ClientConn) error {
		return nil
	})

	// Mock system.NewSystemClient to always return unavailable
	patches.ApplyFunc(system.NewSystemClient, func(cc grpc.ClientConnInterface) system.SystemClient {
		return &mockSystemClientWithError{}
	})

	proxy := NewDPUProxy(nil)
	ctx := context.Background()

	conn, err := proxy.getConnection(ctx, "0", "192.168.1.10", []string{"8080", "9339"})
	if err == nil {
		t.Fatal("getConnection() should return error when all ports fail health check")
	}

	if conn != nil {
		t.Error("getConnection() should return nil connection on failure")
	}
}

// Mock system client with conditional behavior
type mockSystemClientConditional struct {
	callCount *int
}

func (m *mockSystemClientConditional) Time(ctx context.Context, in *system.TimeRequest, opts ...grpc.CallOption) (*system.TimeResponse, error) {
	*m.callCount++
	if *m.callCount == 1 {
		return nil, status.Error(codes.Unavailable, "first port fails")
	}
	return &system.TimeResponse{Time: 1234567890}, nil
}

func (m *mockSystemClientConditional) Reboot(ctx context.Context, in *system.RebootRequest, opts ...grpc.CallOption) (*system.RebootResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockSystemClientConditional) KillProcess(ctx context.Context, in *system.KillProcessRequest, opts ...grpc.CallOption) (*system.KillProcessResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockSystemClientConditional) SetPackage(ctx context.Context, opts ...grpc.CallOption) (system.System_SetPackageClient, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockSystemClientConditional) SwitchControlProcessor(ctx context.Context, in *system.SwitchControlProcessorRequest, opts ...grpc.CallOption) (*system.SwitchControlProcessorResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockSystemClientConditional) CancelReboot(ctx context.Context, in *system.CancelRebootRequest, opts ...grpc.CallOption) (*system.CancelRebootResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockSystemClientConditional) Ping(ctx context.Context, in *system.PingRequest, opts ...grpc.CallOption) (system.System_PingClient, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockSystemClientConditional) RebootStatus(ctx context.Context, in *system.RebootStatusRequest, opts ...grpc.CallOption) (*system.RebootStatusResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockSystemClientConditional) Traceroute(ctx context.Context, in *system.TracerouteRequest, opts ...grpc.CallOption) (system.System_TracerouteClient, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func TestDPUProxy_UnaryInterceptor_ForwardToDPU_WithResolver(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock grpc.NewClient
	patches.ApplyFunc(grpc.NewClient, func(target string, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
		return globalMockConn, nil
	})

	// Mock system.NewSystemClient
	patches.ApplyFunc(system.NewSystemClient, func(cc grpc.ClientConnInterface) system.SystemClient {
		return &mockSystemClient{}
	})

	// Create mock resolver
	resolver := &DPUResolver{}
	patches.ApplyMethod(resolver, "GetDPUInfo", func(r *DPUResolver, ctx context.Context, dpuIndex string) (*DPUInfo, error) {
		return &DPUInfo{
			Index:          dpuIndex,
			IPAddress:      "192.168.1.10",
			Reachable:      true,
			GNMIPortsToTry: []string{"8080"},
		}, nil
	})

	proxy := NewDPUProxy(resolver)
	interceptor := proxy.UnaryInterceptor()

	handlerCalled := false
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		handlerCalled = true
		return nil, nil
	}

	md := metadata.New(map[string]string{
		MetadataKeyTargetType:  "dpu",
		MetadataKeyTargetIndex: "0",
	})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	// Test ForwardToDPU method
	resp, err := interceptor(ctx, &system.TimeRequest{}, &grpc.UnaryServerInfo{FullMethod: "/gnoi.system.System/Time"}, handler)

	if err != nil {
		t.Fatalf("UnaryInterceptor() returned error: %v", err)
	}

	if handlerCalled {
		t.Error("Handler should not be called when forwarding to DPU")
	}

	timeResp, ok := resp.(*system.TimeResponse)
	if !ok {
		t.Fatalf("Expected *system.TimeResponse, got %T", resp)
	}

	if timeResp.Time != 1234567890 {
		t.Errorf("Expected time 1234567890, got %d", timeResp.Time)
	}
}

func TestDPUProxy_UnaryInterceptor_ForwardToDPU_ResolverError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Create mock resolver that returns error
	resolver := &DPUResolver{}
	patches.ApplyMethod(resolver, "GetDPUInfo", func(r *DPUResolver, ctx context.Context, dpuIndex string) (*DPUInfo, error) {
		return nil, status.Error(codes.NotFound, "DPU not found")
	})

	proxy := NewDPUProxy(resolver)
	interceptor := proxy.UnaryInterceptor()

	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		t.Error("Handler should not be called")
		return nil, nil
	}

	md := metadata.New(map[string]string{
		MetadataKeyTargetType:  "dpu",
		MetadataKeyTargetIndex: "0",
	})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	resp, err := interceptor(ctx, &system.TimeRequest{}, &grpc.UnaryServerInfo{FullMethod: "/gnoi.system.System/Time"}, handler)

	if err == nil {
		t.Fatal("UnaryInterceptor() should return error when resolver fails")
	}

	if resp != nil {
		t.Error("UnaryInterceptor() should return nil response on error")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Errorf("Expected gRPC status error, got: %v", err)
	}
	if st.Code() != codes.NotFound {
		t.Errorf("Expected NotFound code, got: %v", st.Code())
	}
}

func TestDPUProxy_UnaryInterceptor_ForwardToDPU_DPUUnreachable(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Create mock resolver that returns unreachable DPU
	resolver := &DPUResolver{}
	patches.ApplyMethod(resolver, "GetDPUInfo", func(r *DPUResolver, ctx context.Context, dpuIndex string) (*DPUInfo, error) {
		return &DPUInfo{
			Index:          dpuIndex,
			IPAddress:      "192.168.1.10",
			Reachable:      false, // Unreachable!
			GNMIPortsToTry: []string{"8080"},
		}, nil
	})

	proxy := NewDPUProxy(resolver)
	interceptor := proxy.UnaryInterceptor()

	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		t.Error("Handler should not be called")
		return nil, nil
	}

	md := metadata.New(map[string]string{
		MetadataKeyTargetType:  "dpu",
		MetadataKeyTargetIndex: "0",
	})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	resp, err := interceptor(ctx, &system.TimeRequest{}, &grpc.UnaryServerInfo{FullMethod: "/gnoi.system.System/Time"}, handler)

	if err == nil {
		t.Fatal("UnaryInterceptor() should return error when DPU is unreachable")
	}

	if resp != nil {
		t.Error("UnaryInterceptor() should return nil response on error")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Errorf("Expected gRPC status error, got: %v", err)
	}
	if st.Code() != codes.Unavailable {
		t.Errorf("Expected Unavailable code, got: %v", st.Code())
	}
}

// Mock streaming clients for testing
type mockFilePutClient struct {
	sendErr   error
	recvErr   error
	response  *gnoi_file_pb.PutResponse
	sendCount int
	maxSends  int
}

func (m *mockFilePutClient) Send(req *gnoi_file_pb.PutRequest) error {
	m.sendCount++
	if m.maxSends > 0 && m.sendCount > m.maxSends {
		return io.EOF
	}
	return m.sendErr
}

func (m *mockFilePutClient) CloseAndRecv() (*gnoi_file_pb.PutResponse, error) {
	if m.recvErr != nil {
		return nil, m.recvErr
	}
	return m.response, nil
}

func (m *mockFilePutClient) CloseSend() error {
	return nil
}

func (m *mockFilePutClient) Header() (metadata.MD, error)  { return nil, nil }
func (m *mockFilePutClient) Trailer() metadata.MD          { return nil }
func (m *mockFilePutClient) Context() context.Context      { return context.Background() }
func (m *mockFilePutClient) RecvMsg(msg interface{}) error { return nil }
func (m *mockFilePutClient) SendMsg(msg interface{}) error {
	return m.Send(msg.(*gnoi_file_pb.PutRequest))
}

type mockSetPackageClient struct {
	sendErr   error
	recvErr   error
	response  *system.SetPackageResponse
	sendCount int
	maxSends  int
}

func (m *mockSetPackageClient) Send(req *system.SetPackageRequest) error {
	m.sendCount++
	if m.maxSends > 0 && m.sendCount > m.maxSends {
		return io.EOF
	}
	return m.sendErr
}

func (m *mockSetPackageClient) CloseAndRecv() (*system.SetPackageResponse, error) {
	if m.recvErr != nil {
		return nil, m.recvErr
	}
	return m.response, nil
}

func (m *mockSetPackageClient) CloseSend() error {
	return nil
}

func (m *mockSetPackageClient) Header() (metadata.MD, error)  { return nil, nil }
func (m *mockSetPackageClient) Trailer() metadata.MD          { return nil }
func (m *mockSetPackageClient) Context() context.Context      { return context.Background() }
func (m *mockSetPackageClient) RecvMsg(msg interface{}) error { return nil }
func (m *mockSetPackageClient) SendMsg(msg interface{}) error {
	return m.Send(msg.(*system.SetPackageRequest))
}

// Mock server stream for testing
type mockServerStreamForProxy struct {
	grpc.ServerStream
	ctx         context.Context
	recvMsgFunc func(interface{}) error
	sendMsgFunc func(interface{}) error
	recvCount   int
	maxRecvs    int
}

func (m *mockServerStreamForProxy) Context() context.Context {
	return m.ctx
}

func (m *mockServerStreamForProxy) RecvMsg(msg interface{}) error {
	m.recvCount++
	if m.maxRecvs > 0 && m.recvCount > m.maxRecvs {
		return io.EOF
	}
	if m.recvMsgFunc != nil {
		return m.recvMsgFunc(msg)
	}
	return nil
}

func (m *mockServerStreamForProxy) SendMsg(msg interface{}) error {
	if m.sendMsgFunc != nil {
		return m.sendMsgFunc(msg)
	}
	return nil
}

// Mock file client for successful streaming
type mockFileClientSuccess struct{}

func (m *mockFileClientSuccess) Put(ctx context.Context, opts ...grpc.CallOption) (gnoi_file_pb.File_PutClient, error) {
	return &mockFilePutClient{
		response: &gnoi_file_pb.PutResponse{},
		maxSends: 2, // Allow 2 sends before EOF
	}, nil
}

func (m *mockFileClientSuccess) Get(ctx context.Context, in *gnoi_file_pb.GetRequest, opts ...grpc.CallOption) (gnoi_file_pb.File_GetClient, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockFileClientSuccess) Remove(ctx context.Context, in *gnoi_file_pb.RemoveRequest, opts ...grpc.CallOption) (*gnoi_file_pb.RemoveResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockFileClientSuccess) Stat(ctx context.Context, in *gnoi_file_pb.StatRequest, opts ...grpc.CallOption) (*gnoi_file_pb.StatResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockFileClientSuccess) TransferToRemote(ctx context.Context, in *gnoi_file_pb.TransferToRemoteRequest, opts ...grpc.CallOption) (*gnoi_file_pb.TransferToRemoteResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

// Mock system client for successful SetPackage streaming
type mockSystemClientSuccessSetPackage struct{}

func (m *mockSystemClientSuccessSetPackage) Time(ctx context.Context, in *system.TimeRequest, opts ...grpc.CallOption) (*system.TimeResponse, error) {
	return &system.TimeResponse{Time: 1234567890}, nil
}

func (m *mockSystemClientSuccessSetPackage) SetPackage(ctx context.Context, opts ...grpc.CallOption) (system.System_SetPackageClient, error) {
	return &mockSetPackageClient{
		response: &system.SetPackageResponse{},
		maxSends: 2, // Allow 2 sends before EOF
	}, nil
}

func (m *mockSystemClientSuccessSetPackage) Reboot(ctx context.Context, in *system.RebootRequest, opts ...grpc.CallOption) (*system.RebootResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockSystemClientSuccessSetPackage) KillProcess(ctx context.Context, in *system.KillProcessRequest, opts ...grpc.CallOption) (*system.KillProcessResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockSystemClientSuccessSetPackage) SwitchControlProcessor(ctx context.Context, in *system.SwitchControlProcessorRequest, opts ...grpc.CallOption) (*system.SwitchControlProcessorResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockSystemClientSuccessSetPackage) CancelReboot(ctx context.Context, in *system.CancelRebootRequest, opts ...grpc.CallOption) (*system.CancelRebootResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockSystemClientSuccessSetPackage) Ping(ctx context.Context, in *system.PingRequest, opts ...grpc.CallOption) (system.System_PingClient, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockSystemClientSuccessSetPackage) RebootStatus(ctx context.Context, in *system.RebootStatusRequest, opts ...grpc.CallOption) (*system.RebootStatusResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockSystemClientSuccessSetPackage) Traceroute(ctx context.Context, in *system.TracerouteRequest, opts ...grpc.CallOption) (system.System_TracerouteClient, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func TestDPUProxy_forwardFilePutStream_Success(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock gnoi_file_pb.NewFileClient to return successful client
	patches.ApplyFunc(gnoi_file_pb.NewFileClient, func(cc grpc.ClientConnInterface) gnoi_file_pb.FileClient {
		return &mockFileClientSuccess{}
	})

	proxy := NewDPUProxy(nil)
	ctx := context.Background()

	// Create mock server stream that sends two messages then EOF
	ss := &mockServerStreamForProxy{
		ctx:      ctx,
		maxRecvs: 2,
		recvMsgFunc: func(msg interface{}) error {
			// Simulate receiving a PutRequest
			if req, ok := msg.(*gnoi_file_pb.PutRequest); ok {
				req.Request = &gnoi_file_pb.PutRequest_Open{
					Open: &gnoi_file_pb.PutRequest_Details{
						RemoteFile:  "/test/file.txt",
						Permissions: 0644,
					},
				}
			}
			return nil
		},
		sendMsgFunc: func(msg interface{}) error {
			// Simulate successful response send
			return nil
		},
	}

	err := proxy.forwardFilePutStream(ctx, globalMockConn, ss)
	if err != nil {
		t.Fatalf("forwardFilePutStream() returned error: %v", err)
	}
}

func TestDPUProxy_forwardFilePutStream_StreamSendError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock gnoi_file_pb.NewFileClient to return client with send error
	patches.ApplyFunc(gnoi_file_pb.NewFileClient, func(cc grpc.ClientConnInterface) gnoi_file_pb.FileClient {
		return &mockFileClientSuccess{}
	})

	proxy := NewDPUProxy(nil)
	ctx := context.Background()

	// Create mock server stream with send error
	ss := &mockServerStreamForProxy{
		ctx:      ctx,
		maxRecvs: 1,
		recvMsgFunc: func(msg interface{}) error {
			return nil
		},
		sendMsgFunc: func(msg interface{}) error {
			return status.Error(codes.Internal, "send failed")
		},
	}

	err := proxy.forwardFilePutStream(ctx, globalMockConn, ss)
	if err == nil {
		t.Fatal("forwardFilePutStream() should return error when send fails")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Errorf("Expected gRPC status error, got: %v", err)
	}
	if st.Code() != codes.Internal {
		t.Errorf("Expected Internal code, got: %v", st.Code())
	}
}

func TestDPUProxy_forwardSetPackageStream_Success(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock system.NewSystemClient to return successful client
	patches.ApplyFunc(system.NewSystemClient, func(cc grpc.ClientConnInterface) system.SystemClient {
		return &mockSystemClientSuccessSetPackage{}
	})

	proxy := NewDPUProxy(nil)
	ctx := context.Background()

	// Create mock server stream that sends two messages then EOF
	ss := &mockServerStreamForProxy{
		ctx:      ctx,
		maxRecvs: 2,
		recvMsgFunc: func(msg interface{}) error {
			// Simulate receiving a SetPackageRequest
			if req, ok := msg.(*system.SetPackageRequest); ok {
				req.Request = &system.SetPackageRequest_Package{
					Package: &system.Package{
						Filename: "/test/package.bin",
						Version:  "1.0.0",
					},
				}
			}
			return nil
		},
		sendMsgFunc: func(msg interface{}) error {
			return nil
		},
	}

	err := proxy.forwardSetPackageStream(ctx, globalMockConn, ss)
	if err != nil {
		t.Fatalf("forwardSetPackageStream() returned error: %v", err)
	}
}

func TestDPUProxy_forwardSetPackageStream_RecvError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock system.NewSystemClient to return successful client
	patches.ApplyFunc(system.NewSystemClient, func(cc grpc.ClientConnInterface) system.SystemClient {
		return &mockSystemClientSuccessSetPackage{}
	})

	proxy := NewDPUProxy(nil)
	ctx := context.Background()

	// Create mock server stream with recv error
	ss := &mockServerStreamForProxy{
		ctx:      ctx,
		maxRecvs: 1,
		recvMsgFunc: func(msg interface{}) error {
			return status.Error(codes.Internal, "recv failed")
		},
		sendMsgFunc: func(msg interface{}) error {
			return nil
		},
	}

	err := proxy.forwardSetPackageStream(ctx, globalMockConn, ss)
	if err == nil {
		t.Fatal("forwardSetPackageStream() should return error when recv fails")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Errorf("Expected gRPC status error, got: %v", err)
	}
	if st.Code() != codes.Internal {
		t.Errorf("Expected Internal code, got: %v", st.Code())
	}
}

func TestDPUProxy_forwardFilePutStream_RecvError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock gnoi_file_pb.NewFileClient to return successful client
	patches.ApplyFunc(gnoi_file_pb.NewFileClient, func(cc grpc.ClientConnInterface) gnoi_file_pb.FileClient {
		return &mockFileClientSuccess{}
	})

	proxy := NewDPUProxy(nil)
	ctx := context.Background()

	// Create mock server stream with recv error
	ss := &mockServerStreamForProxy{
		ctx:      ctx,
		maxRecvs: 1,
		recvMsgFunc: func(msg interface{}) error {
			return status.Error(codes.Internal, "recv failed")
		},
		sendMsgFunc: func(msg interface{}) error {
			return nil
		},
	}

	err := proxy.forwardFilePutStream(ctx, globalMockConn, ss)
	if err == nil {
		t.Fatal("forwardFilePutStream() should return error when recv fails")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Errorf("Expected gRPC status error, got: %v", err)
	}
	if st.Code() != codes.Internal {
		t.Errorf("Expected Internal code, got: %v", st.Code())
	}
}

func TestDPUProxy_StreamInterceptor_ForwardToDPU_GetConnectionError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock grpc.NewClient to return error
	patches.ApplyFunc(grpc.NewClient, func(target string, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
		return nil, status.Error(codes.Internal, "connection failed")
	})

	// Create mock resolver
	resolver := &DPUResolver{}
	patches.ApplyMethod(resolver, "GetDPUInfo", func(r *DPUResolver, ctx context.Context, dpuIndex string) (*DPUInfo, error) {
		return &DPUInfo{
			Index:          dpuIndex,
			IPAddress:      "192.168.1.10",
			Reachable:      true,
			GNMIPortsToTry: []string{"8080"},
		}, nil
	})

	proxy := NewDPUProxy(resolver)
	interceptor := proxy.StreamInterceptor()

	handlerCalled := false
	handler := func(srv interface{}, ss grpc.ServerStream) error {
		handlerCalled = true
		return nil
	}

	md := metadata.New(map[string]string{
		MetadataKeyTargetType:  "dpu",
		MetadataKeyTargetIndex: "0",
	})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	ss := &mockServerStreamForProxy{ctx: ctx}

	// Test ForwardToDPU method with connection error
	err := interceptor(nil, ss, &grpc.StreamServerInfo{FullMethod: "/gnoi.file.File/Put"}, handler)

	if err == nil {
		t.Fatal("StreamInterceptor() should return error when connection fails")
	}

	if handlerCalled {
		t.Error("Handler should not be called when forwarding fails")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Errorf("Expected gRPC status error, got: %v", err)
	}
	if st.Code() != codes.Internal {
		t.Errorf("Expected Internal code, got: %v", st.Code())
	}
}

func TestDPUProxy_StreamInterceptor_HandleLocally(t *testing.T) {
	proxy := NewDPUProxy(nil)
	interceptor := proxy.StreamInterceptor()

	handlerCalled := false
	handler := func(srv interface{}, ss grpc.ServerStream) error {
		handlerCalled = true
		return nil
	}

	md := metadata.New(map[string]string{
		MetadataKeyTargetType:  "dpu",
		MetadataKeyTargetIndex: "0",
	})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	ss := &mockServerStreamForProxy{ctx: ctx}

	// Test HandleLocally method (e.g., Reboot)
	err := interceptor(nil, ss, &grpc.StreamServerInfo{FullMethod: "/gnoi.system.System/Reboot"}, handler)

	if err != nil {
		t.Fatalf("StreamInterceptor() returned error: %v", err)
	}

	if !handlerCalled {
		t.Error("Handler should be called for HandleLocally mode")
	}
}

func TestDPUProxy_StreamInterceptor_ResolverError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Create mock resolver that returns error
	resolver := &DPUResolver{}
	patches.ApplyMethod(resolver, "GetDPUInfo", func(r *DPUResolver, ctx context.Context, dpuIndex string) (*DPUInfo, error) {
		return nil, status.Error(codes.NotFound, "DPU not found")
	})

	proxy := NewDPUProxy(resolver)
	interceptor := proxy.StreamInterceptor()

	handler := func(srv interface{}, ss grpc.ServerStream) error {
		t.Error("Handler should not be called")
		return nil
	}

	md := metadata.New(map[string]string{
		MetadataKeyTargetType:  "dpu",
		MetadataKeyTargetIndex: "0",
	})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	ss := &mockServerStreamForProxy{ctx: ctx}

	err := interceptor(nil, ss, &grpc.StreamServerInfo{FullMethod: "/gnoi.file.File/Put"}, handler)

	if err == nil {
		t.Fatal("StreamInterceptor() should return error when resolver fails")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Errorf("Expected gRPC status error, got: %v", err)
	}
	if st.Code() != codes.NotFound {
		t.Errorf("Expected NotFound code, got: %v", st.Code())
	}
}

func TestDPUProxy_StreamInterceptor_DPUUnreachable(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Create mock resolver that returns unreachable DPU
	resolver := &DPUResolver{}
	patches.ApplyMethod(resolver, "GetDPUInfo", func(r *DPUResolver, ctx context.Context, dpuIndex string) (*DPUInfo, error) {
		return &DPUInfo{
			Index:          dpuIndex,
			IPAddress:      "192.168.1.10",
			Reachable:      false, // Unreachable!
			GNMIPortsToTry: []string{"8080"},
		}, nil
	})

	proxy := NewDPUProxy(resolver)
	interceptor := proxy.StreamInterceptor()

	handler := func(srv interface{}, ss grpc.ServerStream) error {
		t.Error("Handler should not be called")
		return nil
	}

	md := metadata.New(map[string]string{
		MetadataKeyTargetType:  "dpu",
		MetadataKeyTargetIndex: "0",
	})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	ss := &mockServerStreamForProxy{ctx: ctx}

	err := interceptor(nil, ss, &grpc.StreamServerInfo{FullMethod: "/gnoi.file.File/Put"}, handler)

	if err == nil {
		t.Fatal("StreamInterceptor() should return error when DPU is unreachable")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Errorf("Expected gRPC status error, got: %v", err)
	}
	if st.Code() != codes.Unavailable {
		t.Errorf("Expected Unavailable code, got: %v", st.Code())
	}
}

// Helper function to check if a string contains a substring
func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
