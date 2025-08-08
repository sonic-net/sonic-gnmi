package gnmi

// watermark_telemetry_interval_cli_test.go

// Tests SHOW watermark telemetry interval CLI command

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

func TestWatermarkTelemetryInterval(t *testing.T) {
	s := createServer(t, ServerPort)
	go runServer(t, s)
	defer s.ForceStop()
	defer ResetDataSetsAndMappings(t)

	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}

	conn, err := grpc.Dial(TargetAddr, opts...)
	if err != nil {
		t.Fatalf("Dailing to %q failed: %v", TargetAddr, err)
	}
	defer conn.Close()

	gClient := pb.NewGNMIClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	watermarkTelemetryIntervalDefault := `{"interval": "120"}`
	watermarkTelemetryIntervalSetFileName := "../testdata/WATERMARK_TELEMETRY_INTERVAL_SET.txt"
	watermarkTelemetryIntervalSet := `{"interval": "180"}`

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
			desc:       "query SHOW watermark telemetry interval read error",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "" >
				elem: <name: "telemetry" >
				elem: <name: "interval" >
			`,
			wantRetCode: codes.NotFound,
		},
		{
			desc:       "query SHOW watermark telemetry interval not set in CONFIG_DB",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "watermark" >
				elem: <name: "telemetry" >
				elem: <name: "interval" >
			`,
			wantRetCode: codes.OK,
			wantRespVal: []byte(watermarkTelemetryIntervalDefault),
			valTest:     true,
		},
		{
			desc:       "query SHOW watermark telemetry interval Set in CONFIG_DB",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "watermark" >
				elem: <name: "telemetry" >
				elem: <name: "interval" >
			`,
			wantRetCode: codes.OK,
			wantRespVal: []byte(watermarkTelemetryIntervalSet),
			valTest:     true,
			testInit: func() {
				FlushDataSet(t, ConfigDbNum)
				AddDataSet(t, ConfigDbNum, watermarkTelemetryIntervalSetFileName)
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
