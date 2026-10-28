package gnmi

import (
	"context"
	"crypto/tls"
	"fmt"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	spb "github.com/sonic-net/sonic-gnmi/proto/gnoi"
	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

// dialSonicClient brings up an in-process gNMI server, dials it over TLS, and
// returns a SonicService client plus a cleanup func.
func dialSonicClient(t *testing.T, port int64) (spb.SonicServiceClient, func()) {
	t.Helper()
	s := createServer(t, port)
	go runServer(t, s)

	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}
	targetAddr := fmt.Sprintf("127.0.0.1:%d", port)
	conn, err := grpc.Dial(targetAddr, opts...)
	if err != nil {
		s.Stop()
		t.Fatalf("Dialing to %s failed: %v", targetAddr, err)
	}
	cleanup := func() {
		conn.Close()
		s.Stop()
	}
	return spb.NewSonicServiceClient(conn), cleanup
}

func TestGnoiSonicConfigSave(t *testing.T) {
	c, cleanup := dialSonicClient(t, 8081)
	defer cleanup()

	t.Run("Success", func(t *testing.T) {
		patch1 := gomonkey.ApplyFuncReturn(ssc.NewDbusClient, &ssc.DbusClient{}, nil)
		patch2 := gomonkey.ApplyFuncReturn(ssc.DbusApi, "", nil)
		defer patch1.Reset()
		defer patch2.Reset()

		resp, err := c.ConfigSave(context.Background(), &spb.ConfigSaveRequest{
			Input: &spb.ConfigSaveRequest_Input{},
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if resp == nil || resp.GetOutput() == nil {
			t.Fatalf("Expected non-nil response with Output, got %#v", resp)
		}
	})

	t.Run("DbusClientError", func(t *testing.T) {
		patch := gomonkey.ApplyFuncReturn(ssc.NewDbusClient, nil, fmt.Errorf("client error"))
		defer patch.Reset()

		_, err := c.ConfigSave(context.Background(), &spb.ConfigSaveRequest{
			Input: &spb.ConfigSaveRequest_Input{},
		})
		if status.Code(err) != codes.Internal {
			t.Fatalf("Expected Internal, got %v (%v)", status.Code(err), err)
		}
	})

	t.Run("DbusCallError", func(t *testing.T) {
		patch1 := gomonkey.ApplyFuncReturn(ssc.NewDbusClient, &ssc.DbusClient{}, nil)
		patch2 := gomonkey.ApplyFuncReturn(ssc.DbusApi, "", fmt.Errorf("save failed"))
		defer patch1.Reset()
		defer patch2.Reset()

		_, err := c.ConfigSave(context.Background(), &spb.ConfigSaveRequest{
			Input: &spb.ConfigSaveRequest_Input{},
		})
		if status.Code(err) != codes.Internal {
			t.Fatalf("Expected Internal, got %v (%v)", status.Code(err), err)
		}
	})
}

func TestGnoiSonicConfigReload(t *testing.T) {
	c, cleanup := dialSonicClient(t, 8082)
	defer cleanup()

	t.Run("SuccessFromStartupFile", func(t *testing.T) {
		patch1 := gomonkey.ApplyFuncReturn(ssc.NewDbusClient, &ssc.DbusClient{}, nil)
		patch2 := gomonkey.ApplyFuncReturn(ssc.DbusApi, "", nil)
		defer patch1.Reset()
		defer patch2.Reset()

		resp, err := c.ConfigReload(context.Background(), &spb.ConfigReloadRequest{
			Input: &spb.ConfigReloadRequest_Input{},
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if resp == nil || resp.GetOutput() == nil {
			t.Fatalf("Expected non-nil response with Output, got %#v", resp)
		}
	})

	t.Run("SuccessInlineJson", func(t *testing.T) {
		patch1 := gomonkey.ApplyFuncReturn(ssc.NewDbusClient, &ssc.DbusClient{}, nil)
		patch2 := gomonkey.ApplyFuncReturn(ssc.DbusApi, "", nil)
		defer patch1.Reset()
		defer patch2.Reset()

		resp, err := c.ConfigReload(context.Background(), &spb.ConfigReloadRequest{
			Input: &spb.ConfigReloadRequest_Input{ConfigJson: `{"FOO": {}}`},
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if resp == nil || resp.GetOutput() == nil {
			t.Fatalf("Expected non-nil response with Output, got %#v", resp)
		}
	})

	t.Run("InvalidInlineJson", func(t *testing.T) {
		_, err := c.ConfigReload(context.Background(), &spb.ConfigReloadRequest{
			Input: &spb.ConfigReloadRequest_Input{ConfigJson: "{not-json"},
		})
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("Expected InvalidArgument, got %v (%v)", status.Code(err), err)
		}
	})

	t.Run("DbusClientError", func(t *testing.T) {
		patch := gomonkey.ApplyFuncReturn(ssc.NewDbusClient, nil, fmt.Errorf("client error"))
		defer patch.Reset()

		_, err := c.ConfigReload(context.Background(), &spb.ConfigReloadRequest{
			Input: &spb.ConfigReloadRequest_Input{},
		})
		if status.Code(err) != codes.Internal {
			t.Fatalf("Expected Internal, got %v (%v)", status.Code(err), err)
		}
	})

	t.Run("DbusCallError", func(t *testing.T) {
		patch1 := gomonkey.ApplyFuncReturn(ssc.NewDbusClient, &ssc.DbusClient{}, nil)
		patch2 := gomonkey.ApplyFuncReturn(ssc.DbusApi, "", fmt.Errorf("reload failed"))
		defer patch1.Reset()
		defer patch2.Reset()

		_, err := c.ConfigReload(context.Background(), &spb.ConfigReloadRequest{
			Input: &spb.ConfigReloadRequest_Input{},
		})
		if status.Code(err) != codes.Internal {
			t.Fatalf("Expected Internal, got %v (%v)", status.Code(err), err)
		}
	})
}
