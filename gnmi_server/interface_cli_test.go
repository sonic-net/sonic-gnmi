package gnmi

// interface_cli_test.go

// Tests SHOW interface/counters

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

func TestGetInterfaceCounters(t *testing.T) {
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

	portsFileName := "../testdata/PORTS.txt"
	portOidMappingFileName := "../testdata/PORT_COUNTERS_MAPPING.txt"
	portCountersFileName := "../testdata/PORT_COUNTERS.txt"
	portCountersTwoFileName := "../testdata/PORT_COUNTERS_TWO.txt"
	portRatesFileName := "../testdata/PORT_RATES.txt"
	portRatesTwoFileName := "../testdata/PORT_RATES_TWO.txt"
	portTableFileName := "../testdata/PORT_TABLE.txt"
	interfaceCountersAll := `{"Ethernet0":{"State":"U","RxOk":"149903","RxBps":"25.12 B/s","RxUtil":"0.00%","RxErr":"0","RxDrp":"957","RxOvr":"0","TxOk":"144782","TxBps":"773.23 KB/s","TxUtil":"0.01%","TxErr":"0","TxDrp":"2","TxOvr":"0"},"Ethernet40":{"State":"U","RxOk":"7295","RxBps":"0.00 B/s","RxUtil":"0.00%","RxErr":"0","RxDrp":"0","RxOvr":"0","TxOk":"50184","TxBps":"633.66 KB/s","TxUtil":"0.01%","TxErr":"0","TxDrp":"1","TxOvr":"0"},"Ethernet80":{"State":"U","RxOk":"76555","RxBps":"0.37 B/s","RxUtil":"0.00%","RxErr":"0","RxDrp":"0","RxOvr":"0","TxOk":"144767","TxBps":"631.94 KB/s","TxUtil":"0.01%","TxErr":"0","TxDrp":"1","TxOvr":"0"}}`
	interfaceCountersSelectPorts := `{"Ethernet0":{"State":"U","RxOk":"149903","RxBps":"25.12 B/s","RxUtil":"0.00%","RxErr":"0","RxDrp":"957","RxOvr":"0","TxOk":"144782","TxBps":"773.23 KB/s","TxUtil":"0.01%","TxErr":"0","TxDrp":"2","TxOvr":"0"}}`
	interfaceCountersDiff := `{"Ethernet0":{"State":"U","RxOk":"11658","RxBps":"21.39 B/s","RxUtil":"0.00%","RxErr":"0","RxDrp":"76","RxOvr":"0","TxOk":"11270","TxBps":"634.00 KB/s","TxUtil":"0.01%","TxErr":"0","TxDrp":"0","TxOvr":"0"}}`

	ResetDataSetsAndMappings(t)

	tests := []struct {
		desc        string
		pathTarget  string
		textPbPath  string
		wantRetCode codes.Code
		wantRespVal interface{}
		valTest     bool
		mockSleep   bool
		testInit    func()
	}{
		{
			desc:       "query SHOW interface counters NO DATA",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "interface" >
				elem: <name: "counters" >
			`,
			wantRetCode: codes.OK,
		},
		{
			desc:       "query SHOW interface counters",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "interface" >
				elem: <name: "counters" >
			`,
			wantRetCode: codes.OK,
			wantRespVal: []byte(interfaceCountersAll),
			valTest:     true,
			testInit: func() {
				AddDataSet(t, ConfigDbNum, portsFileName)
				AddDataSet(t, CountersDbNum, portOidMappingFileName)
				AddDataSet(t, CountersDbNum, portCountersFileName)
				AddDataSet(t, CountersDbNum, portRatesFileName)
				AddDataSet(t, ApplDbNum, portTableFileName)
			},
		},
		{
			desc:       "query SHOW interface counters interfaces option",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "interface" >
				elem: <name: "counters" key: { key: "interfaces" value: "Ethernet0" }>
			`,
			wantRetCode: codes.OK,
			wantRespVal: []byte(interfaceCountersSelectPorts),
			valTest:     true,
		},
		{
			desc:       "query SHOW interface counters period option",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "interface" >
				elem: <name: "counters"
				      key: { key: "interfaces" value: "Ethernet0" }
				      key: { key: "period" value: "5" }>
			`,
			wantRetCode: codes.OK,
			wantRespVal: []byte(interfaceCountersDiff),
			valTest:     true,
			mockSleep:   true,
		},
	}

	for _, test := range tests {
		if test.testInit != nil {
			test.testInit()
		}
		var patches *gomonkey.Patches
		if test.mockSleep {
			patches = gomonkey.ApplyFunc(time.Sleep, func(d time.Duration) {
				AddDataSet(t, CountersDbNum, portCountersTwoFileName)
				AddDataSet(t, CountersDbNum, portRatesTwoFileName)
			})
		}

		t.Run(test.desc, func(t *testing.T) {
			runTestGet(t, ctx, gClient, test.pathTarget, test.textPbPath, test.wantRetCode, test.wantRespVal, test.valTest)
		})
		if patches != nil {
			patches.Reset()
		}
	}
}
