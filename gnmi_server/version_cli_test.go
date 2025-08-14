package gnmi

// intf_cli_test.go

// Tests SHOW interface errors

import (
	"crypto/tls"
	"errors"
	"os"
	"testing"
	"time"

	pb "github.com/openconfig/gnmi/proto/gnmi"

	"github.com/agiledragon/gomonkey/v2"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"

	show_client "github.com/sonic-net/sonic-gnmi/show_client"
)

func saveEnv(key string) func() {
	originalValue, exists := os.LookupEnv(key)
	return func() {
		if exists {
			os.Setenv(key, originalValue)
		} else {
			os.Unsetenv(key)
		}
	}
}

func TestGetShowVersionWithoutEnv(t *testing.T) {
	cleanup := saveEnv("PLATFORM")
	t.Cleanup(cleanup)

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
build_version: test_branch.1-a8fbac59d
debian_version: 11.4
kernel_version: 5.10.0-18-2-amd64
asic_type: mellanox
asic_subtype: mellanox
commit_id: a8fbac59d
branch: test_branch
release: master
libswsscommon: 1.0.0
sonic_utilities: 1.2
`
	deviceMetadataFilename := "../testdata/VERSION_METADATA.txt"
	chassisDataFilename := "../testdata/VERSION_CHASSIS.txt"

	// Mock interface error data with no errors (all zeros)
	expectedOutputWithEmptyPlat := `
{
  "sonic_software_version": "SONiC.test_branch.1-a8fbac59d",
  "sonic_os_version": "\u003cnil\u003e",
  "distribution": "Debian 11.4",
  "kernel": "5.10.0-18-2-amd64",
  "build_commit": "a8fbac59d",
  "build_date": "\u003cnil\u003e",
  "built_by": "\u003cnil\u003e",
  "platform": "test_onie_platform",
  "hwsku": "",
  "asic": "mellanox",
  "asic_count": "N/A",
  "serial_number": "",
  "model_number": "",
  "hardware_revision": "",
  "uptime": "REPOSITORYtTAGtIMAGE IDtSIZE\\ndocker-muxt20241211.35t2efd431f5493t365MB\\ndocker-muxtlatestt2efd431f5493t365MB\\ndocker-sonic-telemetryt20241211.35t58557b96ac8bt384MB\\ndocker-sonic-telemetrytlatestt58557b96ac8bt384MB\\ndocker-dhcp-servertlatestt1d51def039fbt336MB\\ndocker-macsectlatestt597ecb56d0f0t346MB\\ndocker-sonic-gnmit20241211.35t6267920ddb33t383MB\\ndocker-sonic-gnmitlatestt6267920ddb33t383MB\\ndocker-eventdt20241211.35t39b5714ba4d5t313MB\\ndocker-eventdtlatestt39b5714ba4d5t313MB\\ndocker-gbsyncd-broncost20241211.35t73c0f34db480t355MB\\ndocker-gbsyncd-broncostlatestt73c0f34db480t355MB\\ndocker-gbsyncd-credot20241211.35t180483966e58t328MB\\ndocker-gbsyncd-credotlatestt180483966e58t328MB\\ndocker-orchagentt20241211.35t396eec44c137t356MB\\ndocker-orchagenttlatestt396eec44c137t356MB\\ndocker-fpm-frrt20241211.35t6f41b20b8378t371MB\\ndocker-fpm-frrtlatestt6f41b20b8378t371MB\\ndocker-teamdt20241211.35te5f3aac7fd84t344MB\\ndocker-teamdtlatestte5f3aac7fd84t344MB\\ndocker-snmpt20241211.35t9054e290c0b2t358MB\\ndocker-snmptlatestt9054e290c0b2t358MB\\ndocker-sonic-bmpt20241211.35t7641828a420at315MB\\ndocker-sonic-bmptlatestt7641828a420at315MB\\ndocker-platform-monitort20241211.35tdd829f366fbbt434MB\\ndocker-platform-monitortlatesttdd829f366fbbt434MB\\ndocker-dhcp-relaytlatestt86d79039ff92t324MB\\ndocker-databaset20241211.35tea90783ea2fbt322MB\\ndocker-databasetlatesttea90783ea2fbt322MB\\ndocker-acmst20241211.35t4e5acdc6ed95t364MB\\ndocker-acmstlatestt4e5acdc6ed95t364MB\\ndocker-syncd-brcmt20241211.35t9f2a78a2e716t798MB\\ndocker-syncd-brcmtlatestt9f2a78a2e716t798MB\\ndocker-router-advertisert20241211.35t1d9128c91e53t314MB\\ndocker-router-advertisertlatestt1d9128c91e53t314MB\\ndocker-lldpt20241211.35t73b188910b5at359MB\\ndocker-lldptlatestt73b188910b5at359MB\\ndocker-sonic-restapit20241211.35t83aed180677et332MB\\ndocker-sonic-restapitlatestt83aed180677et332MB\\nk8s.gcr.io/pauset3.5ted210e3e4a5bt683kB",
  "date": "Fri 18 Jul 2025 18:00:00",
  "docker_info": "REPOSITORYtTAGtIMAGE IDtSIZE\\ndocker-muxt20241211.35t2efd431f5493t365MB\\ndocker-muxtlatestt2efd431f5493t365MB\\ndocker-sonic-telemetryt20241211.35t58557b96ac8bt384MB\\ndocker-sonic-telemetrytlatestt58557b96ac8bt384MB\\ndocker-dhcp-servertlatestt1d51def039fbt336MB\\ndocker-macsectlatestt597ecb56d0f0t346MB\\ndocker-sonic-gnmit20241211.35t6267920ddb33t383MB\\ndocker-sonic-gnmitlatestt6267920ddb33t383MB\\ndocker-eventdt20241211.35t39b5714ba4d5t313MB\\ndocker-eventdtlatestt39b5714ba4d5t313MB\\ndocker-gbsyncd-broncost20241211.35t73c0f34db480t355MB\\ndocker-gbsyncd-broncostlatestt73c0f34db480t355MB\\ndocker-gbsyncd-credot20241211.35t180483966e58t328MB\\ndocker-gbsyncd-credotlatestt180483966e58t328MB\\ndocker-orchagentt20241211.35t396eec44c137t356MB\\ndocker-orchagenttlatestt396eec44c137t356MB\\ndocker-fpm-frrt20241211.35t6f41b20b8378t371MB\\ndocker-fpm-frrtlatestt6f41b20b8378t371MB\\ndocker-teamdt20241211.35te5f3aac7fd84t344MB\\ndocker-teamdtlatestte5f3aac7fd84t344MB\\ndocker-snmpt20241211.35t9054e290c0b2t358MB\\ndocker-snmptlatestt9054e290c0b2t358MB\\ndocker-sonic-bmpt20241211.35t7641828a420at315MB\\ndocker-sonic-bmptlatestt7641828a420at315MB\\ndocker-platform-monitort20241211.35tdd829f366fbbt434MB\\ndocker-platform-monitortlatesttdd829f366fbbt434MB\\ndocker-dhcp-relaytlatestt86d79039ff92t324MB\\ndocker-databaset20241211.35tea90783ea2fbt322MB\\ndocker-databasetlatesttea90783ea2fbt322MB\\ndocker-acmst20241211.35t4e5acdc6ed95t364MB\\ndocker-acmstlatestt4e5acdc6ed95t364MB\\ndocker-syncd-brcmt20241211.35t9f2a78a2e716t798MB\\ndocker-syncd-brcmtlatestt9f2a78a2e716t798MB\\ndocker-router-advertisert20241211.35t1d9128c91e53t314MB\\ndocker-router-advertisertlatestt1d9128c91e53t314MB\\ndocker-lldpt20241211.35t73b188910b5at359MB\\ndocker-lldptlatestt73b188910b5at359MB\\ndocker-sonic-restapit20241211.35t83aed180677et332MB\\ndocker-sonic-restapitlatestt83aed180677et332MB\\nk8s.gcr.io/pauset3.5ted210e3e4a5bt683kB" 
}
`
	ResetDataSetsAndMappings(t)

	tests := []struct {
		desc           string
		pathTarget     string
		textPbPath     string
		wantRetCode    codes.Code
		wantRespVal    interface{}
		valTest        bool
		mockOutputFile string
		testTime       time.Time
		testInit       func()
	}{
		{
			desc:       "query SHOW version without evnironment variable",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "version" >
			`,
			wantRetCode:    codes.OK,
			wantRespVal:    []byte(expectedOutputWithEmptyPlat),
			valTest:        true,
			mockOutputFile: "../testdata/VERSION_DOCKER_IMAGEDATA.txt",
			testTime:       time.Date(2025, 7, 18, 18, 0, 0, 0, time.UTC),
			testInit: func() {
				MockReadFile(show_client.SonicVersionYamlPath, versionInfo, nil)
				MockEnvironmentVariable(t, "PLATFORM", "")
				AddDataSet(t, ConfigDbNum, deviceMetadataFilename)
				AddDataSet(t, chassisStateDbNum, chassisDataFilename)
			},
		},
	}

	for _, test := range tests {
		if test.testInit != nil {
			test.testInit()
		}
		var patches *gomonkey.Patches
		if test.mockOutputFile != "" {
			patches = MockNSEnterBGPSummary(t, test.mockOutputFile)
		}

		testTime := test.testTime
		timepatch := gomonkey.ApplyFunc(time.Now, func() time.Time {
			return testTime
		})

		fileOpenPatch := gomonkey.ApplyFunc(show_client.ReadConfToMap, func(string) (map[string]interface{}, error) {
			data := map[string]interface{}{
				"onie_platform":  "test_onie_platform",
				"aboot_platform": "test_aboot_platform",
			}
			return data, nil
		})

		asicFilePatch := gomonkey.ApplyFunc(show_client.GetAsicConfFilePath, func() string {
			return "../testdata/version_test_asics_num.conf"
		})

		platformConfigFilePatch := gomonkey.ApplyFunc(show_client.GetPlatformEnvConfFilePath, func() string {
			return "../testdata/VERSION_TEST_PLATFORM.conf"
		})

		t.Run(test.desc, func(t *testing.T) {
			runTestGet(t, ctx, gClient, test.pathTarget, test.textPbPath, test.wantRetCode, test.wantRespVal, test.valTest)
		})

		if patches != nil {
			patches.Reset()
		}
		if timepatch != nil {
			timepatch.Reset()
		}
		if fileOpenPatch != nil {
			fileOpenPatch.Reset()
		}
		if asicFilePatch != nil {
			asicFilePatch.Reset()
		}
		if platformConfigFilePatch != nil {
			platformConfigFilePatch.Reset()
		}
	}
}

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
build_version: test_branch.1-a8fbac59d
debian_version: 11.4
kernel_version: 5.10.0-18-2-amd64
asic_type: mellanox
asic_subtype: mellanox
commit_id: a8fbac59d
branch: test_branch
release: master
libswsscommon: 1.0.0
sonic_utilities: 1.2
`
	deviceMetadataFilename := "../testdata/VERSION_METADATA.txt"
	chassisDataFilename := "../testdata/VERSION_CHASSIS.txt"

	// Mock interface error data with no errors (all zeros)
	expectedOutput := `
{
  "sonic_software_version": "SONiC.test_branch.1-a8fbac59d",
  "sonic_os_version": "\u003cnil\u003e",
  "distribution": "Debian 11.4",
  "kernel": "5.10.0-18-2-amd64",
  "build_commit": "a8fbac59d",
  "build_date": "\u003cnil\u003e",
  "built_by": "\u003cnil\u003e",
  "platform": "test_onie_platform",
  "hwsku": "",
  "asic": "mellanox",
  "asic_count": "N/A",
  "serial_number": "",
  "model_number": "",
  "hardware_revision": "",
  "uptime": "REPOSITORYtTAGtIMAGE IDtSIZE\\ndocker-muxt20241211.35t2efd431f5493t365MB\\ndocker-muxtlatestt2efd431f5493t365MB\\ndocker-sonic-telemetryt20241211.35t58557b96ac8bt384MB\\ndocker-sonic-telemetrytlatestt58557b96ac8bt384MB\\ndocker-dhcp-servertlatestt1d51def039fbt336MB\\ndocker-macsectlatestt597ecb56d0f0t346MB\\ndocker-sonic-gnmit20241211.35t6267920ddb33t383MB\\ndocker-sonic-gnmitlatestt6267920ddb33t383MB\\ndocker-eventdt20241211.35t39b5714ba4d5t313MB\\ndocker-eventdtlatestt39b5714ba4d5t313MB\\ndocker-gbsyncd-broncost20241211.35t73c0f34db480t355MB\\ndocker-gbsyncd-broncostlatestt73c0f34db480t355MB\\ndocker-gbsyncd-credot20241211.35t180483966e58t328MB\\ndocker-gbsyncd-credotlatestt180483966e58t328MB\\ndocker-orchagentt20241211.35t396eec44c137t356MB\\ndocker-orchagenttlatestt396eec44c137t356MB\\ndocker-fpm-frrt20241211.35t6f41b20b8378t371MB\\ndocker-fpm-frrtlatestt6f41b20b8378t371MB\\ndocker-teamdt20241211.35te5f3aac7fd84t344MB\\ndocker-teamdtlatestte5f3aac7fd84t344MB\\ndocker-snmpt20241211.35t9054e290c0b2t358MB\\ndocker-snmptlatestt9054e290c0b2t358MB\\ndocker-sonic-bmpt20241211.35t7641828a420at315MB\\ndocker-sonic-bmptlatestt7641828a420at315MB\\ndocker-platform-monitort20241211.35tdd829f366fbbt434MB\\ndocker-platform-monitortlatesttdd829f366fbbt434MB\\ndocker-dhcp-relaytlatestt86d79039ff92t324MB\\ndocker-databaset20241211.35tea90783ea2fbt322MB\\ndocker-databasetlatesttea90783ea2fbt322MB\\ndocker-acmst20241211.35t4e5acdc6ed95t364MB\\ndocker-acmstlatestt4e5acdc6ed95t364MB\\ndocker-syncd-brcmt20241211.35t9f2a78a2e716t798MB\\ndocker-syncd-brcmtlatestt9f2a78a2e716t798MB\\ndocker-router-advertisert20241211.35t1d9128c91e53t314MB\\ndocker-router-advertisertlatestt1d9128c91e53t314MB\\ndocker-lldpt20241211.35t73b188910b5at359MB\\ndocker-lldptlatestt73b188910b5at359MB\\ndocker-sonic-restapit20241211.35t83aed180677et332MB\\ndocker-sonic-restapitlatestt83aed180677et332MB\\nk8s.gcr.io/pauset3.5ted210e3e4a5bt683kB",
  "date": "Fri 18 Jul 2025 18:00:00",
  "docker_info": "REPOSITORYtTAGtIMAGE IDtSIZE\\ndocker-muxt20241211.35t2efd431f5493t365MB\\ndocker-muxtlatestt2efd431f5493t365MB\\ndocker-sonic-telemetryt20241211.35t58557b96ac8bt384MB\\ndocker-sonic-telemetrytlatestt58557b96ac8bt384MB\\ndocker-dhcp-servertlatestt1d51def039fbt336MB\\ndocker-macsectlatestt597ecb56d0f0t346MB\\ndocker-sonic-gnmit20241211.35t6267920ddb33t383MB\\ndocker-sonic-gnmitlatestt6267920ddb33t383MB\\ndocker-eventdt20241211.35t39b5714ba4d5t313MB\\ndocker-eventdtlatestt39b5714ba4d5t313MB\\ndocker-gbsyncd-broncost20241211.35t73c0f34db480t355MB\\ndocker-gbsyncd-broncostlatestt73c0f34db480t355MB\\ndocker-gbsyncd-credot20241211.35t180483966e58t328MB\\ndocker-gbsyncd-credotlatestt180483966e58t328MB\\ndocker-orchagentt20241211.35t396eec44c137t356MB\\ndocker-orchagenttlatestt396eec44c137t356MB\\ndocker-fpm-frrt20241211.35t6f41b20b8378t371MB\\ndocker-fpm-frrtlatestt6f41b20b8378t371MB\\ndocker-teamdt20241211.35te5f3aac7fd84t344MB\\ndocker-teamdtlatestte5f3aac7fd84t344MB\\ndocker-snmpt20241211.35t9054e290c0b2t358MB\\ndocker-snmptlatestt9054e290c0b2t358MB\\ndocker-sonic-bmpt20241211.35t7641828a420at315MB\\ndocker-sonic-bmptlatestt7641828a420at315MB\\ndocker-platform-monitort20241211.35tdd829f366fbbt434MB\\ndocker-platform-monitortlatesttdd829f366fbbt434MB\\ndocker-dhcp-relaytlatestt86d79039ff92t324MB\\ndocker-databaset20241211.35tea90783ea2fbt322MB\\ndocker-databasetlatesttea90783ea2fbt322MB\\ndocker-acmst20241211.35t4e5acdc6ed95t364MB\\ndocker-acmstlatestt4e5acdc6ed95t364MB\\ndocker-syncd-brcmt20241211.35t9f2a78a2e716t798MB\\ndocker-syncd-brcmtlatestt9f2a78a2e716t798MB\\ndocker-router-advertisert20241211.35t1d9128c91e53t314MB\\ndocker-router-advertisertlatestt1d9128c91e53t314MB\\ndocker-lldpt20241211.35t73b188910b5at359MB\\ndocker-lldptlatestt73b188910b5at359MB\\ndocker-sonic-restapit20241211.35t83aed180677et332MB\\ndocker-sonic-restapitlatestt83aed180677et332MB\\nk8s.gcr.io/pauset3.5ted210e3e4a5bt683kB" 
}
`
	ResetDataSetsAndMappings(t)

	tests := []struct {
		desc           string
		pathTarget     string
		textPbPath     string
		wantRetCode    codes.Code
		wantRespVal    interface{}
		valTest        bool
		mockOutputFile string
		testTime       time.Time
		testInit       func()
	}{
		{
			desc:       "query SHOW version with evnironment variable",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "version" >
			`,
			wantRetCode:    codes.OK,
			wantRespVal:    []byte(expectedOutput),
			valTest:        true,
			mockOutputFile: "../testdata/VERSION_DOCKER_IMAGEDATA.txt",
			testTime:       time.Date(2025, 7, 18, 18, 0, 0, 0, time.UTC),
			testInit: func() {
				MockReadFile(show_client.SonicVersionYamlPath, versionInfo, nil)
				MockEnvironmentVariable(t, "PLATFORM", "dummy_platform")
				AddDataSet(t, ConfigDbNum, deviceMetadataFilename)
				AddDataSet(t, chassisStateDbNum, chassisDataFilename)
			},
		},
		{
			desc:       "query SHOW version with file yaml error",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "version" >
			`,
			wantRetCode:    codes.NotFound,
			wantRespVal:    nil,
			valTest:        true,
			mockOutputFile: "../testdata/VERSION_DOCKER_IMAGEDATA.txt",
			testTime:       time.Date(2025, 7, 18, 18, 0, 0, 0, time.UTC),
			testInit: func() {
				MockReadFile(show_client.SonicVersionYamlPath, versionInfo, errors.New("test error."))
				AddDataSet(t, ConfigDbNum, deviceMetadataFilename)
				AddDataSet(t, chassisStateDbNum, chassisDataFilename)
			},
		},
		{
			desc:       "query SHOW version with env variable and yaml error",
			pathTarget: "SHOW",
			textPbPath: `
				elem: <name: "version" >
			`,
			wantRetCode:    codes.OK,
			wantRespVal:    []byte(expectedOutput),
			valTest:        true,
			mockOutputFile: "../testdata/VERSION_DOCKER_IMAGEDATA.txt",
			testTime:       time.Date(2025, 7, 18, 18, 0, 0, 0, time.UTC),
			testInit: func() {
				MockReadFile(show_client.SonicVersionYamlPath, versionInfo, nil)
				MockEnvironmentVariable(t, "PLATFORM", "dummy_platform")
				AddDataSet(t, ConfigDbNum, deviceMetadataFilename)
				AddDataSet(t, chassisStateDbNum, chassisDataFilename)
			},
		},
	}

	for _, test := range tests {
		if test.testInit != nil {
			test.testInit()
		}
		var patches *gomonkey.Patches
		if test.mockOutputFile != "" {
			patches = MockNSEnterBGPSummary(t, test.mockOutputFile)
		}

		testTime := test.testTime
		timepatch := gomonkey.ApplyFunc(time.Now, func() time.Time {
			return testTime
		})

		t.Run(test.desc, func(t *testing.T) {
			runTestGet(t, ctx, gClient, test.pathTarget, test.textPbPath, test.wantRetCode, test.wantRespVal, test.valTest)
		})
		if patches != nil {
			patches.Reset()
		}
		if timepatch != nil {
			timepatch.Reset()
		}
	}
}
