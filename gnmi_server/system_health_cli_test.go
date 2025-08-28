package gnmi

// system_health_cli_test.go

// Tests SHOW system-health dpu and SHOW system-health dpu [dpu=DPU_NAME]

import (
	"crypto/tls"
	"encoding/json"
	"testing"
	"time"

	pb "github.com/openconfig/gnmi/proto/gnmi"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"

	show_client "github.com/sonic-net/sonic-gnmi/show_client"
)

// DpuStateFixture represents a test DPU state row
type DpuStateFixture struct {
	Name        string
	OperStatus  string
	StateDetail string
	StateValue  string
	Time        string
	Reason      string
}

// DefaultDpuStateFixtures provides standard test data for DPU states
var DefaultDpuStateFixtures = []DpuStateFixture{
	// DPU0 - Online (all states UP)
	{
		Name:        "DPU0",
		OperStatus:  "Online",
		StateDetail: "dpu_midplane_link_state",
		StateValue:  "UP",
		Time:        "Sat Aug 23 02:02:47 AM UTC 2025",
		Reason:      "",
	},
	{
		Name:        "DPU0",
		OperStatus:  "Online",
		StateDetail: "dpu_control_plane_state",
		StateValue:  "UP",
		Time:        "Mon Aug 25 08:59:57 AM UTC 2025",
		Reason:      "",
	},
	{
		Name:        "DPU0",
		OperStatus:  "Online",
		StateDetail: "dpu_data_plane_state",
		StateValue:  "UP",
		Time:        "Sat Aug 23 02:02:53 AM UTC 2025",
		Reason:      "",
	},
	// DPU1 - Partial Online (control plane DOWN)
	{
		Name:        "DPU1",
		OperStatus:  "Partial Online",
		StateDetail: "dpu_midplane_link_state",
		StateValue:  "UP",
		Time:        "Sat Aug 23 02:02:47 AM UTC 2025",
		Reason:      "",
	},
	{
		Name:        "DPU1",
		OperStatus:  "Partial Online",
		StateDetail: "dpu_control_plane_state",
		StateValue:  "DOWN",
		Time:        "Mon Aug 25 09:00:03 AM UTC 2025",
		Reason:      "Container not running : snmp, radv, teamd, syncd, bgp, swss, host-ethlink-status: host_eth_link is down",
	},
	{
		Name:        "DPU1",
		OperStatus:  "Partial Online",
		StateDetail: "dpu_data_plane_state",
		StateValue:  "UP",
		Time:        "Sat Aug 23 02:02:53 AM UTC 2025",
		Reason:      "",
	},
	// DPU2 - Partial Online (control plane DOWN)
	{
		Name:        "DPU2",
		OperStatus:  "Partial Online",
		StateDetail: "dpu_midplane_link_state",
		StateValue:  "UP",
		Time:        "Sat Aug 23 02:02:47 AM UTC 2025",
		Reason:      "",
	},
	{
		Name:        "DPU2",
		OperStatus:  "Partial Online",
		StateDetail: "dpu_control_plane_state",
		StateValue:  "DOWN",
		Time:        "Mon Aug 25 08:59:32 AM UTC 2025",
		Reason:      "Container not running : snmp, radv, syncd, teamd, bgp, swss, host-ethlink-status: host_eth_link is down",
	},
	{
		Name:        "DPU2",
		OperStatus:  "Partial Online",
		StateDetail: "dpu_data_plane_state",
		StateValue:  "UP",
		Time:        "Sat Aug 23 02:02:53 AM UTC 2025",
		Reason:      "",
	},
	// DPU3 - Partial Online (control plane DOWN)
	{
		Name:        "DPU3",
		OperStatus:  "Partial Online",
		StateDetail: "dpu_midplane_link_state",
		StateValue:  "UP",
		Time:        "Sat Aug 23 02:02:47 AM UTC 2025",
		Reason:      "",
	},
	{
		Name:        "DPU3",
		OperStatus:  "Partial Online",
		StateDetail: "dpu_control_plane_state",
		StateValue:  "DOWN",
		Time:        "Mon Aug 25 09:00:17 AM UTC 2025",
		Reason:      "Container not running : snmp, radv, syncd, teamd, bgp, swss, host-ethlink-status: host_eth_link is down",
	},
	{
		Name:        "DPU3",
		OperStatus:  "Partial Online",
		StateDetail: "dpu_data_plane_state",
		StateValue:  "UP",
		Time:        "Sat Aug 23 02:02:53 AM UTC 2025",
		Reason:      "",
	},
}

// FixturesToDpuStateRows converts DpuStateFixture slice to the format expected by the API
func FixturesToDpuStateRows(fixtures []DpuStateFixture) []show_client.DpuStateRow {
	var rows []show_client.DpuStateRow
	for _, f := range fixtures {
		row := show_client.DpuStateRow{
			Name:        f.Name,
			OperStatus:  f.OperStatus,
			StateDetail: f.StateDetail,
			StateValue:  f.StateValue,
			Time:        f.Time,
			Reason:      f.Reason,
		}
		rows = append(rows, row)
	}
	return rows
}

// FixturesToSingleDpuRows filters fixtures for a specific DPU name and returns DpuStateRow slice
func FixturesToSingleDpuRows(fixtures []DpuStateFixture, dpuName string) []show_client.DpuStateRow {
	var rows []show_client.DpuStateRow
	for _, f := range fixtures {
		if f.Name == dpuName {
			row := show_client.DpuStateRow{
				Name:        f.Name,
				OperStatus:  f.OperStatus,
				StateDetail: f.StateDetail,
				StateValue:  f.StateValue,
				Time:        f.Time,
				Reason:      f.Reason,
			}
			rows = append(rows, row)
		}
	}
	return rows
}

func TestGetShowSystemHealthDpu(t *testing.T) {
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
			desc:       "query SHOW system-health dpu - no data",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "system-health" >
				elem: <name: "dpu" >
			`,
			wantRetCode: codes.NotFound,
			valTest:     false,
		},
		{
			desc:       "query SHOW system-health dpu - all modules",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "system-health" >
				elem: <name: "dpu" >
			`,
			wantRetCode: codes.OK,
			wantRespVal: func() []byte {
				expected := FixturesToDpuStateRows(DefaultDpuStateFixtures)
				jsonData, _ := json.Marshal(expected)
				return jsonData
			}(),
			valTest: true,
			testInit: func() {
				AddDataSet(t, ChassisStateDbNum, "../testdata/DPU_STATE.txt")
			},
		},
		{
			desc:       "query SHOW system-health dpu [dpu=DPU0] - specific module",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "system-health" >
				elem: <name: "dpu" key: <key: "dpu" value: "DPU0" > >
			`,
			wantRetCode: codes.OK,
			wantRespVal: func() []byte {
				expected := FixturesToSingleDpuRows(DefaultDpuStateFixtures, "DPU0")
				jsonData, _ := json.Marshal(expected)
				return jsonData
			}(),
			valTest: true,
			testInit: func() {
				FlushDataSet(t, ChassisStateDbNum)
				AddDataSet(t, ChassisStateDbNum, "../testdata/DPU_STATE.txt")
			},
		},
		{
			desc:       "query SHOW system-health dpu [dpu=DPU99] - module not found",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "system-health" >
				elem: <name: "dpu" key: <key: "dpu" value: "DPU99" > >
			`,
			wantRetCode: codes.NotFound, // Should return NotFound error
			valTest:     false,          // No value test needed for error case
			testInit: func() {
				AddDataSet(t, ChassisStateDbNum, "../testdata/DPU_STATE.txt")
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
