package show_client

import (
	"encoding/json"
	log "github.com/golang/glog"
	sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
)

type IPv6BGPSummaryResponse struct {
	IPv6Unicast IPv6Unicast `json:"ipv6Unicast"`
}

type IPv6Unicast struct {
	RouterID        string          `json:"routerId"`
	LocalAS         int             `json:"as"`
	VRFId           int             `json:"vrfId"`
	TableVersion    int             `json:"tableVersion"`
	RibCount        int             `json:"ribCount"`
	RibMemory       int             `json:"ribMemory"`
	PeerCount       int             `json:"peerCount"`
	PeerMemory      int             `json:"peerMemory"`
	PeerGroupCount  int             `json:"peerGroupCount"`
	PeerGroupMemory int             `json:"peerGroupMemory"`
	Peers           map[string]Peer `json:"peers"`
}

type Peer struct {
	Version      int    `json:"version"`
	RemoteAS     int    `json:"remoteAs"`
	MsgRcvd      int    `json:"msgRcvd"`
	MsgSent      int    `json:"msgSent"`
	TableVersion int    `json:"tableVersion"`
	InQ          int    `json:"inq"`
	OutQ         int    `json:"outq"`
	UpDown       string `json:"peerUptime"`
	State        string `json:"state"`
	PfxRcd       int    `json:"pfxRcd"`
	NeighborName string
}

var (
	vtyshBGPIPv6SummaryCommand = "vtysh -c \"show bgp ipv6 summary json\""
)

func getIPv6BGPSummary(options sdc.OptionMap) ([]byte, error) {
	// Get data from vtysh command
	vtyshOutput, err := GetDataFromHostCommand(vtyshBGPIPv6SummaryCommand)
	if err != nil {
		log.Errorf("Unable to succesfully execute command %v, get err %v", vtyshBGPIPv6SummaryCommand, err)
		return nil, err
	}
	var vtyshResponse IPv6BGPSummaryResponse
	if err := json.Unmarshal([]byte(vtyshOutput), &vtyshResponse); err != nil {
		log.Errorf("Unable to create response from vtysh output %v", err)
		return nil, err
	}

	// Fetch neighbor name from CONFIG DB
	queries := [][]string{
		{"CONFIG_DB", "BGP_NEIGHBOR"},
	}

	bgpNeighborTableOutput, err := GetMapFromQueries(queries)
	if err != nil {
		log.Errorf("Unable to pull data for queries %v, got err %v", queries, err)
		return nil, err
	}

	// Modify vtysh data to use neighbor name from CONFIG DB
	for ip, peer := range vtyshResponse.IPv6Unicast.Peers {
		// If unable to find name in CONFIG_DB/BGP_NEIGHBOR using show command default of NotAvailable
		neighborName := "NotAvailable"
		if neighbor, found := bgpNeighborTableOutput[ip]; found {
			if entry, ok := neighbor.(map[string]interface{}); ok {
				if name, exists := entry["name"]; exists {
					if nameVal, ok := name.(string); ok {
						neighborName = nameVal
					}
				}
			}
		}
		peer.NeighborName = neighborName
		vtyshResponse.IPv6Unicast.Peers[ip] = peer
	}

	ipv6BGPSummaryJSON, err := json.Marshal(vtyshResponse)
	if err != nil {
		log.Errorf("Unable to create json data from modified vtysh response %v, got err %v", vtyshResponse, err)
		return nil, err
	}
	return ipv6BGPSummaryJSON, nil
}
