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
	"github.com/openconfig/gnoi/types"
	"github.com/sonic-net/sonic-gnmi/common_utils"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

// Mock interface implementation that returns success!
type mocksysXfmrSuccess struct{}

func (t mocksysXfmrSuccess) resetOptics(req string) (string, error) {
	return "{}", nil
}

// Mock interface implementation that returns error!
type mocksysXfmrFailure struct{}

func (t mocksysXfmrFailure) resetOptics(req string) (string, error) {
	return "", status.Errorf(codes.Internal, fmt.Sprintf("BackEnd returns an error!"))
}

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

func warmbootManagerResponse(t *testing.T, sc *redis.Client, expectedResponse codes.Code, done chan bool, key string) {
	sub := sc.Subscribe(context.Background(), "Reboot_Request_Channel")
	if _, err := sub.Receive(context.Background()); err != nil {
		t.Errorf("nsfManagerResponse failed to subscribe to request channel: %v", err)
		return
	}
	defer sub.Close()
	channel := sub.Channel()

	np, err := common_utils.NewNotificationProducer("Reboot_Response_Channel")
	if err != nil {
		t.Errorf("warmbootManagerResponse failed to create notification producer: %v", err)
		return
	}
	defer np.Close()

	tc := time.After(5 * time.Second)
	select {
	case msg := <-channel:
		t.Logf("warmbootManagerResponse received request: %v", msg)
		// Respond to the request
		if err := np.Send(key, errorCodeToSwss(expectedResponse), map[string]string{}); err != nil {
			t.Errorf("warmbootManagerResponse failed to send response: %v", err)
			return
		}
	case <-done:
		return
	case <-tc:
		t.Error("warmbootManagerResponse timed out waiting for request")
		return
	}
}

func TestSystem(t *testing.T) {
	s := createServer(t)
	go runServer(t, s)
	defer s.Stop(t)

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
	rclient, err := getRedisDBClient(stateDB)
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
	t.Run("RebootFailsWithTimeout", func(t *testing.T) {
		req := &syspb.RebootRequest{
			Delay:   0,
			Message: "Reboot due to ...",
		}

		for _, method := range []syspb.RebootMethod{syspb.RebootMethod_COLD, syspb.RebootMethod_POWERDOWN, syspb.RebootMethod_WARM, syspb.RebootMethod_NSF} {
			req.Method = method
			_, err := sc.Reboot(ctx, req)
			testErr(err, codes.Internal, "Response Notification timeout from NSF Manager!", t)
		}
	})
	t.Run("RebootFailsForInvalidOptics", func(t *testing.T) {
		req := &syspb.RebootRequest{
			Method:  syspb.RebootMethod_COLD,
			Delay:   0,
			Message: "Reset optics due to ...",
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
								"name":      "Ethernet1234",
								"someField": "someValue",
							},
						},
					},
				},
			},
		}

		_, err := sc.Reboot(ctx, req)
		testErr(err, codes.InvalidArgument, "Transceiver is malformed", t)
	})
	t.Run("RebootFailsIfResetOpticsBackEndFails", func(t *testing.T) {
		req := &syspb.RebootRequest{
			Method:  syspb.RebootMethod_COLD,
			Delay:   0,
			Message: "Reset optics due to ...",
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

		sysXfmr = mocksysXfmrFailure{}
		_, err := sc.Reboot(ctx, req)
		testErr(err, codes.Internal, "BackEnd returns an error!", t)
	})
	t.Run("RebootSucceedsIfResetOpticsBackEndSucceeds", func(t *testing.T) {
		req := &syspb.RebootRequest{
			Method:  syspb.RebootMethod_COLD,
			Delay:   0,
			Message: "Reset optics due to ...",
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

		sysXfmr = mocksysXfmrSuccess{}
		_, err := sc.Reboot(ctx, req)
		if err != nil {
			t.Fatal("Expected success, got error: ", err.Error())
		}
	})
	t.Run("RebootSucceeds", func(t *testing.T) {
		// Start goroutine for mock Warmboot Manager to respond to Reboot requests
		done := make(chan bool, 1)
		go warmbootManagerResponse(t, rclient, codes.OK, done, rebootKey)
		defer func() { done <- true }()

		req := &syspb.RebootRequest{
			Delay:   0,
			Message: "Starting NSF reboot ...",
		}
		sysXfmr = mocksysXfmrSuccess{}
		for _, method := range []syspb.RebootMethod{syspb.RebootMethod_COLD, syspb.RebootMethod_POWERDOWN, syspb.RebootMethod_WARM, syspb.RebootMethod_NSF} {
			req.Method = method
			_, err := sc.Reboot(ctx, req)
			if err != nil {
				t.Fatal("Expected success, got error: ", err.Error())
			}
		}		
	})
	t.Run("RebootStatusFailsWithTimeout", func(t *testing.T) {
		_, err := sc.RebootStatus(ctx, &syspb.RebootStatusRequest{})
		testErr(err, codes.Internal, "Response Notification timeout from NSF Manager!", t)
	})
	t.Run("RebootStatusRequestSucceeds", func(t *testing.T) {
		// Start goroutine for mock Warmboot Manager to respond to RebootStatus requests
		done := make(chan bool, 1)
		go warmbootManagerResponse(t, rclient, codes.OK, done, rebootStatusKey)
		defer func() { done <- true }()

		sysXfmr = mocksysXfmrSuccess{}
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
			Message: "Cancelling NSF Reboot due to hardware constraints",
		}
		_, err := sc.CancelReboot(ctx, req)
		testErr(err, codes.Internal, "Response Notification timeout from NSF Manager!", t)
	})
	t.Run("CancelRebootRequestSucceeds", func(t *testing.T) {
		// Start goroutine for mock Warmboot Manager to respond to CancelReboot requests
		done := make(chan bool, 1)
		go warmbootManagerResponse(t, rclient, codes.OK, done, rebootCancelKey)
		defer func() { done <- true }()

		req := &syspb.CancelRebootRequest{
			Message: "Cancelling Warm Reboot due to hardware constraints",
		}
		sysXfmr = mocksysXfmrSuccess{}
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
	t.Run("TimeSucceeds", func(t *testing.T) {
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