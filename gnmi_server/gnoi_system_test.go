package gnmi

import (
	"context"
	"crypto/tls"
	"fmt"
	"reflect"
	"regexp"
	"testing"
	"time"

	"github.com/go-redis/redis"
	commonpb "github.com/openconfig/gnoi/common"
	syspb "github.com/openconfig/gnoi/system"
	typespb "github.com/openconfig/gnoi/types"

	"github.com/sonic-net/sonic-gnmi/common_utils"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/sonic-net/sonic-gnmi/gnmi_server/mocks" // GoMock-generated mocks
	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"
)

func TestSetPackage(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockServer := mocks.NewMockSystem_SetPackageServer(ctrl)

	srv := &Server{
		config: &Config{},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()

	mockServer.EXPECT().Context().Return(ctx).AnyTimes()

	t.Run("SetPackageSuccessDownloadOnly", func(t *testing.T) {
		mockServer.EXPECT().Recv().Return(&syspb.SetPackageRequest{
			Request: &syspb.SetPackageRequest_Package{
				Package: &syspb.Package{
					RemoteDownload: &commonpb.RemoteDownload{
						Path: "http://example.com/package",
					},
					Filename: "/lib/firmware/package.bin",
					Version:  "1.0",
					Activate: false,
				},
			},
		}, nil).Times(1)

		patches := gomonkey.NewPatches()
		defer patches.Reset()

		downloadCalled := false
		installCalled := false

		patches.ApplyMethod(reflect.TypeOf(&ssc.DbusClient{}), "DownloadImage", func(_ *ssc.DbusClient, path, filename string) error {
			downloadCalled = true
			return nil
		})
		patches.ApplyMethod(reflect.TypeOf(&ssc.DbusClient{}), "InstallImage", func(_ *ssc.DbusClient, filename string) error {
			installCalled = true
			return nil
		})

		mockServer.EXPECT().SendAndClose(gomock.Any()).Return(nil).Times(1)

		err := srv.SetPackage(mockServer)
		if err != nil {
			t.Fatalf("SetPackage failed unexpectedly: %v", err)
		}
		if !downloadCalled {
			t.Errorf("Expected DownloadImage to be called, but it was not")
		}
		if installCalled {
			t.Errorf("Expected InstallImage not to be called, but it was")
		}
	})

	t.Run("SetPackageSuccessInstallOnly", func(t *testing.T) {
		mockServer.EXPECT().Recv().Return(&syspb.SetPackageRequest{
			Request: &syspb.SetPackageRequest_Package{
				Package: &syspb.Package{
					Filename: "/lib/firmware/package.bin",
					Version:  "1.0",
					Activate: true,
				},
			},
		}, nil).Times(1)

		patches := gomonkey.NewPatches()
		defer patches.Reset()

		downloadCalled := false
		installCalled := false

		patches.ApplyMethod(reflect.TypeOf(&ssc.DbusClient{}), "DownloadImage", func(_ *ssc.DbusClient, path, filename string) error {
			downloadCalled = true
			return nil
		})
		patches.ApplyMethod(reflect.TypeOf(&ssc.DbusClient{}), "InstallImage", func(_ *ssc.DbusClient, filename string) error {
			installCalled = true
			return nil
		})

		mockServer.EXPECT().SendAndClose(gomock.Any()).Return(nil).Times(1)

		err := srv.SetPackage(mockServer)
		if err != nil {
			t.Fatalf("SetPackage failed unexpectedly: %v", err)
		}
		if downloadCalled {
			t.Errorf("Expected DownloadImage not to be called, but it was")
		}
		if !installCalled {
			t.Errorf("Expected InstallImage to be called, but it was not")
		}
	})

	t.Run("SetPackageSuccessDownloadAndInstall", func(t *testing.T) {
		mockServer.EXPECT().Recv().Return(&syspb.SetPackageRequest{
			Request: &syspb.SetPackageRequest_Package{
				Package: &syspb.Package{
					RemoteDownload: &commonpb.RemoteDownload{
						Path: "http://example.com/package",
					},
					Filename: "/lib/firmware/package.bin",
					Version:  "1.0",
					Activate: true,
				},
			},
		}, nil).Times(1)

		patches := gomonkey.NewPatches()
		defer patches.Reset()

		downloadCalled := false
		installCalled := false

		patches.ApplyMethod(reflect.TypeOf(&ssc.DbusClient{}), "DownloadImage", func(_ *ssc.DbusClient, path, filename string) error {
			downloadCalled = true
			return nil
		})
		patches.ApplyMethod(reflect.TypeOf(&ssc.DbusClient{}), "InstallImage", func(_ *ssc.DbusClient, filename string) error {
			installCalled = true
			return nil
		})

		mockServer.EXPECT().SendAndClose(gomock.Any()).Return(nil).Times(1)

		err := srv.SetPackage(mockServer)
		if err != nil {
			t.Fatalf("SetPackage failed unexpectedly: %v", err)
		}
		if !downloadCalled {
			t.Errorf("Expected DownloadImage to be called, but it was not")
		}
		if !installCalled {
			t.Errorf("Expected InstallImage to be called, but it was not")
		}
	})

	t.Run("SetPackageDownloadFailure", func(t *testing.T) {
		expectedError := status.Errorf(codes.Internal, "failed to download image")

		mockServer.EXPECT().Recv().Return(&syspb.SetPackageRequest{
			Request: &syspb.SetPackageRequest_Package{
				Package: &syspb.Package{
					RemoteDownload: &commonpb.RemoteDownload{
						Path: "http://example.com/package",
					},
					Filename: "/lib/firmware/package.bin",
					Version:  "1.0",
					Activate: false,
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

		mockServer.EXPECT().Recv().Return(&syspb.SetPackageRequest{
			Request: &syspb.SetPackageRequest_Package{
				Package: &syspb.Package{
					Filename: "/lib/firmware/package.bin",
					Version:  "1.0",
					Activate: true,
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
		mockServer.EXPECT().Recv().Return(&syspb.SetPackageRequest{
			Request: &syspb.SetPackageRequest_Package{
				Package: &syspb.Package{
					RemoteDownload: &commonpb.RemoteDownload{
						Path: "http://example.com/package",
					},
					Version:  "1.0",
					Activate: true,
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

	t.Run("SetPackageInvalidPath", func(t *testing.T) {
		mockServer.EXPECT().Recv().Return(&syspb.SetPackageRequest{
			Request: &syspb.SetPackageRequest_Package{
				Package: &syspb.Package{
					RemoteDownload: &commonpb.RemoteDownload{
						Path: "http://malicious.com/package",
					},
					Filename: "/etc/passwd",
					Version:  "1.0",
					Activate: true,
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

	t.Run("SetPackageMissingVersion", func(t *testing.T) {
		mockServer.EXPECT().Recv().Return(&syspb.SetPackageRequest{
			Request: &syspb.SetPackageRequest_Package{
				Package: &syspb.Package{
					RemoteDownload: &commonpb.RemoteDownload{
						Path: "http://example.com/package",
					},
					Filename: "/lib/firmware/package.bin",
					Activate: true,
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

	t.Run("SetPackageRemoteDownloadPathMissing", func(t *testing.T) {
		mockServer.EXPECT().Recv().Return(&syspb.SetPackageRequest{
			Request: &syspb.SetPackageRequest_Package{
				Package: &syspb.Package{
					RemoteDownload: &commonpb.RemoteDownload{},
					Filename:       "/lib/firmware/package.bin",
					Version:        "1.0",
					Activate:       false,
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

		mockServer.EXPECT().Recv().Return(&syspb.SetPackageRequest{
			Request: &syspb.SetPackageRequest_Package{
				Package: &syspb.Package{
					RemoteDownload: &commonpb.RemoteDownload{
						Path: "http://example.com/package",
					},
					Filename: "/lib/firmware/package.bin",
					Version:  "1.0",
					Activate: false,
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

	t.Run("SetPackageInvalidRequestType", func(t *testing.T) {
		mockServer.EXPECT().Recv().Return(&syspb.SetPackageRequest{
			Request: &syspb.SetPackageRequest_Hash{ // Wrong type
				Hash: &typespb.HashType{},
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
}

// testErr is used to test the gNOI API return Code and error string match.
func testErr(err error, code codes.Code, pattern string, t *testing.T) {
	t.Helper()
	if err == nil {
		t.Fatal("Expected error condition.")
	}
	e, _ := status.FromError(err)
	if e.Code() != code {
		t.Error("Error code: expected ", code, ", received ", e.Code())
	}
	res, _ := regexp.MatchString(pattern, e.Message())
	if !res {
		t.Error("Error message: expected ", pattern, ", received ", e.Message())
	}
}

func RebootBackendResponse(t *testing.T, sc *redis.Client, expectedResponse codes.Code, fvs map[string]string, done chan bool, key string) {
	sub := sc.Subscribe("Reboot_Request_Channel")
	if _, err := sub.Receive(); err != nil {
		t.Errorf("RebootBackendResponse failed to subscribe to request channel: %v", err)
		return
	}
	defer sub.Close()
	channel := sub.Channel()

	np, err := common_utils.NewNotificationProducer("Reboot_Response_Channel")
	if err != nil {
		t.Errorf("RebootBackendResponse failed to create notification producer: %v", err)
		return
	}
	defer np.Close()

	tc := time.After(5 * time.Second)
	select {
	case msg := <-channel:
		t.Logf("RebootBackendResponse received request: %v", msg)
		// Respond to the request
		if err := np.Send(key, ErrorCodeToSwss(expectedResponse), fvs); err != nil {
			t.Errorf("RebootBackendResponse failed to send response: %v", err)
			return
		}
	case <-done:
		return
	case <-tc:
		t.Error("RebootBackendResponse timed out waiting for request")
		return
	}
}

func TestSystem(t *testing.T) {
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.Stop()

	targetAddr := fmt.Sprintf("127.0.0.1:%d", s.config.Port)
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}
	conn, err := grpc.Dial(targetAddr, opts...)
	if err != nil {
		t.Fatalf("Dialing to %s failed: %v", targetAddr, err)
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sc := syspb.NewSystemClient(conn)
	rclient, err := common_utils.GetRedisDBClient()
	if err != nil {
		t.Fatalf("Cannot connect to the redis server: %v.", err.Error())
	}
	defer rclient.Close()

	t.Run("RebootFailsIfRebootMethodIsNotSupported", func(t *testing.T) {
		req := &syspb.RebootRequest{
			Delay:   0,
			Message: "Cold reboot due to ...",
		}

		for _, method := range []syspb.RebootMethod{syspb.RebootMethod_UNKNOWN, syspb.RebootMethod_POWERUP} {
			req.Method = method
			_, err := sc.Reboot(ctx, req)
			testErr(err, codes.InvalidArgument, "reboot method is not supported.", t)
		}
	})
	t.Run("RebootFailsIfItIsDelayed", func(t *testing.T) {
		req := &syspb.RebootRequest{
			Method:  syspb.RebootMethod_COLD,
			Delay:   1,
			Message: "Cold reboot due to ...",
		}

		_, err := sc.Reboot(ctx, req)
		testErr(err, codes.InvalidArgument, "reboot is not immediate.", t)
	})
	t.Run("RebootWithSubcomponents", func(t *testing.T) {
		req := &syspb.RebootRequest{
			Method:  syspb.RebootMethod_COLD,
			Delay:   0,
			Message: "Cold reboot starting ...",
			Subcomponents: []*typespb.Path{
				&typespb.Path{
					Origin: "openconfig",
					Elem: []*typespb.PathElem{
						{
							Name: "components",
						},
						{
							Name: "component",
							Key: map[string]string{
								"name": "Ethernet1234",
							},
						},
					},
				},
			},
		}
		_, err := sc.Reboot(ctx, req)
		if err != nil {
			t.Fatal("Expected success, got error: ", err.Error())
		}
	})
	t.Run("RebootFailsWithTimeout", func(t *testing.T) {
		req := &syspb.RebootRequest{
			Delay:   0,
			Message: "Reboot due to ...",
		}

		for _, method := range []syspb.RebootMethod{syspb.RebootMethod_COLD, syspb.RebootMethod_POWERDOWN, syspb.RebootMethod_WARM, syspb.RebootMethod_NSF} {
			req.Method = method
			_, err := sc.Reboot(ctx, req)
			testErr(err, codes.Internal, "Response Notification timeout from Reboot Backend!", t)
		}
	})
	t.Run("RebootFailsWithWrongKey", func(t *testing.T) {
		// Start goroutine for mock Reboot Backend to respond to Reboot requests
		done := make(chan bool, 1)
		fvs := make(map[string]string)
		fvs["MESSAGE"] = "{}"
		req := &syspb.RebootRequest{
			Method:  syspb.RebootMethod_COLD,
			Delay:   0,
			Message: "Cold reboot starting ...",
		}
		go RebootBackendResponse(t, rclient, codes.OK, fvs, done, "testKey")
		defer func() { done <- true }()
		_, err := sc.Reboot(ctx, req)
		testErr(err, codes.Internal, "Op: testKey doesn't match for Reboot!", t)
	})
	t.Run("RebootFailsWithExpectedBackendErrorCodes", func(t *testing.T) {
		for _, code := range []codes.Code{codes.Unavailable, codes.InvalidArgument, codes.DeadlineExceeded, codes.NotFound, codes.AlreadyExists, codes.PermissionDenied, codes.ResourceExhausted, codes.Unimplemented, codes.Internal, codes.FailedPrecondition, codes.Unavailable} {
			fvs := make(map[string]string)
			fvs["MESSAGE"] = "{}"
			// Start goroutine for mock Reboot Backend to respond to Reboot requests
			done := make(chan bool, 1)
			go RebootBackendResponse(t, rclient, code, fvs, done, rebootKey)
			defer func() { done <- true }()
			req := &syspb.RebootRequest{
				Method:  syspb.RebootMethod_COLD,
				Delay:   0,
				Message: "Cold reboot starting ...",
			}
			_, err := sc.Reboot(ctx, req)
			testErr(err, code, "Response Notification returned SWSS Error code", t)
		}
	})
	t.Run("RebootFailsWithUnexpectedBackendErrorCode", func(t *testing.T) {
		fvs := make(map[string]string)
		fvs["MESSAGE"] = "{}"
		// Start goroutine for mock Reboot Backend to respond to Reboot requests
		done := make(chan bool, 1)
		go RebootBackendResponse(t, rclient, codes.Unauthenticated, fvs, done, rebootKey)
		defer func() { done <- true }()
		req := &syspb.RebootRequest{
			Method:  syspb.RebootMethod_COLD,
			Delay:   0,
			Message: "Cold reboot starting ...",
		}
		_, err := sc.Reboot(ctx, req)
		testErr(err, codes.Internal, "Response Notification returned SWSS Error code: Internal", t)
	})
	t.Run("RebootSucceeds", func(t *testing.T) {
		// Start goroutine for mock Reboot Backend to respond to Reboot requests
		done := make(chan bool, 1)
		fvs := make(map[string]string)
		fvs["MESSAGE"] = "{}"
		go RebootBackendResponse(t, rclient, codes.OK, fvs, done, rebootKey)
		defer func() { done <- true }()

		req := &syspb.RebootRequest{
			Method:  syspb.RebootMethod_COLD,
			Delay:   0,
			Message: "Cold reboot starting ...",
		}
		_, err := sc.Reboot(ctx, req)
		if err != nil {
			t.Fatal("Expected success, got error: ", err.Error())
		}
	})
	t.Run("RebootStatusFailsWithTimeout", func(t *testing.T) {
		_, err := sc.RebootStatus(ctx, &syspb.RebootStatusRequest{})
		testErr(err, codes.Internal, "Response Notification timeout from Reboot Backend!", t)
	})
	t.Run("RebootStatusRequestSucceeds", func(t *testing.T) {
		// Start goroutine for mock Reboot Backend to respond to RebootStatus requests
		done := make(chan bool, 1)
		fvs := make(map[string]string)
		fvs["MESSAGE"] = "{\"active\": true, \"method\":\"NSF\",\"status\":{\"status\":\"STATUS_SUCCESS\"}}"
		go RebootBackendResponse(t, rclient, codes.OK, fvs, done, rebootStatusKey)
		defer func() { done <- true }()

		_, err := sc.RebootStatus(ctx, &syspb.RebootStatusRequest{})
		if err != nil {
			t.Fatal("Expected success, got error: ", err.Error())
		}
	})
	t.Run("CancelRebootFailsWithTimeout", func(t *testing.T) {
		req := &syspb.CancelRebootRequest{
			Message: "Cancelling Reboot due to hardware constraints",
		}
		_, err := sc.CancelReboot(ctx, req)
		testErr(err, codes.Internal, "Response Notification timeout from Reboot Backend!", t)
	})
	t.Run("CancelRebootRequestSucceeds", func(t *testing.T) {
		// Start goroutine for mock Reboot Backend to respond to CancelReboot requests
		done := make(chan bool, 1)
		fvs := make(map[string]string)
		fvs["MESSAGE"] = "{}"
		go RebootBackendResponse(t, rclient, codes.OK, fvs, done, rebootCancelKey)
		defer func() { done <- true }()

		req := &syspb.CancelRebootRequest{
			Message: "Cancelling Warm Reboot due to hardware constraints",
		}
		_, err := sc.CancelReboot(ctx, req)
		if err != nil {
			t.Fatal("Expected success, got error: ", err.Error())
		}
	})
	t.Run("PingSucceeds", func(t *testing.T) {
		opt := grpc.EmptyCallOption{}
		_, err := sc.Ping(ctx, &syspb.PingRequest{}, opt)
		if err != nil {
			t.Fatal(err.Error())
		}
	})
	t.Run("TracerouteSucceeds", func(t *testing.T) {
		opt := grpc.EmptyCallOption{}
		_, err := sc.Traceroute(ctx, &syspb.TracerouteRequest{}, opt)
		if err != nil {
			t.Fatal(err.Error())
		}
	})
	t.Run("SetPackageSucceeds", func(t *testing.T) {
		opt := grpc.EmptyCallOption{}
		_, err := sc.SetPackage(ctx, opt)
		if err != nil {
			t.Fatal(err.Error())
		}
	})
	t.Run("SwitchControlProcessorSucceeds", func(t *testing.T) {
		_, err := sc.SwitchControlProcessor(ctx, &syspb.SwitchControlProcessorRequest{})
		if err != nil {
			t.Fatal(err.Error())
		}
	})
	t.Run("SystemTime", func(t *testing.T) {
		resp, err := sc.Time(ctx, &syspb.TimeRequest{})
		if err != nil {
			t.Fatal(err.Error())
		}
		ctime := uint64(time.Now().UnixNano())
		if ctime-resp.Time < 0 || ctime-resp.Time > 1e9 {
			t.Fatalf("Invalid System Time %d", resp.Time)
		}
	})
}
