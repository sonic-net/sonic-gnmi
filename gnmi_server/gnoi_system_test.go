package gnmi

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	gnoi_common_pb "github.com/openconfig/gnoi/common"
	gnoi_system_pb "github.com/openconfig/gnoi/system"
	gnoi_types_pb "github.com/openconfig/gnoi/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/sonic-net/sonic-gnmi/gnmi_server/mocks" // GoMock-generated mocks
	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"
)

func TestSetPackage(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockServer := mocks.NewMockSystem_SetPackageServer(ctrl)

	srv := &SystemServer{
		Server: &Server{
			config: &Config{},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()

	mockServer.EXPECT().Context().Return(ctx).AnyTimes()

	t.Run("SetPackageSuccess", func(t *testing.T) {
		mockServer.EXPECT().Recv().Return(&gnoi_system_pb.SetPackageRequest{
			Request: &gnoi_system_pb.SetPackageRequest_Package{
				Package: &gnoi_system_pb.Package{
					RemoteDownload: &gnoi_common_pb.RemoteDownload{
						Path: "http://example.com/package",
					},
					Filename: "package.bin",
				},
			},
		}, nil).Times(1)

		patches := gomonkey.NewPatches()
		defer patches.Reset()

		patches.ApplyMethod(reflect.TypeOf(&ssc.DbusClient{}), "DownloadImage", func(_ *ssc.DbusClient, path, filename string) error {
			return nil
		})
		patches.ApplyMethod(reflect.TypeOf(&ssc.DbusClient{}), "InstallImage", func(_ *ssc.DbusClient, filename string) error {
			return nil
		})

		mockServer.EXPECT().SendAndClose(gomock.Any()).Return(nil).Times(1)

		err := srv.SetPackage(mockServer)
		if err != nil {
			t.Fatalf("SetPackage failed unexpectedly: %v", err)
		}
	})

	t.Run("SetPackageDownloadFailure", func(t *testing.T) {
		expectedError := status.Errorf(codes.Internal, "failed to download image")

		mockServer.EXPECT().Recv().Return(&gnoi_system_pb.SetPackageRequest{
			Request: &gnoi_system_pb.SetPackageRequest_Package{
				Package: &gnoi_system_pb.Package{
					RemoteDownload: &gnoi_common_pb.RemoteDownload{
						Path: "http://example.com/package",
					},
					Filename: "package.bin",
				},
			},
		}, nil).Times(1)

		patches := gomonkey.NewPatches()
		defer patches.Reset()

		patches.ApplyMethod(reflect.TypeOf(&ssc.DbusClient{}), "DownloadImage", func(_ *ssc.DbusClient, path, filename string) error {
			return expectedError
		})

		err := srv.SetPackage(mockServer)
		if err == nil {
			t.Fatalf("Expected error but got none")
		}
		st, ok := status.FromError(err)
		if !ok {
			t.Fatalf("Expected gRPC status error, but got %v", err)
		}
		if st.Code() != status.Code(expectedError) {
			t.Errorf("Expected error code '%v', but got '%v'", status.Code(expectedError), st.Code())
		}
	})

	t.Run("SetPackageInstallFailure", func(t *testing.T) {
		expectedError := status.Errorf(codes.Internal, "failed to install image")

		mockServer.EXPECT().Recv().Return(&gnoi_system_pb.SetPackageRequest{
			Request: &gnoi_system_pb.SetPackageRequest_Package{
				Package: &gnoi_system_pb.Package{
					RemoteDownload: &gnoi_common_pb.RemoteDownload{
						Path: "http://example.com/package",
					},
					Filename: "package.bin",
				},
			},
		}, nil).Times(1)

		patches := gomonkey.NewPatches()
		defer patches.Reset()

		patches.ApplyMethod(reflect.TypeOf(&ssc.DbusClient{}), "DownloadImage", func(_ *ssc.DbusClient, path, filename string) error {
			return nil
		})
		patches.ApplyMethod(reflect.TypeOf(&ssc.DbusClient{}), "InstallImage", func(_ *ssc.DbusClient, filename string) error {
			return expectedError
		})

		err := srv.SetPackage(mockServer)
		if err == nil {
			t.Fatalf("Expected error but got none")
		}
		st, ok := status.FromError(err)
		if !ok {
			t.Fatalf("Expected gRPC status error, but got %v", err)
		}
		if st.Code() != status.Code(expectedError) {
			t.Errorf("Expected error code '%v', but got '%v'", status.Code(expectedError), st.Code())
		}
	})

	t.Run("SetPackageMissingFilename", func(t *testing.T) {
		mockServer.EXPECT().Recv().Return(&gnoi_system_pb.SetPackageRequest{
			Request: &gnoi_system_pb.SetPackageRequest_Package{
				Package: &gnoi_system_pb.Package{
					RemoteDownload: &gnoi_common_pb.RemoteDownload{
						Path: "http://example.com/package",
					},
				},
			},
		}, nil).Times(1)
		err := srv.SetPackage(mockServer)
		if err == nil {
			t.Fatalf("Expected error but got none")
		}
		expectedErrorCode := codes.InvalidArgument
		st, ok := status.FromError(err)
		if !ok {
			t.Fatalf("Expected gRPC status error, but got %v", err)
		}
		if st.Code() != expectedErrorCode {
			t.Errorf("Expected error code '%v', but got '%v'", expectedErrorCode, st.Code())
		}
	})

	t.Run("SetPackageRemoteDownloadInfoMissing", func(t *testing.T) {
		mockServer.EXPECT().Recv().Return(&gnoi_system_pb.SetPackageRequest{
			Request: &gnoi_system_pb.SetPackageRequest_Package{
				Package: &gnoi_system_pb.Package{
					Filename: "package.bin",
				},
			},
		}, nil).Times(1)

		err := srv.SetPackage(mockServer)
		if err == nil {
			t.Fatalf("Expected error but got none")
		}
		expectedErrorCode := codes.InvalidArgument
		st, ok := status.FromError(err)
		if !ok {
			t.Fatalf("Expected gRPC status error, but got %v", err)
		}
		if st.Code() != expectedErrorCode {
			t.Errorf("Expected error code '%v', but got '%v'", expectedErrorCode, st.Code())
		}
	})

	t.Run("SetPackageFailToReceiveRequest", func(t *testing.T) {
		expectedError := status.Errorf(codes.InvalidArgument, "failed to receive package request")

		mockServer.EXPECT().Recv().Return(nil, expectedError).Times(1)

		err := srv.SetPackage(mockServer)
		if err == nil {
			t.Fatalf("Expected error but got none")
		}
		st, ok := status.FromError(err)
		if !ok {
			t.Fatalf("Expected gRPC status error, but got %v", err)
		}
		if st.Code() != status.Code(expectedError) {
			t.Errorf("Expected error code '%v', but got '%v'", status.Code(expectedError), st.Code())
		}
	})

	t.Run("SetPackageSendAndCloseFailure", func(t *testing.T) {
		expectedError := status.Errorf(codes.Internal, "failed to send response")

		mockServer.EXPECT().Recv().Return(&gnoi_system_pb.SetPackageRequest{
			Request: &gnoi_system_pb.SetPackageRequest_Package{
				Package: &gnoi_system_pb.Package{
					RemoteDownload: &gnoi_common_pb.RemoteDownload{
						Path: "http://example.com/package",
					},
					Filename: "package.bin",
				},
			},
		}, nil).Times(1)

		patches := gomonkey.NewPatches()
		defer patches.Reset()

		patches.ApplyMethod(reflect.TypeOf(&ssc.DbusClient{}), "DownloadImage", func(_ *ssc.DbusClient, path, filename string) error {
			return nil
		})
		patches.ApplyMethod(reflect.TypeOf(&ssc.DbusClient{}), "InstallImage", func(_ *ssc.DbusClient, filename string) error {
			return nil
		})

		mockServer.EXPECT().SendAndClose(gomock.Any()).Return(expectedError).Times(1)

		err := srv.SetPackage(mockServer)
		if err == nil {
			t.Fatalf("Expected error but got none")
		}
		st, ok := status.FromError(err)
		if !ok {
			t.Fatalf("Expected gRPC status error, but got %v", err)
		}
		if st.Code() != status.Code(expectedError) {
			t.Errorf("Expected error code '%v', but got '%v'", status.Code(expectedError), st.Code())
		}
	})

	t.Run("RecvFails", func(t *testing.T) {
		expectedError := status.Errorf(codes.Internal, "Recv() failed")

		mockServer.EXPECT().Recv().Return(nil, expectedError).Times(1)

		err := srv.SetPackage(mockServer)
		if err == nil {
			t.Fatalf("Expected error but got none")
		}
		st, ok := status.FromError(err)
		if !ok {
			t.Fatalf("Expected gRPC status error, but got %v", err)
		}
		if st.Code() != status.Code(expectedError) {
			t.Errorf("Expected error code '%v', but got '%v'", status.Code(expectedError), st.Code())
		}
	})

	t.Run("InvalidRequestType", func(t *testing.T) {
		mockServer.EXPECT().Recv().Return(&gnoi_system_pb.SetPackageRequest{
			Request: &gnoi_system_pb.SetPackageRequest_Hash{ // Wrong type
				Hash: &gnoi_types_pb.HashType{},
			},
		}, nil).Times(1)

		err := srv.SetPackage(mockServer)
		if err == nil {
			t.Fatalf("Expected error but got none")
		}
		expectedErrorCode := codes.InvalidArgument
		st, ok := status.FromError(err)
		if !ok {
			t.Fatalf("Expected gRPC status error, but got %v", err)
		}
		if st.Code() != expectedErrorCode {
			t.Errorf("Expected error code '%v', but got '%v'", expectedErrorCode, st.Code())
		}
	})

	t.Run("SendAndCloseFails", func(t *testing.T) {
		expectedError := status.Errorf(codes.Internal, "SendAndClose() failed")

		mockServer.EXPECT().Recv().Return(&gnoi_system_pb.SetPackageRequest{
			Request: &gnoi_system_pb.SetPackageRequest_Package{
				Package: &gnoi_system_pb.Package{
					RemoteDownload: &gnoi_common_pb.RemoteDownload{
						Path: "http://example.com/package",
					},
					Filename: "package.bin",
				},
			},
		}, nil).Times(1)

		patches := gomonkey.NewPatches()
		defer patches.Reset()

		patches.ApplyMethod(reflect.TypeOf(&ssc.DbusClient{}), "DownloadImage", func(_ *ssc.DbusClient, path, filename string) error {
			return nil
		})
		patches.ApplyMethod(reflect.TypeOf(&ssc.DbusClient{}), "InstallImage", func(_ *ssc.DbusClient, filename string) error {
			return nil
		})

		mockServer.EXPECT().SendAndClose(gomock.Any()).Return(expectedError).Times(1)

		err := srv.SetPackage(mockServer)
		if err == nil {
			t.Fatalf("Expected error but got none")
		}
		st, ok := status.FromError(err)
		if !ok {
			t.Fatalf("Expected gRPC status error, but got %v", err)
		}
		if st.Code() != status.Code(expectedError) {
			t.Errorf("Expected error code '%v', but got '%v'", status.Code(expectedError), st.Code())
		}
	})
}
