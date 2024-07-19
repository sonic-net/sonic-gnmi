package common_utils

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
)

func clearComponentState(t *testing.T) {
	db, err := getRedisDBClient()
	if err != nil {
		t.Fatalf("Failed to get redis DB client: %v", err)
	}
	defer db.Close()
	sep, _ := sdcfg.GetDbSeparator(dbName, "")
	if keys, err := db.Keys(context.Background(), componentStateTable+sep+"*").Result(); err == nil {
		for _, key := range keys {
			if err = db.Del(context.Background(), key).Err(); err != nil {
				t.Fatalf("Failed to clear component state information in DB: %v\n", err)
			}
		}
	}
	if err = db.Del(context.Background(), alarmStatusTable).Err(); err != nil {
		t.Fatalf("Failed to clear alarmStatusTable: %v", err)
	}
}

func readAlarmStatus(t *testing.T) bool {
	db, err := getRedisDBClient()
	if err != nil {
		fmt.Printf("Failed to get redis DB client: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()
	value, err := db.HGet(context.Background(), alarmStatusTable, alarmStatusKey).Result()
	expectEqual(t, err, nil)
	if value == "true" {
		return true
	}
	return false
}

func expectEqual(t *testing.T, got interface{}, want interface{}) {
	if got != want {
		t.Fatalf("Got %v, want %v", got, want)
	}
}

func TestComponentStateHelperDefaultValue(t *testing.T) {
	clearComponentState(t)
	defer clearComponentState(t)
	h, _ := NewComponentStateHelper(P4rt)
	defer h.Close()
	expectEqual(t, h.StateInfo(), ComponentStateInfo{State: ComponentInitializing})
}

func TestSystemStateHelperDefauleValue(t *testing.T) {
	clearComponentState(t)
	defer clearComponentState(t)
	h, _ := NewSystemStateHelper()
	defer h.Close()
	expectEqual(t, h.GetSystemState(), SystemInitializing)
	expectEqual(t, h.IsSystemCritical(), false)
	expectEqual(t, readAlarmStatus(t), false)
	expectEqual(t, h.GetSystemCriticalReason(), "")
	allComponentStateInfo := h.AllComponentStates()
	expectEqual(t, len(allComponentStateInfo), len(EssentialComponents))
	for ec, _ := range EssentialComponents {
		expectEqual(t, allComponentStateInfo[ec], ComponentStateInfo{State: ComponentInitializing})
	}
}

func TestSystemStateHelperSeesAllComponents(t *testing.T) {
	for _, tc := range []SystemComponent{
		Host,
		P4rt,
		Orchagent,
		Syncd,
		Telemetry,
		Linkqual,
		PlatformMonitor,
		Inbandmgr,
		SwssCfgmgr,
	} {
		name := "Component Report " + tc.String()
		t.Run(name, func(t *testing.T) {
			clearComponentState(t)
			defer clearComponentState(t)
			ch, _ := NewComponentStateHelper(tc)
			defer ch.Close()
			err := ch.ReportComponentState(ComponentUp, "Reason 1")
			if err != nil {
				t.Errorf("Got error in reporting UP state for component %v: %v", tc, err)
			}
			sh, _ := NewSystemStateHelper()
			defer sh.Close()
			stateInfo := sh.AllComponentStates()
			if _, ok := stateInfo[tc]; !ok {
				t.Errorf("Missing information in SystemStateHelper for component %v", tc)
			}
		})
	}
}

func TestComponentStateReport(t *testing.T) {
	for _, tc := range []ComponentState{
		ComponentInitializing,
		ComponentUp,
		ComponentMinor,
		ComponentError,
		ComponentInactive,
	} {
		name := "Component State Report " + tc.String()
		t.Run(name, func(t *testing.T) {
			clearComponentState(t)
			defer clearComponentState(t)
			ch, _ := NewComponentStateHelper(P4rt)
			defer ch.Close()
			reason := "Reason 1"
			timeBeforeReport := uint64(time.Now().UnixNano())
			err := ch.ReportComponentState(tc, reason)
			timeAfterReport := uint64(time.Now().UnixNano())
			if err != nil {
				t.Errorf("Got error in reporting component state %v: %v", tc, err)
			}

			stateInfo := ch.StateInfo()
			expectEqual(t, stateInfo.State, tc)
			expectEqual(t, stateInfo.Reason, reason)
			if stateInfo.TimestampNanosec < timeBeforeReport || stateInfo.TimestampNanosec > timeAfterReport {
				t.Errorf("Expect timestamp in [%v, %v], got %v", timeBeforeReport, timeAfterReport, stateInfo.TimestampNanosec)
			}

			sh, _ := NewSystemStateHelper()
			defer sh.Close()
			stateInfo = sh.AllComponentStates()[P4rt]
			expectEqual(t, stateInfo.State, tc)
			expectEqual(t, stateInfo.Reason, reason)
			if stateInfo.TimestampNanosec < timeBeforeReport || stateInfo.TimestampNanosec > timeAfterReport {
				t.Errorf("Expect timestamp in [%v, %v], got %v", timeBeforeReport, timeAfterReport, stateInfo.TimestampNanosec)
			}
		})
	}
}

func TestComponentStateUpdateSucceeds(t *testing.T) {
	for _, tc := range []struct {
		stateBefore ComponentState
		stateAfter  ComponentState
	}{
		{
			stateBefore: ComponentInitializing,
			stateAfter:  ComponentInitializing,
		},
		{
			stateBefore: ComponentInitializing,
			stateAfter:  ComponentUp,
		},
		{
			stateBefore: ComponentInitializing,
			stateAfter:  ComponentMinor,
		},
		{
			stateBefore: ComponentInitializing,
			stateAfter:  ComponentError,
		},
		{
			stateBefore: ComponentInitializing,
			stateAfter:  ComponentInactive,
		},
		{
			stateBefore: ComponentUp,
			stateAfter:  ComponentInitializing,
		},
		{
			stateBefore: ComponentUp,
			stateAfter:  ComponentUp,
		},
		{
			stateBefore: ComponentUp,
			stateAfter:  ComponentMinor,
		},
		{
			stateBefore: ComponentUp,
			stateAfter:  ComponentError,
		},
		{
			stateBefore: ComponentUp,
			stateAfter:  ComponentInactive,
		},
		{
			stateBefore: ComponentMinor,
			stateAfter:  ComponentError,
		},
		{
			stateBefore: ComponentMinor,
			stateAfter:  ComponentInactive,
		},
		{
			stateBefore: ComponentError,
			stateAfter:  ComponentInactive,
		},
	} {
		name := "Component State Update Succeeds from " + tc.stateBefore.String() + " to " + tc.stateAfter.String()
		t.Run(name, func(t *testing.T) {
			clearComponentState(t)
			defer clearComponentState(t)
			ch, _ := NewComponentStateHelper(P4rt)
			defer ch.Close()
			reason1 := "Reason 1"
			err := ch.ReportComponentState(tc.stateBefore, reason1)
			if err != nil {
				t.Errorf("Got error in reporting component state %v: %v", tc, err)
			}
			reason2 := "Reason 2"
			timeBeforeReport := uint64(time.Now().UnixNano())
			err = ch.ReportComponentState(tc.stateAfter, reason2)
			timeAfterReport := uint64(time.Now().UnixNano())
			if err != nil {
				t.Errorf("Got error in reporting component state %v: %v", tc, err)
			}

			stateInfo := ch.StateInfo()
			expectEqual(t, stateInfo.State, tc.stateAfter)
			expectEqual(t, stateInfo.Reason, reason2)
			if stateInfo.TimestampNanosec < timeBeforeReport || stateInfo.TimestampNanosec > timeAfterReport {
				t.Errorf("Expect timestamp in [%v, %v], got %v", timeBeforeReport, timeAfterReport, stateInfo.TimestampNanosec)
			}

			sh, _ := NewSystemStateHelper()
			defer sh.Close()
			stateInfo = sh.AllComponentStates()[P4rt]
			expectEqual(t, stateInfo.State, tc.stateAfter)
			expectEqual(t, stateInfo.Reason, reason2)
			if stateInfo.TimestampNanosec < timeBeforeReport || stateInfo.TimestampNanosec > timeAfterReport {
				t.Errorf("Expect timestamp in [%v, %v], got %v", timeBeforeReport, timeAfterReport, stateInfo.TimestampNanosec)
			}
		})
	}
}

func TestComponentStateUpdateFails(t *testing.T) {
	for _, tc := range []struct {
		stateBefore ComponentState
		stateAfter  ComponentState
	}{
		{
			stateBefore: ComponentMinor,
			stateAfter:  ComponentInitializing,
		},
		{
			stateBefore: ComponentMinor,
			stateAfter:  ComponentUp,
		},
		{
			stateBefore: ComponentMinor,
			stateAfter:  ComponentMinor,
		},
		{
			stateBefore: ComponentError,
			stateAfter:  ComponentInitializing,
		},
		{
			stateBefore: ComponentError,
			stateAfter:  ComponentUp,
		},
		{
			stateBefore: ComponentError,
			stateAfter:  ComponentMinor,
		},
		{
			stateBefore: ComponentError,
			stateAfter:  ComponentError,
		},
		{
			stateBefore: ComponentInactive,
			stateAfter:  ComponentInitializing,
		},
		{
			stateBefore: ComponentInactive,
			stateAfter:  ComponentUp,
		},
		{
			stateBefore: ComponentInactive,
			stateAfter:  ComponentMinor,
		},
		{
			stateBefore: ComponentInactive,
			stateAfter:  ComponentError,
		},
		{
			stateBefore: ComponentInactive,
			stateAfter:  ComponentInactive,
		},
	} {
		name := "Component State Update Fails from " + tc.stateBefore.String() + " to " + tc.stateAfter.String()
		t.Run(name, func(t *testing.T) {
			clearComponentState(t)
			defer clearComponentState(t)
			ch, _ := NewComponentStateHelper(P4rt)
			defer ch.Close()
			reason1 := "Reason 1"
			timeBeforeReport := uint64(time.Now().UnixNano())
			err := ch.ReportComponentState(tc.stateBefore, reason1)
			timeAfterReport := uint64(time.Now().UnixNano())
			if err != nil {
				t.Errorf("Got error in reporting component state %v: %v", tc, err)
			}
			reason2 := "Reason 2"
			err = ch.ReportComponentState(tc.stateAfter, reason2)
			if err == nil {
				t.Errorf("Expect failure in reporting component state but got success.")
			}

			stateInfo := ch.StateInfo()
			expectEqual(t, stateInfo.State, tc.stateBefore)
			expectEqual(t, stateInfo.Reason, reason1)
			if stateInfo.TimestampNanosec < timeBeforeReport || stateInfo.TimestampNanosec > timeAfterReport {
				t.Errorf("Expect timestamp in [%v, %v], got %v", timeBeforeReport, timeAfterReport, stateInfo.TimestampNanosec)
			}

			sh, _ := NewSystemStateHelper()
			defer sh.Close()
			stateInfo = sh.AllComponentStates()[P4rt]
			expectEqual(t, stateInfo.State, tc.stateBefore)
			expectEqual(t, stateInfo.Reason, reason1)
			if stateInfo.TimestampNanosec < timeBeforeReport || stateInfo.TimestampNanosec > timeAfterReport {
				t.Errorf("Expect timestamp in [%v, %v], got %v", timeBeforeReport, timeAfterReport, stateInfo.TimestampNanosec)
			}
		})
	}
}

func TestSystemStateUpdate(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		componentState        map[SystemComponent]ComponentState
		systemState           SystemState
		systemCritical        bool
		systemCriticalReasons []string // List of possible reasons
	}{
		{
			name: "System state is initializing when any essential componentState is initializing",
			componentState: map[SystemComponent]ComponentState{
				Host:            ComponentUp,
				P4rt:            ComponentInitializing,
				Orchagent:       ComponentUp,
				Syncd:           ComponentUp,
				Telemetry:       ComponentUp,
				Linkqual:        ComponentUp,
				PlatformMonitor: ComponentUp,
				Inbandmgr:       ComponentUp,
				SwssCfgmgr:      ComponentUp,
			},
			systemState: SystemInitializing,
		},
		{
			name: "System state is up when all essential component states are up and minor",
			componentState: map[SystemComponent]ComponentState{
				Host:            ComponentUp,
				P4rt:            ComponentUp,
				Orchagent:       ComponentUp,
				Syncd:           ComponentMinor,
				Telemetry:       ComponentMinor,
				Linkqual:        ComponentInitializing,
				PlatformMonitor: ComponentUp,
				Inbandmgr:       ComponentUp,
				SwssCfgmgr:      ComponentUp,
			},
			systemState: SystemUp,
		},
		{
			name: "System state is critical when any essential component state is error",
			componentState: map[SystemComponent]ComponentState{
				Host:            ComponentUp,
				P4rt:            ComponentUp,
				Orchagent:       ComponentError,
				Syncd:           ComponentUp,
				Telemetry:       ComponentUp,
				Linkqual:        ComponentUp,
				PlatformMonitor: ComponentUp,
				Inbandmgr:       ComponentUp,
				SwssCfgmgr:      ComponentUp,
			},
			systemState:           SystemCritical,
			systemCritical:        true,
			systemCriticalReasons: []string{"swss:orchagent in state ERROR with reason: swss:orchagent reason."},
		},
		{
			name: "System state is critical when any essential component state is inactive",
			componentState: map[SystemComponent]ComponentState{
				Host:            ComponentUp,
				P4rt:            ComponentUp,
				Orchagent:       ComponentUp,
				Syncd:           ComponentInactive,
				Telemetry:       ComponentUp,
				Linkqual:        ComponentUp,
				PlatformMonitor: ComponentUp,
				Inbandmgr:       ComponentUp,
				SwssCfgmgr:      ComponentUp,
			},
			systemState:           SystemCritical,
			systemCritical:        true,
			systemCriticalReasons: []string{"Container monitor reports INACTIVE for components: syncd:syncd"},
		},
		{
			name: "System state is not critical when non-essential component state is error",
			componentState: map[SystemComponent]ComponentState{
				Host:            ComponentUp,
				P4rt:            ComponentUp,
				Orchagent:       ComponentUp,
				Syncd:           ComponentUp,
				Telemetry:       ComponentUp,
				Linkqual:        ComponentError,
				PlatformMonitor: ComponentUp,
				Inbandmgr:       ComponentUp,
				SwssCfgmgr:      ComponentUp,
			},
			systemState: SystemUp,
		},
		{
			name: "System state is not critical when non-essential component state is inactive",
			componentState: map[SystemComponent]ComponentState{
				Host:            ComponentUp,
				P4rt:            ComponentUp,
				Orchagent:       ComponentUp,
				Syncd:           ComponentUp,
				Telemetry:       ComponentUp,
				Linkqual:        ComponentInactive,
				PlatformMonitor: ComponentUp,
				Inbandmgr:       ComponentUp,
				SwssCfgmgr:      ComponentUp,
			},
			systemState: SystemUp,
		},
		{
			name: "System state is critical when multiple essential component states are error or inactive",
			componentState: map[SystemComponent]ComponentState{
				Host:            ComponentUp,
				P4rt:            ComponentUp,
				Orchagent:       ComponentUp,
				Syncd:           ComponentError,
				Telemetry:       ComponentInactive,
				Linkqual:        ComponentUp,
				PlatformMonitor: ComponentUp,
				Inbandmgr:       ComponentUp,
				SwssCfgmgr:      ComponentUp,
			},
			systemState:    SystemCritical,
			systemCritical: true,
			systemCriticalReasons: []string{
				"Container monitor reports INACTIVE for components: telemetry:telemetry",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearComponentState(t)
			defer clearComponentState(t)
			sh, err := NewSystemStateHelper()
			if err != nil {
				t.Fatalf("Failed to create NewSystemStateHelper: %v", err)
			}
			defer sh.Close()
			if sh.IsSystemCritical() || readAlarmStatus(t) {
				t.Fatalf("SystemState is critical before test!\nIsSystemCritical: %v\nAlarmStatus: %v", sh.IsSystemCritical(), readAlarmStatus(t))
			}
			for c, s := range tc.componentState {
				h, err := NewComponentStateHelper(c)
				if err != nil {
					t.Fatalf("Failed to create ComponentStateHelper: %v", err)
				}
				h.ReportComponentState(s, c.String()+" reason")
				h.Close()
				time.Sleep(100 * time.Millisecond)
			}
			time.Sleep(time.Second)

			t.Logf("\n--- System State ---\nIsSystemCritical: %v\nAlarmStatus: %v\nSystemCriticalReason: %v", sh.IsSystemCritical(), readAlarmStatus(t), sh.GetSystemCriticalReason())
			expectEqual(t, sh.GetSystemState(), tc.systemState)
			expectEqual(t, sh.IsSystemCritical(), tc.systemCritical)
			expectEqual(t, readAlarmStatus(t), tc.systemCritical)
			if len(tc.systemCriticalReasons) == 0 {
				expectEqual(t, sh.GetSystemCriticalReason(), "")
			} else {
				matchReason := false
				for _, r := range tc.systemCriticalReasons {
					if sh.GetSystemCriticalReason() == r {
						matchReason = true
						break
					}
				}
				expectEqual(t, matchReason, true)
			}
		})
	}
}

func TestReportHardwareError(t *testing.T) {
	clearComponentState(t)
	defer clearComponentState(t)
	h, _ := NewComponentStateHelper(P4rt)
	defer h.Close()
	expectEqual(t, h.ReportHardwareError(ComponentMinor, "Reason 1"), nil)
	db, err := getRedisDBClient()
	if err != nil {
		t.Fatalf("Failed to get Redis DB client :%v", err)
	}
	sep, _ := sdcfg.GetDbSeparator(dbName, "")
	values, err := db.HGetAll(context.Background(), componentStateTable+sep+P4rt.String()).Result()
	expectEqual(t, err, nil)
	found_hw_flag := false
	for key, value := range values {
		if key == "reason" {
			expectEqual(t, value, "Reason 1")
		}
		if key == "state" {
			expectEqual(t, value, ComponentMinor.String())
		}
		if key == "essential" {
			expectEqual(t, value, "true")
		}
		if key == "hw-err" {
			expectEqual(t, value, "true")
			if value == "true" {
				found_hw_flag = true
				break
			}
		}
	}
	expectEqual(t, found_hw_flag, true)
}

func TestEssentialFlagInDb(t *testing.T) {
	for _, tc := range []SystemComponent{
		Host,
		P4rt,
		Orchagent,
		Syncd,
		Telemetry,
		Linkqual,
		PlatformMonitor,
		Inbandmgr,
		SwssCfgmgr,
	} {
		name := "Essential Flag Test for " + tc.String()
		t.Run(name, func(t *testing.T) {
			clearComponentState(t)
			defer clearComponentState(t)
			h, _ := NewComponentStateHelper(tc)
			defer h.Close()
			expectEqual(t, h.ReportComponentState(ComponentUp, "Reason 1"), nil)
			db, err := getRedisDBClient()
			if err != nil {
				t.Fatalf("Failed to get Redis DB client :%v", err)
			}
			sep, _ := sdcfg.GetDbSeparator(dbName, "")
			values, err := db.HGetAll(context.Background(), componentStateTable+sep+tc.String()).Result()
			expectEqual(t, err, nil)
			found_essential_flag := false
			_, ok := EssentialComponents[tc]
			expEssentialFlag := strconv.FormatBool(ok)
			for key, value := range values {
				if key == "essential" {
					expectEqual(t, value, expEssentialFlag)
					found_essential_flag = true
				}
			}
			expectEqual(t, found_essential_flag, true)
		})
	}
}

func TestContainerMonitorSetsInactiveForEssentialComponent(t *testing.T) {
	clearComponentState(t)
	defer clearComponentState(t)
	h, _ := NewSystemStateHelper()
	defer h.Close()
	db, err := getRedisDBClient()
	if err != nil {
		t.Fatalf("Failed to get Redis DB client :%v", err)
	}
	hash := make(map[string]interface{})
	hash["state"] = "INACTIVE"
	hash["essential"] = "true"
	sep, _ := sdcfg.GetDbSeparator(dbName, "")
	if err := db.HMSet(context.Background(), componentStateTable+sep+"component1", hash).Err(); err != nil {
		t.Fatalf("Failed to write to Redis DB :%v", err)
	}
	if err := db.HMSet(context.Background(), componentStateTable+sep+"component2", hash).Err(); err != nil {
		t.Fatalf("Failed to write to Redis DB :%v", err)
	}
	time.Sleep(time.Second)
	expectEqual(t, h.IsSystemCritical(), true)
	expectEqual(t, h.GetSystemCriticalReason() == "Container monitor reports INACTIVE for components: component1, component2" ||
		h.GetSystemCriticalReason() == "Container monitor reports INACTIVE for components: component2, component1", true)
}

func TestContainerMonitorSetsInactiveForNonessentialComponent(t *testing.T) {
	clearComponentState(t)
	defer clearComponentState(t)
	h, _ := NewSystemStateHelper()
	defer h.Close()
	db, err := getRedisDBClient()
	if err != nil {
		t.Fatalf("Failed to get Redis DB client :%v", err)
	}
	hash := make(map[string]interface{})
	hash["state"] = "INACTIVE"
	sep, _ := sdcfg.GetDbSeparator(dbName, "")
	if err := db.HMSet(context.Background(), componentStateTable+sep+"component", hash).Err(); err != nil {
		t.Fatalf("Failed to write to Redis DB :%v", err)
	}

	time.Sleep(2 * time.Second)
	expectEqual(t, h.IsSystemCritical(), false)
}
