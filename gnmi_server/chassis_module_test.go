package gnmi

// chassis_module_test.go

// Tests for SHOW chassis modules commands:
// - SHOW chassis modules status
// - SHOW chassis modules status [dpu=DPU_NAME]
// - SHOW chassis modules midplane-status
// - SHOW chassis modules midplane-status [dpu=DPU_NAME]

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

// MidplaneFixture represents a test module midplane with its properties
type MidplaneFixture struct {
	Name         string
	IPAddress    string
	Reachability string
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
