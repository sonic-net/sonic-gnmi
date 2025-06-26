package server

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/hostinfo"
	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/hostinfo/mocks"
	pb "github.com/sonic-net/sonic-gnmi/upgrade-service/proto"
)

// testCase defines the structure for test cases.
type testCase struct {
	name                       string
	setupMock                  func(*mocks.MockPlatformInfoProvider)
	expectedPlatformIdentifier string
	expectedVendor             string
	expectedModel              string
	expectError                bool
}

// validateTestResult is a helper function to reduce complexity in test assertions.
func validateTestResult(t *testing.T, test testCase, resp *pb.GetPlatformTypeResponse, err error) {
	t.Helper()

	if test.expectError {
		if err == nil {
			t.Error("Expected an error but got nil")
		}
		return
	}

	if err != nil {
		t.Errorf("Did not expect an error but got: %v", err)
		return
	}

	if resp == nil {
		t.Error("Expected a response but got nil")
		return
	}

	if resp.PlatformIdentifier != test.expectedPlatformIdentifier {
		t.Errorf("Expected platform identifier %v but got %v",
			test.expectedPlatformIdentifier, resp.PlatformIdentifier)
	}
	if resp.Vendor != test.expectedVendor {
		t.Errorf("Expected vendor %v but got %v",
			test.expectedVendor, resp.Vendor)
	}
	if resp.Model != test.expectedModel {
		t.Errorf("Expected model %v but got %v",
			test.expectedModel, resp.Model)
	}
}

func TestSystemInfoServer_GetPlatformType(t *testing.T) {
	tests := []testCase{
		{
			name: "Success - Mellanox SN4600",
			setupMock: func(mock *mocks.MockPlatformInfoProvider) {
				platformInfo := &hostinfo.PlatformInfo{
					Vendor:     "Mellanox",
					Platform:   "x86_64-mlnx_msn4600c-r0",
					SwitchASIC: "mlnx",
				}
				mock.EXPECT().GetPlatformInfo(gomock.Any()).Return(platformInfo, nil)
				mock.EXPECT().GetPlatformIdentifier(gomock.Any(), platformInfo).Return("mellanox_sn4600", "Mellanox", "sn4600")
			},
			expectedPlatformIdentifier: "mellanox_sn4600",
			expectedVendor:             "Mellanox",
			expectedModel:              "sn4600",
			expectError:                false,
		},
		{
			name: "Success - Arista 7060",
			setupMock: func(mock *mocks.MockPlatformInfoProvider) {
				platformInfo := &hostinfo.PlatformInfo{
					Vendor:   "arista",
					Platform: "x86_64-arista_7060x6_64pe",
				}
				mock.EXPECT().GetPlatformInfo(gomock.Any()).Return(platformInfo, nil)
				mock.EXPECT().GetPlatformIdentifier(gomock.Any(), platformInfo).Return("arista_7060", "arista", "7060")
			},
			expectedPlatformIdentifier: "arista_7060",
			expectedVendor:             "arista",
			expectedModel:              "7060",
			expectError:                false,
		},
		{
			name: "Error getting platform info",
			setupMock: func(mock *mocks.MockPlatformInfoProvider) {
				mock.EXPECT().GetPlatformInfo(gomock.Any()).Return(nil, errors.New("failed to read machine.conf"))
			},
			expectError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Set up the mock controller
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			// Create a mock provider
			mockProvider := mocks.NewMockPlatformInfoProvider(ctrl)

			// Set up the mock expectations
			test.setupMock(mockProvider)

			// Create server with mock provider
			server := NewSystemInfoServerWithProvider(mockProvider)

			// Call the method
			resp, err := server.GetPlatformType(context.Background(), &pb.GetPlatformTypeRequest{})

			// Validate results using helper function
			validateTestResult(t, test, resp, err)
		})
	}
}
