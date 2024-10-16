package gnmi

import (
	"context"
	"crypto/tls"
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/go-redis/redis"
	syspb "github.com/openconfig/gnoi/system"
	"github.com/sonic-net/sonic-gnmi/common_utils"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

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

func errorCodeToSwss(errCode codes.Code) string {
	switch errCode {
	case codes.OK:
		return "SWSS_RC_SUCCESS"
	case codes.Unknown:
		return "SWSS_RC_UNKNOWN"
	case codes.InvalidArgument:
		return "SWSS_RC_INVALID_PARAM"
	case codes.DeadlineExceeded:
		return "SWSS_RC_DEADLINE_EXCEEDED"
	case codes.NotFound:
		return "SWSS_RC_NOT_FOUND"
	case codes.AlreadyExists:
		return "SWSS_RC_EXISTS"
	case codes.PermissionDenied:
		return "SWSS_RC_PERMISSION_DENIED"
	case codes.ResourceExhausted:
		return "SWSS_RC_FULL"
	case codes.Unimplemented:
		return "SWSS_RC_UNIMPLEMENTED"
	case codes.Internal:
		return "SWSS_RC_INTERNAL"
	case codes.FailedPrecondition:
		return "SWSS_RC_FAILED_PRECONDITION"
	case codes.Unavailable:
		return "SWSS_RC_UNAVAIL"
	}
	return ""
}

func rebootBackendResponse(t *testing.T, sc *redis.Client, expectedResponse codes.Code, fvs map[string]string, done chan bool, key string) {
	sub := sc.Subscribe("Reboot_Request_Channel")
	if _, err := sub.Receive(); err != nil {
		t.Errorf("rebootBackendResponse failed to subscribe to request channel: %v", err)
		return
	}
	defer sub.Close()
	channel := sub.Channel()

	np, err := common_utils.NewNotificationProducer("Reboot_Response_Channel")
	if err != nil {
		t.Errorf("rebootBackendResponse failed to create notification producer: %v", err)
		return
	}
	defer np.Close()

	tc := time.After(5 * time.Second)
	select {
	case msg := <-channel:
		t.Logf("rebootBackendResponse received request: %v", msg)
		// Respond to the request
		if err := np.Send(key, errorCodeToSwss(expectedResponse), fvs); err != nil {
			t.Errorf("rebootBackendResponse failed to send response: %v", err)
			return
		}
	case <-done:
		return
	case <-tc:
		t.Error("rebootBackendResponse timed out waiting for request")
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

		for _, method := range []syspb.RebootMethod{syspb.RebootMethod_UNKNOWN, syspb.RebootMethod_HALT, syspb.RebootMethod_POWERUP} {
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
	t.Run("RebootFailsIfMessageIsNotSet", func(t *testing.T) {
		req := &syspb.RebootRequest{
			Method: syspb.RebootMethod_COLD,
			Delay:  0,
		}

		_, err := sc.Reboot(ctx, req)
		testErr(err, codes.InvalidArgument, "message is empty.", t)
	})
	t.Run("RebootWithSubcomponents", func(t *testing.T) {
		req := &syspb.RebootRequest{
			Method:  syspb.RebootMethod_COLD,
			Delay:   0,
			Message: "Cold reboot starting ...",
			Subcomponents: []*types.Path{
				&types.Path{
					Origin: "openconfig",
					Elem: []*types.PathElem{
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
		go rebootBackendResponse(t, rclient, codes.OK, fvs, done, "testKey")
		defer func() { done <- true }()
		_, err := sc.Reboot(ctx, req)
		testErr(err, codes.Internal, "Unsupported notification key for Reboot APIs", t)
	})
	t.Run("RebootFailsWithBackendErrorCode", func(t *testing.T) {
		// Start goroutine for mock Reboot Backend to respond to Reboot requests
		done := make(chan bool, 1)
		fvs := make(map[string]string)
		fvs["MESSAGE"] = "{}"
		req := &syspb.RebootRequest{
			Method:  syspb.RebootMethod_COLD,
			Delay:   0,
			Message: "Cold reboot starting ...",
		}
		for _, code := range []codes.Code{codes.Unknown, codes.InvalidArgument, codes.DeadlineExceeded, codes.NotFound, codes.AlreadyExists, codes.PermissionDenied, codes.ResourceExhausted, codes.Unimplemented, codes.Internal, codes.FailedPrecondition, codes.Unavailable} {
			go rebootBackendResponse(t, rclient, code, fvs, done, rebootKey)
			defer func() { done <- true }()
			_, err := sc.Reboot(ctx, req)
			testErr(err, code, "Response Notification returned SWSS Error code", t)
		}
	})
	t.Run("RebootSucceeds", func(t *testing.T) {
		// Start goroutine for mock Reboot Backend to respond to Reboot requests
		done := make(chan bool, 1)
		fvs := make(map[string]string)
		fvs["MESSAGE"] = "{}"
		go rebootBackendResponse(t, rclient, codes.OK, fvs, done, rebootKey)
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
		go rebootBackendResponse(t, rclient, codes.OK, fvs, done, rebootStatusKey)
		defer func() { done <- true }()

		_, err := sc.RebootStatus(ctx, &syspb.RebootStatusRequest{})
		if err != nil {
			t.Fatal("Expected success, got error: ", err.Error())
		}
	})
	t.Run("CancelRebootFailsWithEmptyMessage", func(t *testing.T) {
		_, err := sc.CancelReboot(ctx, &syspb.CancelRebootRequest{})
		testErr(err, codes.Internal, "Invalid CancelReboot request: message is empty.", t)
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
		go rebootBackendResponse(t, rclient, codes.OK, fvs, done, rebootCancelKey)
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