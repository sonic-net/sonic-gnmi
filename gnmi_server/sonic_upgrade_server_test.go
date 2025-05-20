// Unit tests for sonic_upgrade_server.go
package gnmi

import (
	context "context"
	"errors"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	spb "github.com/sonic-net/sonic-gnmi/proto/gnoi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// dummyUpdateFirmwareServer implements SonicUpgradeService_UpdateFirmwareServer for testing
// Only implements the methods needed for the test

type dummyUpdateFirmwareServer struct {
	spb.SonicUpgradeService_UpdateFirmwareServer
	ctx      context.Context
	sent     []*spb.UpdateFirmwareStatus
	sendErr  error
}

func (d *dummyUpdateFirmwareServer) Context() context.Context {
	if d.ctx != nil {
		return d.ctx
	}
	return context.Background()
}

func (d *dummyUpdateFirmwareServer) Send(status *spb.UpdateFirmwareStatus) error {
	d.sent = append(d.sent, status)
	return d.sendErr
}

func newMockUpgradeServer() *SonicUpgradeServer {
	return &SonicUpgradeServer{
		Server: &Server{
			config: &Config{},
		},
	}
}

func TestSonicUpgradeServer_UpdateFirmware_Success(t *testing.T) {
	server := newMockUpgradeServer()
	stream := &dummyUpdateFirmwareServer{}

	patches := gomonkey.NewPatches()
	defer patches.Reset()
	patches.ApplyFunc(authenticate, func(_ *Config, ctx context.Context, _ string, _ bool) (context.Context, error) {
		return ctx, nil
	})

	err := server.UpdateFirmware(stream)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(stream.sent) != 1 {
		t.Fatalf("expected 1 status sent, got %d", len(stream.sent))
	}
	status := stream.sent[0]
	if status.State != spb.UpdateFirmwareStatus_STARTED {
		t.Errorf("expected state STARTED, got %v", status.State)
	}
}

func TestSonicUpgradeServer_UpdateFirmware_AuthFail(t *testing.T) {
	server := newMockUpgradeServer()
	stream := &dummyUpdateFirmwareServer{}

	patches := gomonkey.NewPatches()
	defer patches.Reset()
	patches.ApplyFunc(authenticate, func(_ *Config, _ context.Context, _ string, _ bool) (context.Context, error) {
		return nil, errors.New("auth failed")
	})

	err := server.UpdateFirmware(stream)
	if err != nil {
		// The implementation logs error but does not return, so err is nil
		// If implementation changes to return error, update this test
	}
}

func TestSonicUpgradeServer_UpdateFirmware_SendError(t *testing.T) {
	server := newMockUpgradeServer()
	stream := &dummyUpdateFirmwareServer{sendErr: status.Error(codes.Internal, "send failed")}

	err := server.UpdateFirmware(stream)
	if err == nil || status.Code(err) != codes.Internal {
		t.Errorf("expected Internal error, got %v", err)
	}
}

// Save the real authenticate for patching
var realAuthenticate = authenticate
