package gnmi

// chassis_module_cli_test.go

// Tests SHOW chassis modules status and SHOW chassis modules status [dpu=DPU_NAME]

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

// ModuleFixture represents a test module with all its properties
type ModuleFixture struct {
	Name        string
	AdminStatus string
	OperStatus  string
	Serial      string
	Description string
	Slot        string
}

// DefaultModuleFixtures provides standard test data for chassis modules
var DefaultModuleFixtures = []ModuleFixture{
	{
		Name:        "DPU0",
		AdminStatus: "up",
		OperStatus:  "Online",
		Serial:      "FLM29100D70-0",
		Description: "AMD Pensando DSC",
		Slot:        "N/A",
	},
	{
		Name:        "DPU1",
		AdminStatus: "up",
		OperStatus:  "Online",
		Serial:      "FLM29100D70-1",
		Description: "AMD Pensando DSC",
		Slot:        "N/A",
	},
	{
		Name:        "DPU2",
		AdminStatus: "up",
		OperStatus:  "Online",
		Serial:      "FLM29100D6U-0",
		Description: "AMD Pensando DSC",
		Slot:        "N/A",
	},
	{
		Name:        "DPU3",
		AdminStatus: "down",
		OperStatus:  "Online",
		Serial:      "FLM29100D6U-1",
		Description: "AMD Pensando DSC",
		Slot:        "N/A",
	},
}

// FixturesToMap converts ModuleFixture slice to the format expected by the API
func FixturesToMap(fixtures []ModuleFixture) map[string]map[string]interface{} {
	result := make(map[string]map[string]interface{})
	for _, f := range fixtures {
		result[f.Name] = map[string]interface{}{
			"admin_status": f.AdminStatus,
			"oper_status":  f.OperStatus,
			"serial":       f.Serial,
			"desc":         f.Description,
			"slot":         f.Slot,
		}
	}
	return result
}

// FixturesToSingleModuleMap converts ModuleFixture slice to single module format
func FixturesToSingleModuleMap(fixtures []ModuleFixture, moduleName string) map[string]interface{} {
	for _, f := range fixtures {
		if f.Name == moduleName {
			return map[string]interface{}{
				moduleName: map[string]interface{}{
					"name":          f.Name,
					"description":   f.Description,
					"physical_slot": f.Slot,
					"oper_status":   f.OperStatus,
					"admin_status":  f.AdminStatus,
					"serial":        f.Serial,
				},
			}
		}
	}
	return nil
}

func TestGetShowChassisModulesStatus(t *testing.T) {
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
			desc:       "query SHOW chassis modules status - no data",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "chassis" >
				elem: <name: "modules" >
				elem: <name: "status" >
			`,
			wantRetCode: codes.OK,
			valTest:     false,
		},
		{
			desc:       "query SHOW chassis modules status - all modules",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "chassis" >
				elem: <name: "modules" >
				elem: <name: "status" >
			`,
			wantRetCode: codes.OK,
			wantRespVal: func() []byte {
				expected := FixturesToMap(DefaultModuleFixtures)
				jsonData, _ := json.Marshal(expected)
				return jsonData
			}(),
			valTest: true,
			testInit: func() {
				AddDataSet(t, StateDbNum, "../testdata/CHASSIS_MODULE_STATE.txt")
				AddDataSet(t, ConfigDbNum, "../testdata/CHASSIS_MODULE_CONFIG.txt")
			},
		},
		{
			desc:       "query SHOW chassis modules status [dpu=DPU1] - specific module",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "chassis" >
				elem: <name: "modules" >
				elem: <name: "status" key: <key: "dpu" value: "DPU1" > >
			`,
			wantRetCode: codes.OK,
			wantRespVal: func() []byte {
				expected := FixturesToSingleModuleMap(DefaultModuleFixtures, "DPU1")
				jsonData, _ := json.MarshalIndent(expected, "", "  ")
				return jsonData
			}(),
			valTest: true,
			testInit: func() {
				FlushDataSet(t, StateDbNum)
				FlushDataSet(t, ConfigDbNum)
				AddDataSet(t, StateDbNum, "../testdata/CHASSIS_MODULE_STATE.txt")
				AddDataSet(t, ConfigDbNum, "../testdata/CHASSIS_MODULE_CONFIG.txt")
			},
		},
		{
			desc:       "query SHOW chassis modules status [dpu=DPU99] - module not found",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "chassis" >
				elem: <name: "modules" >
				elem: <name: "status" key: <key: "dpu" value: "DPU99" > >
			`,
			wantRetCode: codes.NotFound,
			testInit: func() {
				AddDataSet(t, StateDbNum, "../testdata/CHASSIS_MODULE_STATE.txt")
				AddDataSet(t, ConfigDbNum, "../testdata/CHASSIS_MODULE_CONFIG.txt")
			},
		},
		{
			desc:       "query SHOW chassis modules status [dpu=] - empty dpu name",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "chassis" >
				elem: <name: "modules" >
				elem: <name: "status" key: <key: "dpu" value: "" > >
			`,
			wantRetCode: codes.InvalidArgument,
			testInit: func() {
				AddDataSet(t, StateDbNum, "../testdata/CHASSIS_MODULE_STATE.txt")
				AddDataSet(t, ConfigDbNum, "../testdata/CHASSIS_MODULE_CONFIG.txt")
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

func TestChassisModuleHelperFunctions(t *testing.T) {
	// Test helper functions directly for better coverage

	// Test CreateChassisModuleQueries
	t.Run("CreateChassisModuleQueries - all modules", func(t *testing.T) {
		queries := show_client.CreateChassisModuleQueries("")
		if len(queries.State) != 1 || len(queries.Config) != 1 {
			t.Error("Expected one query each for state and config")
		}
		if queries.State[0][1] != "CHASSIS_MODULE_TABLE" {
			t.Error("Expected CHASSIS_MODULE_TABLE in state query")
		}
		if queries.Config[0][1] != "CHASSIS_MODULE" {
			t.Error("Expected CHASSIS_MODULE in config query")
		}
	})

	t.Run("CreateChassisModuleQueries - specific module", func(t *testing.T) {
		queries := show_client.CreateChassisModuleQueries("DPU1")
		if len(queries.State[0]) != 3 || queries.State[0][2] != "DPU1" {
			t.Error("Expected module name in state query")
		}
		if len(queries.Config[0]) != 3 || queries.Config[0][2] != "DPU1" {
			t.Error("Expected module name in config query")
		}
	})

	// Test CreateModuleStatusFromFlatData
	t.Run("CreateModuleStatusFromFlatData", func(t *testing.T) {
		stateData := map[string]interface{}{
			"desc":        "Test Description",
			"slot":        "Slot1",
			"oper_status": "Online",
			"serial":      "TEST123",
		}

		configData := map[string]interface{}{
			"admin_status": "down",
		}

		module := show_client.CreateModuleStatusFromFlatData("DPU1", stateData, configData)

		if module.Name != "DPU1" {
			t.Errorf("Expected name DPU1, got %s", module.Name)
		}
		if module.Description != "Test Description" {
			t.Errorf("Expected description 'Test Description', got %s", module.Description)
		}
		if module.AdminStatus != "down" {
			t.Errorf("Expected admin_status 'down', got %s", module.AdminStatus)
		}
		if module.OperStatus != "Online" {
			t.Errorf("Expected oper_status 'Online', got %s", module.OperStatus)
		}
		if module.Serial != "TEST123" {
			t.Errorf("Expected serial 'TEST123', got %s", module.Serial)
		}
		if module.Slot != "Slot1" {
			t.Errorf("Expected slot 'Slot1', got %s", module.Slot)
		}
	})

	t.Run("CreateModuleStatusFromFlatData - defaults", func(t *testing.T) {
		stateData := map[string]interface{}{}
		configData := map[string]interface{}{}

		module := show_client.CreateModuleStatusFromFlatData("DPU1", stateData, configData)

		if module.AdminStatus != "up" {
			t.Errorf("Expected default admin_status 'up', got %s", module.AdminStatus)
		}
		if module.Slot != "N/A" {
			t.Errorf("Expected default slot 'N/A', got %s", module.Slot)
		}
	})
}
