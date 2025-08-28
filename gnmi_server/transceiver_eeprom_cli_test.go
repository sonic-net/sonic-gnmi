package gnmi

// interface_transceiver_eeprom_cli_test.go

// Tests SHOW interface transceiver commands

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

func TestGetTransceiverEEPROM(t *testing.T) {
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

	portTableFileName := "../testdata/PORT_TABLE.txt"
	portsFileName := "../testdata/PORTS.txt"
	transceiverInfoFileName := "../testdata/TRANSCEIVER_INFO.txt"
	transceiverFirmwareInfoFileName := "../testdata/TRANSCEIVER_FIRMWARE_INFO.txt"
	transceiverDomSensorFileName := "../testdata/TRANSCEIVER_DOM_SENSOR.txt"
	transceiverDomThresholdFileName := "../testdata/TRANSCEIVER_DOM_THRESHOLD.txt"
	transceiverErrorStatusFileName := "../testdata/TRANSCEIVER_STATUS_SW.txt"

	transceiverEEPROM := ``
	transceiverEEPROMPort := ``
	ResetDataSetsAndMappings(t)

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
			desc:       "query SHOW interface transceiver eeprom read error",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "" >
				elem: <name: "transceiver" >
				elem: <name: "eeprom" >
			`,
			wantRetCode: codes.NotFound,
		},
		{
			desc:       "query SHOW interface transceiver eeprom NO interface dataset",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "interface" >
				elem: <name: "transceiver" >
				elem: <name: "eeprom" >
			`,
			wantRetCode: codes.OK,
		},
		{
			desc:       "query SHOW interface transceiver eeprom",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "interface" >
				elem: <name: "transceiver" >
				elem: <name: "eeprom" >
			`,
			wantRetCode: codes.OK,
			wantRespVal: []byte(transceiverEEPROM),
			valTest:     false,
			testInit: func() {
				FlushDataSet(t, ApplDbNum)
				FlushDataSet(t, ConfigDbNum)
				FlushDataSet(t, StateDbNum)
				AddDataSet(t, ApplDbNum, portTableFileName)
				AddDataSet(t, ConfigDbNum, portsFileName)
				AddDataSet(t, StateDbNum, transceiverInfoFileName)
				AddDataSet(t, StateDbNum, transceiverFirmwareInfoFileName)
				AddDataSet(t, StateDbNum, transceiverDomSensorFileName)
				AddDataSet(t, StateDbNum, transceiverDomThresholdFileName)
				AddDataSet(t, StateDbNum, transceiverErrorStatusFileName)
			},
		},
		{
			desc:       "query SHOW interface transceiver eeprom port option",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "interface" >
				elem: <name: "transceiver" >
				elem: <name: "eeprom" key: { key: "port" value: "Ethernet40" }>
			`,
			wantRetCode: codes.OK,
			wantRespVal: []byte(transceiverEEPROMPort),
			valTest:     false,
		},
		{
			desc:       "query SHOW interface transceiver eeprom dom option",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "interface" >
				elem: <name: "transceiver" >
				elem: <name: "eeprom" key: { key: "dom" value: "true" }>
			`,
			wantRetCode: codes.OK,
			wantRespVal: []byte(transceiverEEPROMPort),
			valTest:     false,
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
