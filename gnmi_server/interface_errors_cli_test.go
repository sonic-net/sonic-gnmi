package gnmi

// intf_cli_test.go

// Tests SHOW interface errors

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

func TestGetShowInterfaceErrors(t *testing.T) {
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

	// Mock interface error data with some errors present
	intfErrorsWithData := `[["Port Errors","Count","Last timestamp(UTC)"],["oper error status","3","2024-01-15T10:30:45Z"],["mac local fault","2","2024-01-15T10:25:30Z"],["mac remote fault","0","Never"],["fec sync loss","1","2024-01-15T10:20:15Z"],["fec alignment loss","0","Never"],["high ser error","0","Never"],["high ber error","0","Never"],["data unit crc error","5","2024-01-15T10:35:20Z"],["data unit misalignment error","0","Never"],["signal local error","0","Never"],["crc rate","0","Never"],["data unit size","0","Never"],["code group error","0","Never"],["no rx reachability","0","Never"]]`

	// Mock interface error data with no errors (all zeros)
	intfErrorsEmpty := `[["Port Errors","Count","Last timestamp(UTC)"],["oper error status","0","Never"],["mac local fault","0","Never"],["mac remote fault","0","Never"],["fec sync loss","0","Never"],["fec alignment loss","0","Never"],["high ser error","0","Never"],["high ber error","0","Never"],["data unit crc error","0","Never"],["data unit misalignment error","0","Never"],["signal local error","0","Never"],["crc rate","0","Never"],["data unit size","0","Never"],["code group error","0","Never"],["no rx reachability","0","Never"]]`

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
			desc:       "query SHOW interface errors - no data",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "interface" >
				elem: <name: "errors" key: { key: "interface" value: "Ethernet0" } >
			`,
			wantRetCode: codes.OK,
			wantRespVal: []byte(intfErrorsEmpty),
			valTest:     true,
		},
		{
			desc:       "query SHOW interface errors - with error data",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "interface" >
				elem: <name: "errors" key: { key: "interface" value: "Ethernet0" } >
			`,
			wantRetCode: codes.OK,
			wantRespVal: []byte(intfErrorsWithData),
			valTest:     true,
			testInit: func() {
				FlushDataSet(t, StateDbNum)
				// Setup PORT_OPERR_TABLE data with some errors
				AddDataSet(t, StateDbNum, "../testdata/INTERFACE_ERRORS_WITH_DATA.txt")
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
