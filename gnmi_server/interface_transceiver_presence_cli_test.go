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

func TestShowInterfaceTransceiverPresence(t *testing.T) {
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

	interfaceTransceiverPresenceConfigDBFileName := "../testdata/INTERFACE_TRANSCEIVER_PRESENCE_CONFIG_DB.txt"
	interfaceTransceiverPresenceStateDBFileName := "../testdata/INTERFACE_TRANSCEIVER_PRESENCE_STATE_DB.txt"

	intfTransPresData := `{{"Ethernet0": "Present"}, {"Ethernet4": "Present"}, {"Ethernet8": "Not Present"}}`
	intfTransPresDataWithIntf := `{{"Ethernet0": "Present"}}`
	intfTransPresDataWithNonExistentIntf := `{{"Ethernet1": "Not Present"}}`

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
			desc:       "query SHOW interface transceiver presence - no data",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "interface" >
				elem: <name: "transceiver" >
				elem: <name: "presence" >
			`,
			wantRetCode: codes.OK,
		},
		{
			desc:       "query SHOW interface transceiver presence - with no interface specified",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "interface" >
				elem: <name: "transceiver" >
				elem: <name: "presence" >
			`,
			wantRetCode: codes.OK,
			wantRespVal: []byte(intfTransPresData),
			valTest:     true,
			testInit: func() {
				FlushDataSet(t, StateDbNum)
				FlushDataSet(t, ConfigDbNum)
				AddDataSet(t, StateDbNum, interfaceTransceiverPresenceStateDBFileName)
				AddDataSet(t, ConfigDbNum, interfaceTransceiverPresenceConfigDBFileName)
			},
		},
		{
			desc:       "query SHOW interface transceiver presence - with interface specified",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "interface" >
				elem: <name: "transceiver" >
				elem: <name: "presence" key: { key: "interface" value: "Ethernet0" } >
			`,
			wantRetCode: codes.OK,
			wantRespVal: []byte(intfTransPresDataWithIntf),
			valTest:     true,
			testInit: func() {
				FlushDataSet(t, StateDbNum)
				FlushDataSet(t, ConfigDbNum)
				AddDataSet(t, StateDbNum, interfaceTransceiverPresenceStateDBFileName)
				AddDataSet(t, ConfigDbNum, interfaceTransceiverPresenceConfigDBFileName)
			},
		},
		{
			desc:       "query SHOW interface transceiver presence - with non-existent interface specified",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "interface" >
				elem: <name: "transceiver" >
				elem: <name: "presence" key: { key: "interface" value: "Ethernet1" } >
			`,
			wantRetCode: codes.OK,
			wantRespVal: []byte(intfTransPresDataWithNonExistentIntf),
			valTest:     true,
			testInit: func() {
				FlushDataSet(t, StateDbNum)
				FlushDataSet(t, ConfigDbNum)
				AddDataSet(t, StateDbNum, interfaceTransceiverPresenceStateDBFileName)
				AddDataSet(t, ConfigDbNum, interfaceTransceiverPresenceConfigDBFileName)
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
