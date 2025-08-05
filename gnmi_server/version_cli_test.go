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

	show_client "github.com/sonic-net/sonic-gnmi/show_client"
)

func TestGetShowVersion(t *testing.T) {
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
	versionInfo := `
build_version: "20230501.01"
sonic_os_version: "4.0"
debian_version: "10"
kernel_version: "5.10.0-8-amd64"
commit_id: "abcdef123456"
build_date: "2023-05-01"
built_by: "builder"
asic_type: "dummy_asic"
`
	// From utilities.
	versionInfo = `
	---
build_version: "test_branch.1-a8fbac59d"
debian_version: "11.4"
kernel_version: "5.10.0-18-2-amd64"
asic_type: "mellanox"
asic_subtype: "mellanox"
commit_id: "a8fbac59d"
branch: "test_branch"
release: "master"
libswsscommon: "1.0.0"
sonic_utilities: "1.2"
`
	deviceMetadata := `{"device_metadata": {"asic_count": 1, "asic_type": "test_asic", "platform": "test_platform"}}`
	chassisData := `{"chassis": {"asic_count": 1, "asic_type": "test_asic", "platform": "test_platform"}}`

	// Mock interface error data with no errors (all zeros)
	expectedOutput := `
build_version: "20230501.01"
sonic_os_version: "4.0"
debian_version: "10"
kernel_version: "5.10.0-8-amd64"
commit_id: "abcdef123456"
build_date: "2023-05-01"
built_by: "builder"
asic_type: "dummy_asic"
`
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
			desc:       "query SHOW version with evnironment variable",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "version" >
			`,
			wantRetCode: codes.OK,
			wantRespVal: []byte(expectedOutput),
			valTest:     true,
			testInit: func() {
				MockReadFile(show_client.SonicVersionYamlPath, versionInfo, nil)
				MockEnvironmentVariable(t, "PLATFORM", "dummy_platform")
				AddDataSet(t, ConfigDbNum, deviceMetadata)
				AddDataSet(t, chassisStateDbNum, chassisData)
				MockGetAsicConfFilePath("../testdata/version_test_asics_num.conf")
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
