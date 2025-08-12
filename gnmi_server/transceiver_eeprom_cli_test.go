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

	transceiverEEPROM := `
Ethernet0: SFP EEPROM detected
        Vendor SN: APF2447201229A
        Vendor Rev: A
        Vendor PN: NJMMER-M201
        Vendor OUI: 78-a7-14
        Vendor Name: Amphenol
        Vendor Date Code(YYYY-MM-DD Lot): 2024-11-25
        Nominal Bit Rate(100Mbs): N/A
        Module Hardware Rev: 0.0
        Media Lane Count: 0
        Media Interface Technology: Copper cable unequalized
        Length Cable Assembly(m): 1.0
        Inactive Firmware: N/A
        Identifier: OSFP 8X Pluggable Transceiver
        Host Lane Count: 8
        Extended RateSelect Compliance: N/A
        Extended Identifier: Power Class 1 (0.25W Max)
        Encoding: N/A
        Connector: No separable connector
        CMIS Rev: 5.0
        Active application selected code assigned to host lane 8: N/A
        Active application selected code assigned to host lane 7: N/A
        Active application selected code assigned to host lane 6: N/A
        Active application selected code assigned to host lane 5: N/A
        Active application selected code assigned to host lane 4: N/A
        Active application selected code assigned to host lane 3: N/A
        Active application selected code assigned to host lane 2: N/A
        Active application selected code assigned to host lane 1: N/A
        Active Firmware: N/A
        vdm_supported: False
        type_abbrv_name: OSFP-8X
        media_lane_assignment_option: N/A
        media_interface_code: Copper cable
        is_replaceable: True
        host_lane_assignment_option: 1
        host_electrical_interface: N/A

Ethernet40: SFP EEPROM detected
        Vendor SN: APF244720121WT
        Vendor Rev: A
        Vendor PN: NJMMER-M201
        Vendor OUI: 78-a7-14
        Vendor Name: Amphenol
        Vendor Date Code(YYYY-MM-DD Lot): 2024-11-26
        Nominal Bit Rate(100Mbs): N/A
        Module Hardware Rev: 0.0
        Media Lane Count: 0
        Media Interface Technology: Copper cable unequalized
        Length Cable Assembly(m): 1.0
        Inactive Firmware: N/A
        Identifier: OSFP 8X Pluggable Transceiver
        Host Lane Count: 8
        Extended RateSelect Compliance: N/A
        Extended Identifier: Power Class 1 (0.25W Max)
        Encoding: N/A
        Connector: No separable connector
        CMIS Rev: 5.0
        Active application selected code assigned to host lane 8: N/A
        Active application selected code assigned to host lane 7: N/A
        Active application selected code assigned to host lane 6: N/A
        Active application selected code assigned to host lane 5: N/A
        Active application selected code assigned to host lane 4: N/A
        Active application selected code assigned to host lane 3: N/A
        Active application selected code assigned to host lane 2: N/A
        Active application selected code assigned to host lane 1: N/A
        Active Firmware: N/A
        vdm_supported: False
        type_abbrv_name: OSFP-8X
        media_lane_assignment_option: N/A
        media_interface_code: Copper cable
        is_replaceable: True
        host_lane_assignment_option: 1
        host_electrical_interface: N/A

Ethernet80: SFP EEPROM detected
        Vendor SN: APF24482013Y1U
        Vendor Rev: A
        Vendor PN: NJMMER-M201
        Vendor OUI: 78-a7-14
        Vendor Name: Amphenol
        Vendor Date Code(YYYY-MM-DD Lot): 2024-11-26
        Nominal Bit Rate(100Mbs): N/A
        Module Hardware Rev: 0.0
        Media Lane Count: 0
        Media Interface Technology: Copper cable unequalized
        Length Cable Assembly(m): 1.0
        Inactive Firmware: N/A
        Identifier: OSFP 8X Pluggable Transceiver
        Host Lane Count: 8
        Extended RateSelect Compliance: N/A
        Extended Identifier: Power Class 1 (0.25W Max)
        Encoding: N/A
        Connector: No separable connector
        CMIS Rev: 5.0
        Active application selected code assigned to host lane 8: N/A
        Active application selected code assigned to host lane 7: N/A
        Active application selected code assigned to host lane 6: N/A
        Active application selected code assigned to host lane 5: N/A
        Active application selected code assigned to host lane 4: N/A
        Active application selected code assigned to host lane 3: N/A
        Active application selected code assigned to host lane 2: N/A
        Active application selected code assigned to host lane 1: N/A
        Active Firmware: N/A
        vdm_supported: False
        type_abbrv_name: OSFP-8X
        media_lane_assignment_option: N/A
        media_interface_code: Copper cable
        is_replaceable: True
        host_lane_assignment_option: 1
        host_electrical_interface: N/A
`
	transceiverEEPROMPort := `
Ethernet0: SFP EEPROM detected
        Vendor SN: APF2447201229A
        Vendor Rev: A
        Vendor PN: NJMMER-M201
        Vendor OUI: 78-a7-14
        Vendor Name: Amphenol
        Vendor Date Code(YYYY-MM-DD Lot): 2024-11-25
        Nominal Bit Rate(100Mbs): N/A
        Module Hardware Rev: 0.0
        Media Lane Count: 0
        Media Interface Technology: Copper cable unequalized
        Length Cable Assembly(m): 1.0
        Inactive Firmware: N/A
        Identifier: OSFP 8X Pluggable Transceiver
        Host Lane Count: 8
        Extended RateSelect Compliance: N/A
        Extended Identifier: Power Class 1 (0.25W Max)
        Encoding: N/A
        Connector: No separable connector
        CMIS Rev: 5.0
        Active application selected code assigned to host lane 8: N/A
        Active application selected code assigned to host lane 7: N/A
        Active application selected code assigned to host lane 6: N/A
        Active application selected code assigned to host lane 5: N/A
        Active application selected code assigned to host lane 4: N/A
        Active application selected code assigned to host lane 3: N/A
        Active application selected code assigned to host lane 2: N/A
        Active application selected code assigned to host lane 1: N/A
        Active Firmware: N/A
        vdm_supported: False
        type_abbrv_name: OSFP-8X
        media_lane_assignment_option: N/A
        media_interface_code: Copper cable
        is_replaceable: True
        host_lane_assignment_option: 1
        host_electrical_interface: N/A
`
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
				elem: <name: "eeprom" key: { key: "port" value: "Ethernet0" }>
			`,
			wantRetCode: codes.OK,
			wantRespVal: []byte(transceiverEEPROMPort),
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
