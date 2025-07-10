package gnmi

import (
	"context"
	"crypto/tls"
	"fmt"
	"os"
	"testing"
	"time"

	log "github.com/golang/glog"
	ospb "github.com/openconfig/gnoi/os"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	json "google.golang.org/protobuf/encoding/protojson"
)

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

//func clearAlarm(t *testing.T) {
//	rc := getRedisClient(t, "STATE_DB")
//	defer rc.Close()
//	if err := rc.Del(context.Background(), "COMPONENT_STATE_TABLE|Telemetry").Err(); err != nil {
//		t.Fatalf("Failed to clear component state information in DB: %v\n", err)
//	}
//}

var testOSCases = []struct {
	desc string
	f    func(ctx context.Context, t *testing.T, sc ospb.OSClient, s *OSServer)
}{
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
	targetAddr := fmt.Sprintf("127.0.0.1:%d", s.config.Port)
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
