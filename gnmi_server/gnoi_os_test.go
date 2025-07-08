package gnmi

import (
	"context"
	"crypto/tls"
	"fmt"
	log "github.com/golang/glog"
	json "google.golang.org/protobuf/encoding/protojson"
	"io/ioutil"
	"os"
	"testing"
	"time"

	ospb "github.com/openconfig/gnoi/os"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
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

			// Create a new client.
			targetAddr := "127.0.0.1:8081"
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
			dataRead, err := ioutil.ReadFile(imgPath)
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
	defer s.Stop()

	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}
	targetAddr := "127.0.0.1:8081"
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
