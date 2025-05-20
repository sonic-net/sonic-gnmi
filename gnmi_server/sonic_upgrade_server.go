package gnmi

import (
	log "github.com/golang/glog"
	spb "github.com/sonic-net/sonic-gnmi/proto/gnoi"
)

// SonicUpgradeServer implements the gNOI SonicUpgradeServiceServer interface for handling firmware upgrades.
//
// This server provides methods to manage firmware upgrades on SONiC devices via the gNOI protocol.

// UpdateFirmware handles the firmware upgrade process for SONiC devices.
//
// This method authenticates the request and streams status updates to the client.
// Currently, it sends a STARTED status as a dummy implementation.
func (s *SonicUpgradeServer) UpdateFirmware(stream spb.SonicUpgradeService_UpdateFirmwareServer) error {
	ctx := stream.Context()
	_, err := authenticate(s.config, ctx, "gnoi", true)
	if err != nil {
		log.Errorf("Failed to authenticate: %v", err)
	}

	log.V(1).Info("gNOI: Sonic UpdateFirmware")

	// Dummy implementation: send a STARTED status and close.
	status := &spb.UpdateFirmwareStatus{
		LogLine: "Firmware update started (dummy implementation)",
		State:   spb.UpdateFirmwareStatus_STARTED,
	}
	if err := stream.Send(status); err != nil {
		return err
	}
	return nil
}
