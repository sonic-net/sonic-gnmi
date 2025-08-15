package gnmi

import (
	"crypto/tls"
	"os"
	"testing"
	"time"

	pb "github.com/openconfig/gnmi/proto/gnmi"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
)

func TestGetQueueCounters(t *testing.T) {
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
	portTableFileName := "../testdata/PORT_TABLE.txt"
	queueOidMappingFileName := "../testdata/QUEUE_OID_MAPPING.txt"
	queueCountersFileName := "../testdata/QUEUE_COUNTERS.txt"
	allQueueCounters, err := os.ReadFile("../testdata/QUEUE_COUNTERS_RESULTS_ALL.txt")
	if err != nil {
		t.Fatalf("Failed to read expected query results for queues of all interfaces: %v", err)
	}
	oneSelectedQueueCounters, err := os.ReadFile("../testdata/QUEUE_COUNTERS_RESULTS_ONE.txt")
	if err != nil {
		t.Fatalf("Failed to read expected query results for queues of Ethernet40: %v", err)
	}
	twoSelectedQueueCounters, err := os.ReadFile("../testdata/QUEUE_COUNTERS_RESULTS_TWO.txt")
	if err != nil {
		t.Fatalf("Failed to read expected query results for queues of Ethernet0 and Ethernet80: %v", err)
	}

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
			desc:       "query SHOW queue counters NO DATA",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "queue" >
				elem: <name: "counters" >
			`,
			wantRetCode: codes.OK,
		},
		{
			desc:       "query SHOW queue counters",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "queue" >
				elem: <name: "counters" >
			`,
			wantRetCode: codes.OK,
			wantRespVal: allQueueCounters,
			valTest:     true,
			testInit: func() {
				AddDataSet(t, ConfigDbNum, portsFileName)
				AddDataSet(t, ApplDbNum, portTableFileName)
				AddDataSet(t, CountersDbNum, queueOidMappingFileName)
				AddDataSet(t, CountersDbNum, queueCountersFileName)
			},
		},
		{
			desc:       "query SHOW queue counters interfaces option (one interface)",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "queue" >
				elem: <name: "counters" key: { key: "interfaces" value: "Ethernet40" }>
			`,
			wantRetCode: codes.OK,
			wantRespVal: oneSelectedQueueCounters,
			valTest:     true,
		},
		{
			desc:       "query SHOW queue counters interfaces option (two interfaces)",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "queue" >
				elem: <name: "counters" key: { key: "interfaces" value: "Ethernet0,Ethernet80" }>
			`,
			wantRetCode: codes.OK,
			wantRespVal: twoSelectedQueueCounters,
			valTest:     true,
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
