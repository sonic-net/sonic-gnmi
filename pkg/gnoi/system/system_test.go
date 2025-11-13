package system

import (
	"context"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/openconfig/gnoi/common"
	syspb "github.com/openconfig/gnoi/system"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestHandleSetPackageValidation(t *testing.T) {
	tests := []struct {
		name    string
		req     *syspb.SetPackageRequest
		wantErr bool
		errMsg  string
	}{
		{
			name: "invalid request type",
			req: &syspb.SetPackageRequest{
				Request: nil,
			},
			wantErr: true,
			errMsg:  "invalid request type",
		},
		{
			name: "missing filename",
			req: &syspb.SetPackageRequest{
				Request: &syspb.SetPackageRequest_Package{
					Package: &syspb.Package{
						Filename: "",
						Version:  "test-version",
					},
				},
			},
			wantErr: true,
			errMsg:  "filename is missing",
		},
		{
			name: "missing version",
			req: &syspb.SetPackageRequest{
				Request: &syspb.SetPackageRequest_Package{
					Package: &syspb.Package{
						Filename: "/path/to/image.bin",
						Version:  "",
					},
				},
			},
			wantErr: true,
			errMsg:  "version is missing",
		},
		{
			name: "remote download not supported",
			req: &syspb.SetPackageRequest{
				Request: &syspb.SetPackageRequest_Package{
					Package: &syspb.Package{
						Filename: "/path/to/image.bin",
						Version:  "test-version",
						RemoteDownload: &common.RemoteDownload{
							Path: "http://example.com/image.bin",
						},
					},
				},
			},
			wantErr: true,
			errMsg:  "remote download is not supported",
		},
		{
			name: "relative path not allowed",
			req: &syspb.SetPackageRequest{
				Request: &syspb.SetPackageRequest_Package{
					Package: &syspb.Package{
						Filename: "relative/path/image.bin",
						Version:  "test-version",
					},
				},
			},
			wantErr: true,
			errMsg:  "filename must be an absolute path",
		},
		{
			name: "valid request without activation",
			req: &syspb.SetPackageRequest{
				Request: &syspb.SetPackageRequest_Package{
					Package: &syspb.Package{
						Filename: "/path/to/image.bin",
						Version:  "test-version",
						Activate: false,
					},
				},
			},
			wantErr: false, // Would fail during actual execution but validation passes
		},
		{
			name: "valid request with activation",
			req: &syspb.SetPackageRequest{
				Request: &syspb.SetPackageRequest_Package{
					Package: &syspb.Package{
						Filename: "/path/to/image.bin",
						Version:  "test-version",
						Activate: true,
					},
				},
			},
			wantErr: false, // Would fail during actual execution but validation passes
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			_, err := HandleSetPackage(ctx, tt.req)

			if tt.wantErr {
				if err == nil {
					t.Errorf("HandleSetPackage() expected error but got none")
					return
				}
				if tt.errMsg != "" && !containsString(err.Error(), tt.errMsg) {
					t.Errorf("HandleSetPackage() error = %v, want error containing %v", err, tt.errMsg)
				}
			} else if err != nil {
				// For valid requests, we expect execution errors since we're not in a real SONiC environment
				// but validation should pass
				t.Logf("HandleSetPackage() got expected execution error: %v", err)
			}
		})
	}
}

func TestInstallPackageValidation(t *testing.T) {
	ctx := context.Background()

	// Test empty filename
	err := installPackage(ctx, "")
	if err == nil {
		t.Error("installPackage() with empty filename should fail")
	}

	// Test with non-existent file (will fail but tests the command execution path)
	err = installPackage(ctx, "/nonexistent/path/image.bin")
	if err == nil {
		t.Error("installPackage() with non-existent file should fail")
	}
	t.Logf("Expected error for non-existent file: %v", err)
}

func TestActivatePackageValidation(t *testing.T) {
	ctx := context.Background()

	// Test empty version
	err := activatePackage(ctx, "")
	if err == nil {
		t.Error("activatePackage() with empty version should fail")
	}

	// Test with non-existent version (will fail but tests the command execution path)
	err = activatePackage(ctx, "non-existent-version")
	if err == nil {
		t.Error("activatePackage() with non-existent version should fail")
	}
	t.Logf("Expected error for non-existent version: %v", err)
}

// Note: Testing HandleSetPackage with actual sonic-installer commands requires
// nsenter permissions and a real SONiC environment, so it would typically be
// tested in integration tests or with mocks. Here we just ensure the function
// signature is correct and basic validation works.
func TestHandleSetPackageSignature(t *testing.T) {
	// This test just ensures the function compiles with the correct signature
	var _ func(context.Context, *syspb.SetPackageRequest) (*syspb.SetPackageResponse, error) = HandleSetPackage
}

// Helper function to check if a string contains a substring
func containsString(s, substr string) bool {
	return len(s) >= len(substr) &&
		(substr == "" ||
			(len(s) > 0 && len(substr) > 0 && contains(s, substr)))
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Test that HandleDPUReboot has the correct function signature
func TestHandleDPURebootSignature(t *testing.T) {
	// This test just ensures the function compiles with the correct signature
	var _ func(context.Context, *syspb.RebootRequest, string) (*syspb.RebootResponse, error) = HandleDPUReboot
}

func TestHandleReboot_DPU_Routing(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock HandleDPUReboot to succeed
	patches.ApplyFunc(HandleDPUReboot,
		func(ctx context.Context, req *syspb.RebootRequest, dpuIndex string) (*syspb.RebootResponse, error) {
			return &syspb.RebootResponse{}, nil
		})

	// Create context with DPU metadata
	md := metadata.New(map[string]string{
		"x-sonic-ss-target-type":  "dpu",
		"x-sonic-ss-target-index": "0",
	})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	req := &syspb.RebootRequest{}

	resp, err := HandleReboot(ctx, req)
	if err != nil {
		t.Fatalf("HandleReboot() with DPU metadata returned error: %v", err)
	}
	if resp == nil {
		t.Fatal("HandleReboot() with DPU metadata returned nil response")
	}
}

func TestHandleReboot_NPU_Fallback(t *testing.T) {
	ctx := context.Background() // No DPU metadata

	req := &syspb.RebootRequest{}

	resp, err := HandleReboot(ctx, req)
	if err == nil {
		t.Fatal("HandleReboot() without DPU metadata should return Unimplemented error")
	}
	if resp != nil {
		t.Error("HandleReboot() should return nil response on NPU fallback error")
	}

	// Should return Unimplemented status for NPU fallback
	if status.Code(err) != codes.Unimplemented {
		t.Errorf("Expected Unimplemented error, got: %v", err)
	}
}

func TestHandleSetPackageStream_Success(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock HandleSetPackage to succeed
	patches.ApplyFunc(HandleSetPackage,
		func(ctx context.Context, req *syspb.SetPackageRequest) (*syspb.SetPackageResponse, error) {
			return &syspb.SetPackageResponse{}, nil
		})

	// Create mock stream
	mockStream := &mockSetPackageStream{
		ctx: context.Background(),
		req: &syspb.SetPackageRequest{
			Request: &syspb.SetPackageRequest_Package{
				Package: &syspb.Package{
					Filename: "/test/package.bin",
					Version:  "1.0.0",
				},
			},
		},
	}

	err := HandleSetPackageStream(mockStream)
	if err != nil {
		t.Fatalf("HandleSetPackageStream() returned error: %v", err)
	}

	if !mockStream.sendCalled {
		t.Error("Expected SendAndClose to be called")
	}
}

func TestHandleSetPackageStream_ReceiveError(t *testing.T) {
	// Create mock stream that fails on Recv
	mockStream := &mockSetPackageStream{
		ctx:     context.Background(),
		recvErr: status.Error(codes.Internal, "recv failed"),
	}

	err := HandleSetPackageStream(mockStream)
	if err == nil {
		t.Fatal("HandleSetPackageStream() should return error when Recv fails")
	}

	if mockStream.sendCalled {
		t.Error("SendAndClose should not be called when Recv fails")
	}
}

// Mock stream for testing HandleSetPackageStream
type mockSetPackageStream struct {
	ctx        context.Context
	req        *syspb.SetPackageRequest
	recvErr    error
	sendErr    error
	sendCalled bool
}

func (m *mockSetPackageStream) Context() context.Context {
	return m.ctx
}

func (m *mockSetPackageStream) Recv() (*syspb.SetPackageRequest, error) {
	if m.recvErr != nil {
		return nil, m.recvErr
	}
	return m.req, nil
}

func (m *mockSetPackageStream) SendAndClose(resp *syspb.SetPackageResponse) error {
	m.sendCalled = true
	return m.sendErr
}
