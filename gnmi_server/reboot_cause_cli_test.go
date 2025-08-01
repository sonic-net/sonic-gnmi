package gnmi

// reboot_cause_cli_test.go

// Tests SHOW reboot-cause and SHOW reboot-cause history

import (
	"crypto/tls"
	"testing"
	"time"

	pb "github.com/openconfig/gnmi/proto/gnmi"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"

	show_client "github.com/sonic-net/sonic-gnmi/show_client"
)

func TestGetShowRebootCause(t *testing.T) {
	s := createServer(t, ServerPort)
	go runServer(t, s)
	defer s.ForceStop()

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

	rebootCauseReboot := `{"gen_time": "2025_07_09_17_32_14", "cause": "reboot", "user": "admin", "time": "Wed Jul  9 05:28:24 PM UTC 2025", "comment": "N/A"}`
	rebootCausePowerLoss := `{"gen_time": "2025_07_10_12_19_14", "cause": "Power Loss", "user": "N/A", "time": "Thur Jul  10 00:14:24 PM UTC 2025", "comment": "N/A"}`
	rebootCauseHardware := `{"gen_time": "2025_07_08_11_53_50", "cause": "Hardware - Other (gpi-2, description: gpi 2 detailed fault, time: 2025-07-08 11:53:04)", "user": "N/A", "time": "N/A", "comment": "Unknown"}`

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
			desc:       "query SHOW reboot-cause error reading",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "reboot-cause" >
			`,
			wantRetCode: codes.NotFound,
		},
		{
			desc:       "query SHOW reboot-cause reboot",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "reboot-cause" >
			`,
			wantRetCode: codes.OK,
			wantRespVal: []byte(rebootCauseReboot),
			valTest:     true,
			testInit: func() {
				MockReadFile(show_client.PreviousRebootCauseFilePath, rebootCauseReboot, nil)
			},
		},
		{
			desc:       "query SHOW reboot-cause Power Loss",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "reboot-cause" >
			`,
			wantRetCode: codes.OK,
			wantRespVal: []byte(rebootCausePowerLoss),
			valTest:     true,
			testInit: func() {
				MockReadFile(show_client.PreviousRebootCauseFilePath, rebootCausePowerLoss, nil)
			},
		},
		{
			desc:       "query SHOW reboot-cause Hardware",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "reboot-cause" >
			`,
			wantRetCode: codes.OK,
			wantRespVal: []byte(rebootCauseHardware),
			valTest:     true,
			testInit: func() {
				MockReadFile(show_client.PreviousRebootCauseFilePath, rebootCauseHardware, nil)
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

func TestGetShowRebootCauseHistory(t *testing.T) {
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

	rebootCauseHistoryWatchdogFileName := "../testdata/REBOOT_CAUSE_WATCHDOG.txt"
	rebootCauseHistoryWatchdog := `{"2025_07_10_20_06_34":{"cause":"reboot","comment":"N/A","time":"Thu Jul 10 08:05:33 PM UTC 2025","user":"admin"},"2025_07_10_20_12_49":{"cause":"fast-reboot","comment":"N/A","time":"Thu Jul 10 08:10:49 PM UTC 2025","user":"admin"},"2025_07_10_20_19_34":{"cause":"warm-reboot","comment":"N/A","time":"Thu Jul 10 08:17:34 PM UTC 2025","user":"admin"},"2025_07_10_20_31_14":{"cause":"Watchdog (watchdog, description: Watchdog fired, time: 2025-07-10 20:30:26)","comment":"Unknown","time":"N/A","user":"N/A"},"2025_07_10_20_36_35":{"cause":"reboot","comment":"N/A","time":"Thu Jul 10 08:35:34 PM UTC 2025","user":"admin"},"2025_07_10_20_41_54":{"cause":"reboot","comment":"N/A","time":"Thu Jul 10 08:40:52 PM UTC 2025","user":"admin"},"2025_07_10_20_47_15":{"cause":"reboot","comment":"N/A","time":"Thu Jul 10 08:46:13 PM UTC 2025","user":"admin"},"2025_07_11_01_49_30":{"cause":"reboot","comment":"N/A","time":"Fri Jul 11 01:48:29 AM UTC 2025","user":"admin"},"2025_07_11_02_00_24":{"cause":"reboot","comment":"N/A","time":"Fri Jul 11 01:59:22 AM UTC 2025","user":"admin"},"2025_07_11_02_35_51":{"cause":"Unknown","comment":"N/A","time":"N/A","user":"N/A"}}`
	rebootCauseHistoryHardwareFileName := "../testdata/REBOOT_CAUSE_HARDWARE.txt"
	rebootCauseHistoryHardware := `{"2025_07_07_02_35_26":{"cause":"reboot","comment":"N/A","time":"Mon Jul  7 02:34:07 AM UTC 2025","user":"admin"},"2025_07_07_02_43_43":{"cause":"reboot","comment":"N/A","time":"Mon Jul  7 02:42:22 AM UTC 2025","user":"admin"},"2025_07_08_05_22_02":{"cause":"reboot","comment":"N/A","time":"Tue Jul  8 05:20:54 AM UTC 2025","user":"admin"},"2025_07_08_05_30_28":{"cause":"reboot","comment":"N/A","time":"Tue Jul  8 05:29:20 AM UTC 2025","user":"admin"},"2025_07_08_07_00_08":{"cause":"reboot","comment":"N/A","time":"Tue Jul  8 06:59:00 AM UTC 2025","user":"admin"},"2025_07_08_09_21_21":{"cause":"reboot","comment":"N/A","time":"Tue Jul  8 09:20:13 AM UTC 2025","user":""},"2025_07_08_09_29_39":{"cause":"reboot","comment":"N/A","time":"Tue Jul  8 09:28:30 AM UTC 2025","user":"admin"},"2025_07_08_10_13_31":{"cause":"reboot","comment":"N/A","time":"Tue Jul  8 10:12:23 AM UTC 2025","user":"admin"},"2025_07_08_11_53_50":{"cause":"Hardware - Other (gpi-2, description: gpi 2 detailed fault, time: 2025-07-08 11:53:04)","comment":"Unknown","time":"N/A","user":"N/A"},"2025_07_08_18_29_38":{"cause":"reboot","comment":"N/A","time":"Tue Jul  8 06:28:26 PM UTC 2025","user":"admin"}}`
	rebootCauseHistoryKernelFileName := "../testdata/REBOOT_CAUSE_KERNEL.txt"
	rebootCauseHistoryKernel := `{"2025_07_10_14_39_01":{"cause":"reboot","comment":"N/A","time":"Thu Jul 10 02:34:54 PM UTC 2025","user":"admin"},"2025_07_10_14_55_20":{"cause":"Kernel Panic","comment":"N/A","time":"Thu Jul 10 02:51:53 PM UTC 2025","user":"N/A"},"2025_07_10_15_13_45":{"cause":"reboot","comment":"N/A","time":"Thu Jul 10 03:09:38 PM UTC 2025","user":"admin"},"2025_07_10_15_29_52":{"cause":"reboot","comment":"N/A","time":"Thu Jul 10 03:25:44 PM UTC 2025","user":"admin"},"2025_07_10_15_45_47":{"cause":"reboot","comment":"N/A","time":"Thu Jul 10 03:41:43 PM UTC 2025","user":"admin"},"2025_07_10_18_03_28":{"cause":"reboot","comment":"N/A","time":"Thu Jul 10 05:59:18 PM UTC 2025","user":"admin"},"2025_07_10_23_33_47":{"cause":"warm-reboot","comment":"N/A","time":"Thu Jul 10 11:29:44 PM UTC 2025","user":"admin"},"2025_07_10_23_48_41":{"cause":"warm-reboot","comment":"N/A","time":"Thu Jul 10 11:44:41 PM UTC 2025","user":"admin"},"2025_07_11_00_51_52":{"cause":"warm-reboot","comment":"N/A","time":"Fri Jul 11 12:49:13 AM UTC 2025","user":"admin"},"2025_07_11_01_00_54":{"cause":"Kernel Panic","comment":"N/A","time":"Fri Jul 11 12:57:23 AM UTC 2025","user":"N/A"}}`
	rebootCauseHistoryPowerLossFileName := "../testdata/REBOOT_CAUSE_POWER_LOSS.txt"
	rebootCauseHistoryPowerLoss := `{"2025_07_09_04_44_38":{"cause":"reboot","comment":"N/A","time":"Wed Jul  9 04:41:09 AM UTC 2025","user":"admin"},"2025_07_09_05_11_26":{"cause":"reboot","comment":"N/A","time":"Wed Jul  9 05:07:59 AM UTC 2025","user":"admin"},"2025_07_09_06_52_52":{"cause":"fast-reboot","comment":"N/A","time":"Wed Jul  9 06:50:57 AM UTC 2025","user":"admin"},"2025_07_09_09_23_16":{"cause":"Power Loss","comment":"Unknown","time":"N/A","user":"N/A"},"2025_07_09_09_33_28":{"cause":"Power Loss","comment":"Unknown","time":"N/A","user":"N/A"},"2025_07_09_17_46_25":{"cause":"reboot","comment":"N/A","time":"Wed Jul  9 05:42:44 PM UTC 2025","user":"admin"},"2025_07_10_02_58_57":{"cause":"reboot","comment":"N/A","time":"Thu Jul 10 02:55:28 AM UTC 2025","user":""},"2025_07_10_05_00_09":{"cause":"Power Loss","comment":"Unknown","time":"N/A","user":"N/A"},"2025_07_10_05_27_16":{"cause":"reboot","comment":"N/A","time":"Thu Jul 10 05:23:48 AM UTC 2025","user":"admin"},"2025_07_10_06_31_24":{"cause":"Kernel Panic - Out of memory [Time: Thu Jul 10 06:28:29 AM UTC 2025]","comment":"N/A","time":"N/A","user":"N/A"}}`

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
			desc:       "query SHOW reboot-cause history NO REBOOT-CAUSE dataset",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "reboot-cause" >
				elem: <name: "history" >
			`,
			wantRetCode: codes.OK,
		},
		{
			desc:       "query SHOW reboot-cause history watchdog",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "reboot-cause" >
				elem: <name: "history" >
			`,
			wantRetCode: codes.OK,
			wantRespVal: []byte(rebootCauseHistoryWatchdog),
			valTest:     true,
			testInit: func() {
				AddDataSet(t, StateDbNum, rebootCauseHistoryWatchdogFileName)
			},
		},
		{
			desc:       "query SHOW reboot-cause history hardware",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "reboot-cause" >
				elem: <name: "history" >
			`,
			wantRetCode: codes.OK,
			wantRespVal: []byte(rebootCauseHistoryHardware),
			valTest:     true,
			testInit: func() {
				FlushDataSet(t, StateDbNum)
				AddDataSet(t, StateDbNum, rebootCauseHistoryHardwareFileName)
			},
		},
		{
			desc:       "query SHOW reboot-cause history kernel",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "reboot-cause" >
				elem: <name: "history" >
			`,
			wantRetCode: codes.OK,
			wantRespVal: []byte(rebootCauseHistoryKernel),
			valTest:     true,
			testInit: func() {
				FlushDataSet(t, StateDbNum)
				AddDataSet(t, StateDbNum, rebootCauseHistoryKernelFileName)
			},
		},
		{
			desc:       "query SHOW reboot-cause history power loss",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "reboot-cause" >
				elem: <name: "history" >
			`,
			wantRetCode: codes.OK,
			wantRespVal: []byte(rebootCauseHistoryPowerLoss),
			valTest:     true,
			testInit: func() {
				FlushDataSet(t, StateDbNum)
				AddDataSet(t, StateDbNum, rebootCauseHistoryPowerLossFileName)
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
