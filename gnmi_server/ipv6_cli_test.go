package gnmi

// ipv6_cli_test.go

// Tests SHOW ipv6 bgp summary

import (
	"crypto/tls"
	"io/ioutil"
	"testing"
	"time"

	pb "github.com/openconfig/gnmi/proto/gnmi"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"

	show_client "github.com/sonic-net/sonic-gnmi/show_client"
	sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
)

const (
	ServerPort   = 8081
	StateDbNum   = 6
	ConfigDbNum  = 4
	TargetAddr   = "127.0.0.1:8081"
	QueryTimeout = 10
)

func MockNSEnterBGPSummary(t *testing.T, filename string) *gomonkey.Patches {
	fileContentBytes, err := ioutil.ReadFile(filename)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	patches := gomonkey.ApplyFunc(exec.Command, func(name string, args ...string) *exec.Cmd {
		return &exec.Cmd{}
	})
	patches.ApplyMethod(reflect.TypeOf(&exec.Cmd{}), "CombinedOutput", func(_ *exec.Cmd) ([]byte, error) {
		return fileContentBytes, nil
	})
	return patches
}

func TestGetIPv6BGPSummary(t *testing.T) {
	s := createServer(t, ServerPort)
	go runServer(t, s)
	defer s.ForceStop()

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
				AddDataSet(t, ConfigDBNum, bgpNeighborFileName)
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
		},
	}

	for _, test := range tests {
		if test.testInit != nil {
			test.testInit()
		}
		t.Run(test.desc, func(t *testing.T) {
			runTestGet(t, ctx, gClient, test.pathTarget, test.textPbPath, test.wantRetCode, test.wantRespVal, test.valTest)
		})
	}
}
