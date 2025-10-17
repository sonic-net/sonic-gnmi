package gnmi

import (
	log "github.com/golang/glog"
	gnoi_debug "github.com/sonic-net/sonic-gnmi/pkg/gnoi/debug"
	gnoi_debug_pb "github.com/sonic-net/sonic-gnmi/proto/gnoi/debug"
)

func (srv *DebugServer) Debug(req *gnoi_debug_pb.DebugRequest, stream gnoi_debug_pb.Debug_DebugServer) error {
	log.Infof("gNOI Debug RPC called with request: %+v", req)

	_, readAccessErr := authenticate(srv.config, stream.Context(), "gnoi", false)
	if readAccessErr != nil {
		// User cannot do anything, abort
		log.Errorf("authentication failed in Debug RPC: %v", readAccessErr)
		return readAccessErr
	}

	_, writeAccessErr := authenticate(srv.config, stream.Context(), "gnoi", true)
	if writeAccessErr != nil {
		// User has read-only access
		return gnoi_debug.HandleCommandRequest(req, stream, srv.readWhitelist)
	}

	// Otherwise, this user has write access
	return gnoi_debug.HandleCommandRequest(req, stream, srv.writeWhitelist)
}

// Helper function to construct lists of allowed commands for read and write users.
// Commands have been chosen based on what is found in existing TSGs, and are
// conservatively marked as write if there is any chance of impact on a running device.
func constructWhitelists() (read, write []string) {
	readCommands := []string{
		"ls",
		"cat",
		"echo",
		"ping",
		"grep",
		"dmesg",
		"zgrep",
		"tail",
		"teamshow",
		"ps",
		"uptime",
		"awk",
		"xargs",
		"show",
		"TSC",
	}

	writeCommands := []string{
		"docker",
		"reboot",
		"mv",
		"systemctl",
		"ip",
		"ifconfig",
		"TSA",
		"TSB",
		"sonic-installer",
		"sonic_installer",
		"config",
		"tcpdump",
		"acl-loader",
		"counterpoll",
		"bcmcmd",
		"redis-cli",
		"sonic-db-cli",
		"sonic-clear",
		"wc",
		"portstat",
		"pfcwd",
		"lspci",
		"pcieutil",
		"include",
		"vtysh",
	}
	writeCommands = append(writeCommands, readCommands...)

	return readCommands, writeCommands
}
