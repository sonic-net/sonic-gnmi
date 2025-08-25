package gnmi

// chassis_module_midplane_cli_test.go

// Tests SHOW chassis modules midplane-status and SHOW chassis modules midplane-status [dpu=DPU_NAME]

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

// MidplaneFixture represents a test module midplane with its properties
type MidplaneFixture struct {
	Name         string
	IPAddress    string
	Reachability string
}

// DefaultMidplaneFixtures provides standard test data for chassis module midplanes
var DefaultMidplaneFixtures = []MidplaneFixture{
	{
		Name:         "DPU0",
		IPAddress:    "169.254.200.1",
		Reachability: "True",
	},
	{
		Name:         "DPU1",
		IPAddress:    "169.254.200.2",
		Reachability: "True",
	},
	{
		Name:         "DPU2",
		IPAddress:    "169.254.200.3",
		Reachability: "False",
	},
	{
		Name:         "DPU3",
		IPAddress:    "169.254.200.4",
		Reachability: "True",
	},
}

// FixturesToMidplaneMap converts MidplaneFixture slice to the format expected by the API
func FixturesToMidplaneMap(fixtures []MidplaneFixture) map[string]map[string]interface{} {
	result := make(map[string]map[string]interface{})
	for _, f := range fixtures {
		result[f.Name] = map[string]interface{}{
			"ip_address": f.IPAddress,
			"access":     f.Reachability,
		}
	}
	return result
}

// FixturesToSingleMidplaneMap converts MidplaneFixture slice to single module format
func FixturesToSingleMidplaneMap(fixtures []MidplaneFixture, moduleName string) map[string]interface{} {
	for _, f := range fixtures {
		if f.Name == moduleName {
			return map[string]interface{}{
				moduleName: map[string]interface{}{
					"name":         f.Name,
					"ip_address":   f.IPAddress,
					"reachability": f.Reachability,
				},
			}
		}
	}
	return nil
}

func TestGetShowChassisModulesMidplaneStatus(t *testing.T) {
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
			desc:       "query SHOW chassis modules midplane-status - no data",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "chassis" >
				elem: <name: "modules" >
				elem: <name: "midplane-status" >
			`,
			wantRetCode: codes.OK,
			valTest:     false,
		},
		{
			desc:       "query SHOW chassis modules midplane-status - all modules",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "chassis" >
				elem: <name: "modules" >
				elem: <name: "midplane-status" >
			`,
			wantRetCode: codes.OK,
			wantRespVal: func() []byte {
				expected := FixturesToMidplaneMap(DefaultMidplaneFixtures)
				jsonData, _ := json.Marshal(expected)
				return jsonData
			}(),
			valTest: true,
			testInit: func() {
				AddDataSet(t, StateDbNum, "../testdata/CHASSIS_MIDPLANE_STATE.txt")
			},
		},
		{
			desc:       "query SHOW chassis modules midplane-status [dpu=DPU1] - specific module",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "chassis" >
				elem: <name: "modules" >
				elem: <name: "midplane-status" key: <key: "dpu" value: "DPU1" > >
			`,
			wantRetCode: codes.OK,
			wantRespVal: func() []byte {
				expected := FixturesToSingleMidplaneMap(DefaultMidplaneFixtures, "DPU1")
				jsonData, _ := json.MarshalIndent(expected, "", "  ")
				return jsonData
			}(),
			valTest: true,
			testInit: func() {
				FlushDataSet(t, StateDbNum)
				AddDataSet(t, StateDbNum, "../testdata/CHASSIS_MIDPLANE_STATE.txt")
			},
		},
		{
			desc:       "query SHOW chassis modules midplane-status [dpu=DPU99] - module not found",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "chassis" >
				elem: <name: "modules" >
				elem: <name: "midplane-status" key: <key: "dpu" value: "DPU99" > >
			`,
			wantRetCode: codes.NotFound,
			testInit: func() {
				AddDataSet(t, StateDbNum, "../testdata/CHASSIS_MIDPLANE_STATE.txt")
			},
		},
		{
			desc:       "query SHOW chassis modules midplane-status [dpu=] - empty dpu name",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "chassis" >
				elem: <name: "modules" >
				elem: <name: "midplane-status" key: <key: "dpu" value: "" > >
			`,
			wantRetCode: codes.InvalidArgument,
			testInit: func() {
				AddDataSet(t, StateDbNum, "../testdata/CHASSIS_MIDPLANE_STATE.txt")
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

func TestChassisModuleMidplaneHelperFunctions(t *testing.T) {
	// Test helper functions directly for better coverage

	// Test CreateChassisModuleMidplaneQueries
	t.Run("CreateChassisModuleMidplaneQueries - all modules", func(t *testing.T) {
		queries := show_client.CreateChassisModuleMidplaneQueries("")
		if len(queries.State) != 1 {
			t.Error("Expected one query for state")
		}
		if queries.State[0][1] != "CHASSIS_MIDPLANE_TABLE" {
			t.Error("Expected CHASSIS_MIDPLANE_TABLE in state query")
		}
	})

	t.Run("CreateChassisModuleMidplaneQueries - specific module", func(t *testing.T) {
		queries := show_client.CreateChassisModuleMidplaneQueries("DPU1")
		if len(queries.State[0]) != 3 || queries.State[0][2] != "DPU1" {
			t.Error("Expected module name in state query")
		}
	})

	// Test CreateModuleMidplaneStatusFromFlatData
	t.Run("CreateModuleMidplaneStatusFromFlatData", func(t *testing.T) {
		stateData := map[string]interface{}{
			"ip_address": "192.168.1.10",
			"access":     "True",
		}

		module := show_client.CreateModuleMidplaneStatusFromFlatData("DPU1", stateData)

		if module.Name != "DPU1" {
			t.Errorf("Expected name DPU1, got %s", module.Name)
		}
		if module.IPAddress != "192.168.1.10" {
			t.Errorf("Expected ip_address '192.168.1.10', got %s", module.IPAddress)
		}
		if module.Reachability != "True" {
			t.Errorf("Expected reachability 'True', got %s", module.Reachability)
		}
	})

	t.Run("CreateModuleMidplaneStatusFromFlatData - missing fields", func(t *testing.T) {
		stateData := map[string]interface{}{}

		module := show_client.CreateModuleMidplaneStatusFromFlatData("DPU1", stateData)

		if module.Name != "DPU1" {
			t.Errorf("Expected name DPU1, got %s", module.Name)
		}
		if module.IPAddress != "" {
			t.Errorf("Expected empty ip_address, got %s", module.IPAddress)
		}
		if module.Reachability != "" {
			t.Errorf("Expected empty reachability, got %s", module.Reachability)
		}
	})
}
