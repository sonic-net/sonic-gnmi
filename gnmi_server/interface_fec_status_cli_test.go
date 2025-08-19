package gnmi

// intf_cli_test.go

// Tests SHOW interface errors

import (
	"crypto/tls"
	"testing"
	"time"

	pb "github.com/openconfig/gnmi/proto/gnmi"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
)

func TestGetShowInterfaceFecStatus(t *testing.T) {
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

	// Expected JSON for empty (no DB data) case: empty list
	emptyResp := `[]`

	fullData := `[{"Interface": "Ethernet0", "FEC Oper": "rs", "FEC Admin": "rs"},{"Interface": "Ethernet40", "FEC Oper": "N/A", "FEC Admin": "rs"},{"Interface": "Ethernet80", "FEC Oper": "rs", "FEC Admin": "rs"}]`
	mixedData := `[{"Interface": "Ethernet0", "FEC Oper": "N/A", "FEC Admin": "rs"},{"Interface": "Ethernet40", "FEC Oper": "N/A", "FEC Admin": "rs"},{"Interface": "Ethernet80", "FEC Oper": "N/A", "FEC Admin": "N/A"}]`
	oneIntfData := `[{"Interface": "Ethernet0", "FEC Oper": "rs", "FEC Admin": "rs"}]`

	portsFileName := "../testdata/PORTS.txt"
	portTableFileName := "../testdata/PORT_TABLE.txt"
	operDownPortTableFileName := "../testdata/OPER_DOWN_PORT_TABLE.json"
	stateDBPortTableFileName := "../testdata/STATE_PORT_TABLE.json"

	tests := []struct {
		desc        string
		pathTarget  string
		textPbPath  string
		wantRetCode codes.Code
		wantRespVal interface{}
		valTest     bool
		testInit    func()
	}{
		{
			desc:       "query SHOW interface fec status - no data",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "interface" >
				elem: <name: "fec" >
				elem: <name: "status" >
			`,
			wantRetCode: codes.OK,
			wantRespVal: []byte(emptyResp),
			valTest:     true,
		},
		{
			desc:       "query SHOW interface fec status - all ports",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "interface" >
				elem: <name: "fec" >
				elem: <name: "status" >
			`,
			wantRetCode: codes.OK,
			wantRespVal: []byte(fullData),
			valTest:     true,
			testInit: func() {
				FlushDataSet(t, ConfigDbNum)
				FlushDataSet(t, ApplDbNum)
				FlushDataSet(t, StateDbNum)
				AddDataSet(t, ConfigDbNum, portsFileName)
				AddDataSet(t, ApplDbNum, portTableFileName)
				AddDataSet(t, StateDbNum, stateDBPortTableFileName)
			},
		},
		{
			desc:       "query SHOW interface fec status - single interface",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "interface" >
				elem: <name: "fec" >
				elem: <name: "status" key: { key: "interface" value: "Ethernet0" } >
			`,
			wantRetCode: codes.OK,
			wantRespVal: []byte(oneIntfData),
			valTest:     true,
			testInit: func() {
				FlushDataSet(t, ConfigDbNum)
				FlushDataSet(t, ApplDbNum)
				FlushDataSet(t, StateDbNum)
				AddDataSet(t, ConfigDbNum, portsFileName)
				AddDataSet(t, ApplDbNum, portTableFileName)
				AddDataSet(t, StateDbNum, stateDBPortTableFileName)
			},
		},
		{
			desc:       "query SHOW interface fec status - single non-existent interface",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "interface" >
				elem: <name: "fec" >
				elem: <name: "status" key: { key: "interface" value: "Ethernet10" } >
			`,
			wantRetCode: codes.NotFound,
			testInit: func() {
				FlushDataSet(t, ConfigDbNum)
				FlushDataSet(t, ApplDbNum)
				FlushDataSet(t, StateDbNum)
				AddDataSet(t, ConfigDbNum, portsFileName)
				AddDataSet(t, ApplDbNum, portTableFileName)
				AddDataSet(t, StateDbNum, stateDBPortTableFileName)
			},
		},
		{
			desc:       "query SHOW interface fec status - all ports oper down",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "interface" >
				elem: <name: "fec" >
				elem: <name: "status" >
			`,
			wantRetCode: codes.OK,
			wantRespVal: []byte(mixedData),
			valTest:     true,
			testInit: func() {
				FlushDataSet(t, ConfigDbNum)
				FlushDataSet(t, ApplDbNum)
				FlushDataSet(t, StateDbNum)
				AddDataSet(t, ConfigDbNum, portsFileName)
				AddDataSet(t, ApplDbNum, operDownPortTableFileName)
				AddDataSet(t, StateDbNum, stateDBPortTableFileName)
			},
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
