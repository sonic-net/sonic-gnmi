package system

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/openconfig/gnoi/system"
	"google.golang.org/grpc"
)

// TestValidateFlags verifies flag validation logic using a table-driven approach.
func TestValidateFlags(t *testing.T) {
	// Save original flag values
	origFilename := *filename
	origVersion := *version
	origURL := *url
	origActivate := *activate

	// Restore them when test finishes
	defer func() {
		*filename = origFilename
		*version = origVersion
		*url = origURL
		*activate = origActivate
	}()

	tests := []struct {
		name    string
		fn      string
		ver     string
		u       string
		act     bool
		wantErr bool
		errSub  string // substring we expect in the error
	}{
		{
			name:    "Missing filename",
			fn:      "",
			ver:     "1.0",
			u:       "http://example.com/pkg",
			act:     true,
			wantErr: true,
			errSub:  "missing -package_filename",
		},
		{
			name:    "Missing version",
			fn:      "sonic.pkg",
			ver:     "",
			u:       "http://example.com/pkg",
			act:     true,
			wantErr: true,
			errSub:  "missing -package_version",
		},
		{
			name:    "Missing url",
			fn:      "sonic.pkg",
			ver:     "1.0",
			u:       "",
			act:     true,
			wantErr: true,
			errSub:  "missing -package_url",
		},
		{
			name:    "Activate false not supported",
			fn:      "sonic.pkg",
			ver:     "1.0",
			u:       "http://example.com/pkg",
			act:     false,
			wantErr: true,
			errSub:  "-package_activate=false is not yet supported",
		},
		{
			name:    "Valid flags",
			fn:      "sonic.pkg",
			ver:     "1.0",
			u:       "http://example.com/pkg",
			act:     true,
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			*filename = tc.fn
			*version = tc.ver
			*url = tc.u
			*activate = tc.act

			err := validateFlags()
			if tc.wantErr && err == nil {
				t.Fatalf("Expected an error, got none")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("Did not expect error, got %v", err)
			}
			if tc.wantErr && err != nil && !strings.Contains(err.Error(), tc.errSub) {
				t.Fatalf("Expected error containing %q, got %v", tc.errSub, err)
			}
		})
	}
}

// TestSetPackage_FlagValidationError ensures SetPackage prints the flag error and returns
// if validateFlags() fails, without calling setPackageClient logic.
func TestSetPackage_FlagValidationError(t *testing.T) {
	// Force an error by leaving -package_filename blank
	*filename = ""
	*version = "1.0"
	*url = "http://example.com/package"
	*activate = true

	// Capture stdout to see the printed error
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// We pass a nil *grpc.ClientConn here because we expect an early return.
	SetPackage(nil, context.Background())

	// Restore stdout
	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	io.Copy(&buf, r)

	output := buf.String()
	if !strings.Contains(output, "Error validating flags") {
		t.Errorf("Expected validation error in output, got:\n%s", output)
	}
	if !strings.Contains(output, "missing -package_filename") {
		t.Errorf("Expected 'missing -package_filename' in error, got:\n%s", output)
	}
}

// TestSetPackage_Success tests that SetPackage calls newSystemClient, then setPackageClient,
// and the gRPC flow with our mock client is correct.
func TestSetPackage_Success(t *testing.T) {
	// We'll mock out the entire gRPC flow.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := NewMockSystemClient(ctrl)
	mockStream := NewMockSystem_SetPackageClient(ctrl)

	// Override the global newSystemClient to return our mock client.
	original := newSystemClient
	newSystemClient = func(conn *grpc.ClientConn) system.SystemClient {
		return mockClient
	}
	defer func() { newSystemClient = original }()

	// Provide valid flags so validateFlags() passes.
	*filename = "sonic.pkg"
	*version = "1.0"
	*url = "http://example.com/pkg"
	*activate = true

	// We'll capture stdout to verify what's printed at the end.
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// We expect the mockClient.SetPackage(...) to be called and return mockStream.
	mockClient.EXPECT().SetPackage(gomock.Any()).Return(mockStream, nil)

	// Next, we expect the stream.Send(...) call.
	// If you want to inspect the request, you can use gomock.Any() or a custom matcher.
	mockStream.EXPECT().Send(gomock.Any()).Return(nil)

	// Then CloseSend().
	mockStream.EXPECT().CloseSend().Return(nil)

	// Finally CloseAndRecv(), returning a dummy SetPackageResponse.
	mockStream.EXPECT().CloseAndRecv().Return(&system.SetPackageResponse{}, nil)

	// Actually call SetPackage; pass a nil conn since it won't be used (we override newSystemClient).
	SetPackage(nil, context.Background())

	// Restore stdout
	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	if !strings.Contains(output, "System SetPackage") {
		t.Errorf("Expected 'System SetPackage' in output, got:\n%s", output)
	}
}
