package gnmi

// interface_transceiver_cli_test.go

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

func TestGetTransceiverErrorStatus(t *testing.T) {
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

	transceiverErrorStatusFileName := "../testdata/TRANSCEIVER_STATUS_SW.txt"
	transceiverErrorStatus := `{"Ethernet0":{"cmis_state": "READY","error": "N/A","status": "1"},"Ethernet40": {"cmis_state": "READY","error": "N/A","status": "1"},"Ethernet80": {"cmis_state": "READY","error": "N/A","status": "1"},"Ethernet120": {"cmis_state": "READY","error": "N/A","status": "1"},"Ethernet160": {"cmis_state": "READY","error": "N/A","status": "1"}}`
	transceiverErrorStatusPort := `{"cmis_state": "READY","error": "N/A","status": "1"}`
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
			desc:       "query SHOW interface transceiver error-status read error",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "" >
				elem: <name: "transceiver" >
				elem: <name: "error-status" >
			`,
			wantRetCode: codes.NotFound,
		},
		{
			desc:       "query SHOW interface transceiver error-status NO interface dataset",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "interface" >
				elem: <name: "transceiver" >
				elem: <name: "error-status" >
			`,
			wantRetCode: codes.OK,
		},
		{
			desc:       "query SHOW interface transceiver error-status",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "interface" >
				elem: <name: "transceiver" >
				elem: <name: "error-status" >
			`,
			wantRetCode: codes.OK,
			wantRespVal: []byte(transceiverErrorStatus),
			valTest:     true,
			testInit: func() {
				FlushDataSet(t, StateDbNum)
				AddDataSet(t, StateDbNum, transceiverErrorStatusFileName)
			},
		},
		{
			desc:       "query SHOW interface transceiver error-status port option",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "interface" >
				elem: <name: "transceiver" >
				elem: <name: "error-status" key: { key: "interface" value: "Ethernet80" }>
			`,
			wantRetCode: codes.OK,
			wantRespVal: []byte(transceiverErrorStatusPort),
			valTest:     true,
			testInit: func() {
				FlushDataSet(t, StateDbNum)
				AddDataSet(t, StateDbNum, transceiverErrorStatusFileName)
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

	portTableFileName := "../testdata/TRANSCEIVER_PORT_TABLE.txt"
	portsFileName := "../testdata/TRANSCEIVER_PORTS.txt"
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

func TestGetTransceiverInfo(t *testing.T) {
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

	portTableFileName := "../testdata/TRANSCEIVER_PORT_TABLE.txt"
	portsFileName := "../testdata/TRANSCEIVER_PORTS.txt"
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
			desc:       "query SHOW interface transceiver info read error",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "" >
				elem: <name: "transceiver" >
				elem: <name: "info" >
			`,
			wantRetCode: codes.NotFound,
		},
		{
			desc:       "query SHOW interface transceiver info NO interface dataset",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "interface" >
				elem: <name: "transceiver" >
				elem: <name: "info" >
			`,
			wantRetCode: codes.OK,
		},
		{
			desc:       "query SHOW interface transceiver info",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "interface" >
				elem: <name: "transceiver" >
				elem: <name: "info" >
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
			desc:       "query SHOW interface transceiver info port option",
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
