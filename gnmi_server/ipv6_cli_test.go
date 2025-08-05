package gnmi

// ipv6_cli_test.go

// Tests SHOW ipv6 bgp summary

import (
	"crypto/tls"
	"testing"
	"time"

	pb "github.com/openconfig/gnmi/proto/gnmi"

	"github.com/agiledragon/gomonkey/v2"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
)

func TestGetIPv6BGPSummary(t *testing.T) {
	s := createServer(t, ServerPort)
	go runServer(t, s)
	defer s.ForceStop()
	defer ResetDataSetsAndMappings(t)

	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}

	conn, err := grpc.Dial(TargetAddr, opts...)
	if err != nil {
		t.Fatalf("Dialing to %q failed: %v", TargetAddr, err)
	}
	defer conn.Close()

	gClient := pb.NewGNMIClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), QueryTimeout*time.Second)
	defer cancel()

	bgpNeighborFileName := "../testdata/BGP_NEIGHBOR.txt"
	ipv6BGPSummaryDefault := `{"ipv6Unicast":{"routerId":"00.00.0.00","as":64601,"vrfId":0,"tableVersion":19203,"ribCount":12807,"ribMemory":1639296,"peerCount":4,"peerMemory":96288,"peerGroupCount":4,"peerGroupMemory":256,"peers":{"aa00::12":{"version":4,"remoteAs":64802,"msgRcvd":9189,"msgSent":9192,"tableVersion":19203,"inq":0,"outq":0,"peerUptime":"4d03h44m","state":"Established","pfxRcd":6400,"NeighborName":"ARISTA03T1"},"aa00::1a":{"version":4,"remoteAs":64802,"msgRcvd":9189,"msgSent":9192,"tableVersion":19203,"inq":0,"outq":0,"peerUptime":"4d03h44m","state":"Established","pfxRcd":6400,"NeighborName":"ARISTA04T1"},"aa00::1":{"version":4,"remoteAs":64802,"msgRcvd":9191,"msgSent":9195,"tableVersion":19203,"inq":0,"outq":0,"peerUptime":"4d03h44m","state":"Established","pfxRcd":6400,"NeighborName":"ARISTA01T1"},"aa00::a":{"version":4,"remoteAs":64802,"msgRcvd":9189,"msgSent":9193,"tableVersion":19203,"inq":0,"outq":0,"peerUptime":"4d03h44m","state":"Established","pfxRcd":6400,"NeighborName":"ARISTA02T1"}}}}`
	ipv6BGPSummaryNoNeighborName := `{"ipv6Unicast":{"routerId":"00.00.0.00","as":64601,"vrfId":0,"tableVersion":19203,"ribCount":12807,"ribMemory":1639296,"peerCount":4,"peerMemory":96288,"peerGroupCount":4,"peerGroupMemory":256,"peers":{"aa00::12":{"version":4,"remoteAs":64802,"msgRcvd":9189,"msgSent":9192,"tableVersion":19203,"inq":0,"outq":0,"peerUptime":"4d03h44m","state":"Established","pfxRcd":6400,"NeighborName":"NotAvailable"},"aa00::1a":{"version":4,"remoteAs":64802,"msgRcvd":9189,"msgSent":9192,"tableVersion":19203,"inq":0,"outq":0,"peerUptime":"4d03h44m","state":"Established","pfxRcd":6400,"NeighborName":"NotAvailable"},"aa00::1":{"version":4,"remoteAs":64802,"msgRcvd":9191,"msgSent":9195,"tableVersion":19203,"inq":0,"outq":0,"peerUptime":"4d03h44m","state":"Established","pfxRcd":6400,"NeighborName":"NotAvailable"},"aa00::a":{"version":4,"remoteAs":64802,"msgRcvd":9189,"msgSent":9193,"tableVersion":19203,"inq":0,"outq":0,"peerUptime":"4d03h44m","state":"Established","pfxRcd":6400,"NeighborName":"NotAvailable"}}}}`

	ResetDataSetsAndMappings(t)

	tests := []struct {
		desc           string
		pathTarget     string
		textPbPath     string
		wantRetCode    codes.Code
		wantRespVal    interface{}
		valTest        bool
		mockOutputFile string
		testInit       func()
	}{
		{
			desc:       "query SHOW ipv6 bgp summary read error",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "ipv6" >
				elem: <name: "bgp" >
				elem: <name: "summary" >
			`,
			wantRetCode: codes.NotFound,
		},
		{
			desc:       "query SHOW ipv6 bgp summary invalid vtysh output",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "ipv6" >
				elem: <name: "bgp" >
				elem: <name: "summary" >
			`,
			wantRetCode:    codes.NotFound,
			mockOutputFile: "../testdata/INVALID_JSON.txt",
		},
		{
			desc:       "query SHOW ipv6 bgp summary",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "ipv6" >
				elem: <name: "bgp" >
				elem: <name: "summary" >
			`,
			wantRetCode:    codes.OK,
			wantRespVal:    []byte(ipv6BGPSummaryDefault),
			valTest:        true,
			mockOutputFile: "../testdata/VTYSH_SHOW_IPV6_SUMMARY_JSON.txt",
			testInit: func() {
				AddDataSet(t, ConfigDbNum, bgpNeighborFileName)
			},
		},
		{
			desc:       "query SHOW ipv6 bgp summary NO BGP_NEIGHBOR TABLE",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "ipv6" >
				elem: <name: "bgp" >
				elem: <name: "summary" >
			`,
			wantRetCode:    codes.OK,
			wantRespVal:    []byte(ipv6BGPSummaryNoNeighborName),
			valTest:        true,
			mockOutputFile: "../testdata/VTYSH_SHOW_IPV6_SUMMARY_JSON.txt",
			testInit: func() {
				FlushDataSet(t, ConfigDbNum)
			},
		},
	}

	for _, test := range tests {
		if test.testInit != nil {
			test.testInit()
		}
		var patches *gomonkey.Patches
		if test.mockOutputFile != "" {
			patches = MockNSEnterBGPSummary(t, test.mockOutputFile)
		}

		t.Run(test.desc, func(t *testing.T) {
			runTestGet(t, ctx, gClient, test.pathTarget, test.textPbPath, test.wantRetCode, test.wantRespVal, test.valTest)
		})
		if patches != nil {
			patches.Reset()
		}
	}
}
