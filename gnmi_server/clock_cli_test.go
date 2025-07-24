package gnmi

// clock_cli_test.go

// Tests SHOW clock

import (
	"crypto/tls"
	"testing"
	"time"

	pb "github.com/openconfig/gnmi/proto/gnmi"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"

	"github.com/agiledragon/gomonkey/v2"

	show_client "github.com/sonic-net/sonic-gnmi/show_client"
)

func TestGetShowClock(t *testing.T) {
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

	tests := []struct {
		desc        string
		pathTarget  string
		textPbPath  string
		wantRetCode codes.Code
		wantRespVal interface{}
		valTest     bool
		testTime    time.Time
	}{
		{
			desc:       "query SHOW clock zero epoch",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "clock" >
			`,
			wantRetCode: codes.OK,
			wantRespVal: []byte(`{"date": "Thu Jan  1 00:00:00 UTC 1970"}`),
			valTest:     true,
			testTime:    time.Unix(0, 0).UTC(),
		},
		{
			desc:       "query SHOW clock normal",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "clock" >
			`,
			wantRetCode: codes.OK,
			wantRespVal: []byte(`{"date": "Fri Jul 18 18:00:00 UTC 2025"}`),
			valTest:     true,
			testTime:    time.Date(2025, 7, 18, 18, 0, 0, 0, time.UTC),
		},
	}

	for _, test := range tests {
		testTime := test.testTime
		patches := gomonkey.ApplyFunc(time.Now, func() time.Time {
			return testTime
		})
		t.Run(test.desc, func(t *testing.T) {
			runTestGet(t, ctx, gClient, test.pathTarget, test.textPbPath, test.wantRetCode, test.wantRespVal, test.valTest)
		})
		patches.Reset()
	}
}

func TestGetShowClockTimezones(t *testing.T) {
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

	showClockTimezonesResp := `{"timezones": ["America/Anchorage","America/Anguilla","America/Argentina/Buenos_Aires","America/Aruba","Asia/Aden","Asia/Almaty","Atlantic/Bermuda","CET","CST6CDT","Etc/GMT","Etc/GMT+0","Etc/GMT+10","Etc/GMT+2","Etc/GMT+3","Etc/GMT+4","Etc/GMT-1","Etc/GMT-10","Etc/GMT-2","Etc/GMT-3","Etc/GMT-4","UTC","Universal"]}`

	tests := []struct {
		desc        string
		pathTarget  string
		textPbPath  string
		wantRetCode codes.Code
		wantRespVal interface{}
		valTest     bool
		testDir     string
	}{
		{
			desc:       "query SHOW clock timezones error eading",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "clock" >
				elem: <name: "timezones" >
			`,
			wantRetCode: codes.NotFound,
		},
		{
			desc:       "query SHOW clock timezones",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "clock" >
				elem: <name: "timezones" >
			`,
			wantRetCode: codes.OK,
			wantRespVal: []byte(showClockTimezonesResp),
			valTest:     true,
			testDir:     "../testdata/zoneinfo",
		},
	}

	for _, test := range tests {
		testDir := test.testDir
		show_client.SetTimezonesDir(testDir)
		t.Run(test.desc, func(t *testing.T) {
			runTestGet(t, ctx, gClient, test.pathTarget, test.textPbPath, test.wantRetCode, test.wantRespVal, test.valTest)
		})
	}
}
