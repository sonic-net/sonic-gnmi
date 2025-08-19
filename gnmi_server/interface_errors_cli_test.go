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
	intfErrorsWithData := `[{"Port Errors": "oper error status","Count": "3","Last timestamp(UTC)": "2024-01-15T10:30:45Z"},{"Port Errors": "mac local fault","Count": "2","Last timestamp(UTC)": "2024-01-15T10:25:30Z"},{"Port Errors": "mac remote fault","Count": "0","Last timestamp(UTC)": "Never"},{"Port Errors": "fec sync loss","Count": "1","Last timestamp(UTC)": "2024-01-15T10:20:15Z"},{"Port Errors": "fec alignment loss","Count": "0","Last timestamp(UTC)": "Never"},{"Port Errors": "high ser error","Count": "0","Last timestamp(UTC)": "Never"},{"Port Errors": "high ber error","Count": "0","Last timestamp(UTC)": "Never"},{"Port Errors": "data unit crc error","Count": "5","Last timestamp(UTC)": "2024-01-15T10:35:20Z"},{"Port Errors": "data unit misalignment error","Count": "0","Last timestamp(UTC)": "Never"},{"Port Errors": "signal local error","Count": "0","Last timestamp(UTC)": "Never"},{"Port Errors": "crc rate","Count": "0","Last timestamp(UTC)": "Never"},{"Port Errors": "data unit size","Count": "0","Last timestamp(UTC)": "Never"},{"Port Errors": "code group error","Count": "0","Last timestamp(UTC)": "Never"},{"Port Errors": "no rx reachability","Count": "0","Last timestamp(UTC)": "Never"}]`

	// Mock interface error data with no errors (all zeros)
	intfErrorsEmpty := `[{"Port Errors": "oper error status","Count": "0","Last timestamp(UTC)": "Never"},{"Port Errors": "mac local fault","Count": "0","Last timestamp(UTC)": "Never"},{"Port Errors": "mac remote fault","Count": "0","Last timestamp(UTC)": "Never"},{"Port Errors": "fec sync loss","Count": "0","Last timestamp(UTC)": "Never"},{"Port Errors": "fec alignment loss","Count": "0","Last timestamp(UTC)": "Never"},{"Port Errors": "high ser error","Count": "0","Last timestamp(UTC)": "Never"},{"Port Errors": "high ber error","Count": "0","Last timestamp(UTC)": "Never"},{"Port Errors": "data unit crc error","Count": "0","Last timestamp(UTC)": "Never"},{"Port Errors": "data unit misalignment error","Count": "0","Last timestamp(UTC)": "Never"},{"Port Errors": "signal local error","Count": "0","Last timestamp(UTC)": "Never"},{"Port Errors": "crc rate","Count": "0","Last timestamp(UTC)": "Never"},{"Port Errors": "data unit size","Count": "0","Last timestamp(UTC)": "Never"},{"Port Errors": "code group error","Count": "0","Last timestamp(UTC)": "Never"},{"Port Errors": "no rx reachability","Count": "0","Last timestamp(UTC)": "Never"}]`

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
