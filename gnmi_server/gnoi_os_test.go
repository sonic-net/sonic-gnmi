package gnmi

import (
	"context"
	"crypto/tls"
	"fmt"
	log "github.com/golang/glog"
	ospb "github.com/openconfig/gnoi/os"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	json "google.golang.org/protobuf/encoding/protojson"
	"os"
	"strings"
	"testing"
	"time"
)

type fakeOSCfg struct{}

func (f *fakeOSCfg) ProcessTrfReady(req string) (string, error) {
	return ProcessFakeTrfReady(req)
}

func (f *fakeOSCfg) ProcessTrfEnd(req string) (string, error) {
	return ProcessFakeTrfEnd(req)
}

func TestProcessTransferReq(t *testing.T) {
	server := &OSServer{
		config: &Config{
			OSCfg: &fakeOSCfg{},
		},
	}
	req := &ospb.InstallRequest{
		Request: &ospb.InstallRequest_TransferRequest{
			TransferRequest: &ospb.TransferRequest{
				Version: "os1.0",
			},
		},
	}
	resp := server.processTransferReq(req)
	if resp == nil || resp.GetTransferReady() == nil {
		t.Fatal("Expected TransferReady response")
	}
}

func TestProcessTransferReq_InvalidVersion(t *testing.T) {
	server := &OSServer{
		config: &Config{
			OSCfg: &fakeOSCfg{},
		},
	}
	req := &ospb.InstallRequest{
		Request: &ospb.InstallRequest_TransferRequest{
			TransferRequest: &ospb.TransferRequest{}, // No version
		},
	}
	resp := server.processTransferReq(req)
	if resp == nil || resp.GetInstallError() == nil {
		t.Fatal("Expected InstallError due to missing version")
	}
}

func TestProcessTransferEnd(t *testing.T) {
	server := &OSServer{
		config: &Config{
			OSCfg: &fakeOSCfg{},
		},
	}
	req := &ospb.InstallRequest{
		Request: &ospb.InstallRequest_TransferEnd{},
	}
	resp := server.processTransferEnd(req)
	if resp == nil || resp.GetValidated() == nil {
		t.Fatal("Expected Validated response")
	}
}

// ProcessFakeTrfReady responds with the TransferReady response.
func ProcessFakeTrfReady(req string) (string, error) {
	// Fake response.
	resp := &ospb.InstallResponse{
		Response: &ospb.InstallResponse_TransferReady{},
	}
	respStr, err := json.Marshal(resp)
	if err != nil {
		log.Errorln("Cannot marshal TransferReady response!")
		return "", fmt.Errorf("Cannot marshal TransferReady response!")
	}
	return string(respStr), nil
}

// ProcessFakeTrfEnd responds with the Validated response.
func ProcessFakeTrfEnd(req string) (string, error) {
	// Fake response.
	resp := &ospb.InstallResponse{
		Response: &ospb.InstallResponse_Validated{},
	}
	respStr, err := json.Marshal(resp)
	if err != nil {
		log.Errorln("Cannot marshal TransferEnd response!")
		return "", fmt.Errorf("Cannot marshal TransferEnd response!")
	}
	return string(respStr), nil
}

func TestProcessTransferReq_InvalidVersion(t *testing.T) {
	s := &OSServer{}
	req := &ospb.InstallRequest{
		Request: &ospb.InstallRequest_TransferRequest{
			TransferRequest: &ospb.TransferRequest{
				Version: "", // Invalid empty version
			},
		},
	}
	resp := s.processTransferReq(req)
	if resp == nil {
		t.Fatal("Expected response, got nil")
	}
	instErr := resp.GetInstallError()
	if instErr == nil {
		t.Fatal("Expected InstallError, got nil")
	}
	if instErr.Type != ospb.InstallError_PARSE_FAIL {
		t.Fatalf("Expected PARSE_FAIL error, got: %v", instErr.Type)
	}
	if instErr.Detail == "" {
		t.Fatal("Expected error detail message, got empty string")
	}
}

func TestHandleErrorResponse(t *testing.T) {
	resp := handleErrorResponse("install failed: %s", "corrupted file")
	if resp == nil {
		t.Fatal("Expected response, got nil")
	}
	err := resp.GetInstallError()
	if err == nil {
		t.Fatal("Expected InstallError")
	}
	if !strings.Contains(err.Detail, "install failed: corrupted file") {
		t.Fatalf("Unexpected detail: %s", err.Detail)
	}
}

func TestRemoveIncompleteTrf_FileExists(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "os_test_image_")
	if err != nil {
		t.Fatal(err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()

	server := &OSServer{}
	if !server.imageExists(tmpPath) {
		t.Fatal("Expected image to exist before deletion")
	}
	server.removeIncompleteTrf(tmpPath)
	if server.imageExists(tmpPath) {
		t.Fatal("Expected image to be deleted")
	}
}

func TestRemoveIncompleteTrf_FileDoesNotExist(t *testing.T) {
	server := &OSServer{}
	// Should not crash even if file doesn't exist
	server.removeIncompleteTrf("/tmp/nonexistent_fake_image_12345")
}

func TestProcessTransferEnd_UnmarshalError(t *testing.T) {
	server := &OSServer{
		config: &Config{
			OSCfg: &badTrfEndCfg{},
		},
	}
	req := &ospb.InstallRequest{
		Request: &ospb.InstallRequest_TransferEnd{},
	}
	resp := server.processTransferEnd(req)
	if resp.GetInstallError() == nil {
		t.Fatal("Expected InstallError from bad JSON")
	}
}

func TestProcessInstallFromBackEnd_Error(t *testing.T) {
	resp, err := ProcessInstallFromBackEnd("dummy-os-install-req")
	if err == nil {
		t.Fatal("Expected error due to missing D-Bus service")
	}
	if resp != "" {
		t.Fatalf("Expected empty string, got: %s", resp)
	}
}

// TODO(b/328077908) Alarms to be implemented later
//
//	func expectAlarm(t *testing.T) {
//		defer clearAlarm(t)
//		// Check for alarm
//		sh, sErr := common_utils.NewSystemStateHelper()
//		if sErr != nil {
//			t.Fatalf("Failed to create system state helper: %v", sErr)
//		}
//		defer sh.Close()
//		cstates := sh.AllComponentStates()
//		if state, ok := cstates[common_utils.Telemetry]; !ok || state.State != common_utils.ComponentMinor {
//			t.Fatalf("Expected ComponentMinor alarm, got: %v", state)
//		}
//		t.Logf("ComponentMinor alarm is present")
//	}
/*func clearAlarm(t *testing.T) {
	rc := getRedisClient(t, "STATE_DB")
	defer rc.Close()
	if err := rc.Del(context.Background(), "COMPONENT_STATE_TABLE|Telemetry").Err(); err != nil {
		t.Fatalf("Failed to clear component state information in DB: %v\n", err)
	}
}*/

var testOSCases = []struct {
	desc string
	f    func(ctx context.Context, t *testing.T, sc ospb.OSClient, s *OSServer)
}{
	// TODO(b/328077908) Alarms to be implemented later
	// {
	// 	desc: "OSActivateFailsAsBackEndIsUnimplemented",
	// 	f: func(ctx context.Context, t *testing.T, sc ospb.OSClient, s *OSServer) {
	// 		_, err := sc.Activate(ctx, &ospb.ActivateRequest{})
	// 		expectAlarm(err)
	// 		testErr(err, codes.Internal, "Internal SONiC HostService failure", t)
	// 	},
	// },
	// TODO(b/328077908) Alarms to be implemented later
	// {
	// 	desc: "OSVerifyFailsAsBackEndIsUnimplemented",
	// 	f: func(ctx context.Context, t *testing.T, sc ospb.OSClient, s *OSServer) {
	// 		_, err := sc.Verify(ctx, &ospb.VerifyRequest{})
	// 		expectAlarm(err)
	// 		testErr(err, codes.Internal, "Internal SONiC HostService failure", t)
	// 	},
	// },
	{
		desc: "OSInstallFailsIfTransferRequestIsMissingVersion",
		f: func(ctx context.Context, t *testing.T, sc ospb.OSClient, s *OSServer) {
			stream, err := sc.Install(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Send TransferRequest.
			err = stream.Send(&ospb.InstallRequest{
				Request: &ospb.InstallRequest_TransferRequest{},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Receive InstallError due to missing version.
			resp, err := stream.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			instErr := resp.GetInstallError()
			if instErr == nil {
				t.Fatal("Expected InstallError!")
			}
			if instErr.GetType() != ospb.InstallError_PARSE_FAIL {
				t.Fatal("Expected InstallError type: PARSE_FAIL!")
			}
			// Receive error reporting.
			_, err = stream.Recv()
			if err == nil {
				t.Fatal("Expected error!")
			}
			// TODO(b/328077908) Alarms to be implemented later
			// expectAlarm(err)
			testErr(err, codes.Aborted, "Failed to process TransferRequest.", t)
		},
	},
	{
		desc: "OSInstallFailsForConcurrentOperations",
		f: func(ctx context.Context, t *testing.T, sc ospb.OSClient, s *OSServer) {
			stream, err := sc.Install(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Send TransferRequest.
			err = stream.Send(&ospb.InstallRequest{
				Request: &ospb.InstallRequest_TransferRequest{
					TransferRequest: &ospb.TransferRequest{
						Version: "os1.1",
					},
				},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Receive TransferReady response.
			resp, err := stream.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			if resp.GetTransferReady() == nil {
				t.Fatal("Did not receive expected TransferReady response")
			}
			targetAddr := fmt.Sprintf("127.0.0.1:%d", s.config.Port)
			// Create a new client.
			tlsConfig := &tls.Config{InsecureSkipVerify: true}
			opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}
			conn, err := grpc.Dial(targetAddr, opts...)
			if err != nil {
				t.Fatalf("Dialing to %s failed: %v", targetAddr, err)
			}
			defer conn.Close()
			newsc := ospb.NewOSClient(conn)
			newctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			newstream, err := newsc.Install(newctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Receive InstallError due to Install in progress.
			resp, err = newstream.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			instErr := resp.GetInstallError()
			if instErr == nil {
				t.Fatal("Expected InstallError!")
			}
			if instErr.GetType() != ospb.InstallError_INSTALL_IN_PROGRESS {
				t.Fatal("Expected InstallError type: INSTALL_IN_PROGRESS!")
			}
			_, err = newstream.Recv()
			if err == nil {
				t.Fatal("Expected error!")
			}
			testErr(err, codes.Aborted, "Concurrent Install RPCs", t)
			// Continue with the existing stream.
			err = stream.Send(&ospb.InstallRequest{
				Request: &ospb.InstallRequest_TransferEnd{},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Receive Validated response.
			resp, err = stream.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			if resp.GetValidated() == nil {
				t.Fatal("Did not receive expected Validated response.")
			}
		},
	},
	{
		desc: "OSInstallFailsIfWrongMessageIsSent",
		f: func(ctx context.Context, t *testing.T, sc ospb.OSClient, s *OSServer) {
			stream, err := sc.Install(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Send TransferEnd; server expects TransferRequest.
			err = stream.Send(&ospb.InstallRequest{
				Request: &ospb.InstallRequest_TransferEnd{},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Receive error reporting.
			_, err = stream.Recv()
			if err == nil {
				t.Fatal("Expected error!")
			}
			// TODO(b/328077908) Alarms to be implemented later
			// expectAlarm(err)
			testErr(err, codes.InvalidArgument, "Expected TransferRequest", t)
		},
	},
	{
		desc: "OSInstallAbortedImmediately",
		f: func(ctx context.Context, t *testing.T, sc ospb.OSClient, s *OSServer) {
			stream, err := sc.Install(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Close the stream immediately.
			stream.CloseSend()
			// Receive error reporting premature closure of the stream.
			_, err = stream.Recv()
			if err == nil {
				t.Fatal("Expected an error reporting on premature closure of the stream.")
			}
			// TODO(b/328077908) Alarms to be implemented later
			// expectAlarm(err)
		},
	},
	{
		desc: "OSInstallFailsIfImageExistsWhenTransferBegins",
		f: func(ctx context.Context, t *testing.T, sc ospb.OSClient, s *OSServer) {
			stream, err := sc.Install(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Send TransferRequest.
			version := "os1.1"
			err = stream.Send(&ospb.InstallRequest{
				Request: &ospb.InstallRequest_TransferRequest{
					TransferRequest: &ospb.TransferRequest{
						Version: version,
					},
				},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Receive TransferReady response.
			resp, err := stream.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			if resp.GetTransferReady() == nil {
				t.Fatal("Did not receive expected TransferReady response")
			}
			// TransferReady initiates transferring content. Image must not exist at this point!
			imgPath := s.getVersionPath(version)
			f, err := os.OpenFile(imgPath, os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				t.Fatal(err.Error())
			}
			if err := f.Close(); err != nil {
				t.Fatal(err.Error())
			}
			// Cleanup
			defer func() {
				if err := os.Remove(imgPath); err != nil {
					t.Errorf("Error while deleting temporary test file: %v\n", err)
				}
			}()
			// Send TransferContent.
			err = stream.Send(&ospb.InstallRequest{
				Request: &ospb.InstallRequest_TransferContent{
					TransferContent: []byte("unimportant string"),
				},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Receive InstallError.
			resp, err = stream.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			instErr := resp.GetInstallError()
			if instErr == nil {
				t.Fatal("Expected InstallError!")
			}
			// Receive error reporting.
			_, err = stream.Recv()
			if err == nil {
				t.Fatal("Expected error!")
			}
			// TODO(b/328077908) Alarms to be implemented later
			// expectAlarm(err)
		},
	},
	{
		desc: "OSInstallFailsIfStreamClosesInTheMiddleOfTransfer",
		f: func(ctx context.Context, t *testing.T, sc ospb.OSClient, s *OSServer) {
			stream, err := sc.Install(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			version := "os1.1"
			// Send TransferRequest.
			err = stream.Send(&ospb.InstallRequest{
				Request: &ospb.InstallRequest_TransferRequest{
					TransferRequest: &ospb.TransferRequest{
						Version: version,
					},
				},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Receive TransferReady response.
			resp, err := stream.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			if trfReady := resp.GetTransferReady(); trfReady == nil {
				t.Fatal("Did not receive expected TransferReady response")
			}
			// Send TransferContent.
			err = stream.Send(&ospb.InstallRequest{
				Request: &ospb.InstallRequest_TransferContent{
					TransferContent: []byte("unimportant string"),
				},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Receive TransferProgress response.
			resp, err = stream.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			if trfProg := resp.GetTransferProgress(); trfProg == nil {
				t.Fatal("Did not receive expected TransferProgress response")
			}
			// Close the stream immediately.
			stream.CloseSend()
			// Receive error reporting premature closure of the stream.
			_, err = stream.Recv()
			if err == nil {
				t.Fatal("Expected an error reporting on premature closure of the stream.")
			}
			// Check incomplete transfer is removed!
			if s.imageExists(s.getVersionPath(version)) {
				t.Fatal("Incomplete image should have been deleted!")
			}
			// TODO(b/328077908) Alarms to be implemented later
			// expectAlarm(err)
		},
	},
	{
		desc: "OSInstallFailsIfWrongMsgIsSentInTheMiddleOfTransfer",
		f: func(ctx context.Context, t *testing.T, sc ospb.OSClient, s *OSServer) {
			stream, err := sc.Install(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			version := "os1.1"
			// Send TransferRequest.
			err = stream.Send(&ospb.InstallRequest{
				Request: &ospb.InstallRequest_TransferRequest{
					TransferRequest: &ospb.TransferRequest{
						Version: version,
					},
				},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Receive TransferReady response.
			resp, err := stream.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			if trfReady := resp.GetTransferReady(); trfReady == nil {
				t.Fatal("Did not receive expected TransferReady response")
			}
			// Send TransferContent.
			err = stream.Send(&ospb.InstallRequest{
				Request: &ospb.InstallRequest_TransferContent{
					TransferContent: []byte("unimportant string"),
				},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Receive TransferProgress response.
			resp, err = stream.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			if trfProg := resp.GetTransferProgress(); trfProg == nil {
				t.Fatal("Did not receive expected TransferProgress response")
			}
			// Send TransferRequest again. This is unexpected!
			// Server should send error message, clean up incomplete transfer.
			err = stream.Send(&ospb.InstallRequest{
				Request: &ospb.InstallRequest_TransferRequest{
					TransferRequest: &ospb.TransferRequest{
						Version: version,
					},
				},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Receive error reporting.
			_, err = stream.Recv()
			if err == nil {
				t.Fatal("Expected an error reporting on premature closure of the stream.")
			}
			// Check incomplete transfer is removed!
			if s.imageExists(s.getVersionPath(version)) {
				t.Fatal("Incomplete image should have been deleted!")
			}
		},
	},
	{
		desc: "OSInstallSucceeds",
		f: func(ctx context.Context, t *testing.T, sc ospb.OSClient, s *OSServer) {
			stream, err := sc.Install(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err.Error())
			}
			version := "os1.1"
			// Send TransferRequest.
			err = stream.Send(&ospb.InstallRequest{
				Request: &ospb.InstallRequest_TransferRequest{
					TransferRequest: &ospb.TransferRequest{
						Version: version,
					},
				},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Receive TransferReady response.
			resp, err := stream.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			if trfReady := resp.GetTransferReady(); trfReady == nil {
				t.Fatal("Did not receive expected TransferReady response")
			}
			data := []byte("unimportant string")
			// Send TransferContent.
			err = stream.Send(&ospb.InstallRequest{
				Request: &ospb.InstallRequest_TransferContent{
					TransferContent: data,
				},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Receive TransferProgress response.
			resp, err = stream.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			if trfProg := resp.GetTransferProgress(); trfProg == nil {
				t.Fatal("Did not receive expected TransferProgress response")
			}
			// Send TransferEnd.
			err = stream.Send(&ospb.InstallRequest{
				Request: &ospb.InstallRequest_TransferEnd{},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			// Receive Validated response.
			resp, err = stream.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			if resp.GetValidated() == nil {
				t.Fatal("Did not receive expected Validated response.")
			}
			// Sanity check!
			imgPath := s.getVersionPath(version)
			dataRead, err := os.ReadFile(imgPath)
			if err != nil {
				t.Fatal(err.Error())
			}
			if string(data) != string(dataRead) {
				t.Fatal("Content doesn't match!")
			}
			// Cleanup
			if err = os.Remove(imgPath); err != nil {
				t.Errorf("Error while deleting temporary test file: %v\n", err)
			}
		},
	},
}

// TestOSServer tests implementation of gnoi.OS server.
func TestOSServer(t *testing.T) {
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.s.Stop()
	targetAddr := "127.0.0.1:8081"
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}
	conn, err := grpc.Dial(targetAddr, opts...)
	if err != nil {
		t.Fatalf("Dialing to %s failed: %v", targetAddr, err)
	}
	defer conn.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, test := range testOSCases {
		t.Run(test.desc, func(t *testing.T) {
			conn, err := grpc.Dial(targetAddr, opts...)
			if err != nil {
				t.Fatalf("Dialing to %s failed: %v", targetAddr, err)
			}
			defer conn.Close()
			sc := ospb.NewOSClient(conn)
			test.f(ctx, t, sc, &OSServer{Server: s})
		})
	}
}
