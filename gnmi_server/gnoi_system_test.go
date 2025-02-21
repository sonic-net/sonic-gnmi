package gnmi

// server_test covers gNMI get, subscribe (stream and poll) test
// Prerequisite: redis-server should be running.
import (
	"crypto/tls"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/agiledragon/gomonkey/v2"

	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"
	gnoi_common_pb "github.com/openconfig/gnoi/common"
	gnoi_system_pb "github.com/openconfig/gnoi/system"
)

func TestSetPackage(t *testing.T) {
	s := createServer(t, 8089)
	go runServer(t, s)
	defer s.Stop()

	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}

	targetAddr := "127.0.0.1:8089"
	conn, err := grpc.Dial(targetAddr, opts...)
	if err != nil {
		t.Fatalf("Dialing to %q failed: %v", targetAddr, err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()

	t.Run("SetPackageSuccess", func(t *testing.T) {
		mockClient := &ssc.DbusClient{}
		mock := gomonkey.ApplyMethod(reflect.TypeOf(&ssc.DbusClient{}), "DownloadImage", func(_ *ssc.DbusClient, path, filename string) error {
			return nil
		})
		defer mock.Reset()

		mock = gomonkey.ApplyMethod(reflect.TypeOf(mockClient), "InstallImage", func(_ *ssc.DbusClient, filename string) error {
			return nil
		})
		defer mock.Reset()

		spc := gnoi_system_pb.NewSystemClient(conn)
		stream, err := spc.SetPackage(ctx)
		if err != nil {
			t.Fatalf("SetPackage failed: %v", err)
		}

		req := &gnoi_system_pb.SetPackageRequest{
			Request: &gnoi_system_pb.SetPackageRequest_Package{
				Package: &gnoi_system_pb.Package{
					RemoteDownload: &gnoi_common_pb.RemoteDownload{
						Path: "http://example.com/package",
					},
					Filename: "package.bin",
				},
			},
		}

		err = stream.Send(req)
		if err != nil {
			t.Fatalf("Failed to send request: %v", err)
		}

		_, err = stream.CloseAndRecv()
		if err != nil {
			t.Fatalf("SetPackage failed: %v", err)
		}
	})

	t.Run("SetPackageDownloadFailure", func(t *testing.T) {
		mockClient := &ssc.DbusClient{}
		expectedError := fmt.Errorf("failed to download image")

		mock := gomonkey.ApplyMethod(reflect.TypeOf(mockClient), "DownloadImage", func(_ *ssc.DbusClient, path, filename string) error {
			return expectedError
		})
		defer mock.Reset()

		spc := gnoi_system_pb.NewSystemClient(conn)
		stream, err := spc.SetPackage(ctx)
		if err != nil {
			t.Fatalf("SetPackage failed: %v", err)
		}

		req := &gnoi_system_pb.SetPackageRequest{
			Request: &gnoi_system_pb.SetPackageRequest_Package{
				Package: &gnoi_system_pb.Package{
					RemoteDownload: &gnoi_common_pb.RemoteDownload{
						Path: "http://example.com/package",
					},
					Filename: "package.bin",
				},
			},
		}

		err = stream.Send(req)
		if err != nil {
			t.Fatalf("Failed to send request: %v", err)
		}

		_, err = stream.CloseAndRecv()
		if err == nil {
			t.Fatalf("Expected error but got none")
		}
		if !strings.Contains(err.Error(), expectedError.Error()) {
			t.Errorf("Expected error to contain '%v' but got '%v'", expectedError, err)
		}
	})

	t.Run("SetPackageInstallFailure", func(t *testing.T) {
		mockClient := &ssc.DbusClient{}
		expectedError := fmt.Errorf("failed to install image")

		mock := gomonkey.ApplyMethod(reflect.TypeOf(mockClient), "DownloadImage", func(_ *ssc.DbusClient, path, filename string) error {
			return nil
		})
		defer mock.Reset()

		mock = gomonkey.ApplyMethod(reflect.TypeOf(mockClient), "InstallImage", func(_ *ssc.DbusClient, filename string) error {
			return expectedError
		})
		defer mock.Reset()

		spc := gnoi_system_pb.NewSystemClient(conn)
		stream, err := spc.SetPackage(ctx)
		if err != nil {
			t.Fatalf("SetPackage failed: %v", err)
		}

		req := &gnoi_system_pb.SetPackageRequest{
			Request: &gnoi_system_pb.SetPackageRequest_Package{
				Package: &gnoi_system_pb.Package{
					RemoteDownload: &gnoi_common_pb.RemoteDownload{
						Path: "http://example.com/package",
					},
					Filename: "package.bin",
				},
			},
		}

		err = stream.Send(req)
		if err != nil {
			t.Fatalf("Failed to send request: %v", err)
		}

		_, err = stream.CloseAndRecv()
		if err == nil {
			t.Fatalf("Expected error but got none")
		}
		if !strings.Contains(err.Error(), expectedError.Error()) {
			t.Errorf("Expected error to contain '%v' but got '%v'", expectedError, err)
		}
	})

	t.Run("SetPackageRemoteDownloadInfoMissing", func(t *testing.T) {
		mockClient := &ssc.DbusClient{}
		mock := gomonkey.ApplyMethod(reflect.TypeOf(mockClient), "DownloadImage", func(_ *ssc.DbusClient, path, filename string) error {
			return nil
		})
		defer mock.Reset()

		mock = gomonkey.ApplyMethod(reflect.TypeOf(mockClient), "InstallImage", func(_ *ssc.DbusClient, filename string) error {
			return nil
		})
		defer mock.Reset()

		spc := gnoi_system_pb.NewSystemClient(conn)
		stream, err := spc.SetPackage(ctx)
		if err != nil {
			t.Fatalf("SetPackage failed: %v", err)
		}

		req := &gnoi_system_pb.SetPackageRequest{
			Request: &gnoi_system_pb.SetPackageRequest_Package{
				Package: &gnoi_system_pb.Package{
					Filename: "package.bin",
				},
			},
		}

		err = stream.Send(req)
		if err != nil {
			t.Fatalf("Failed to send request: %v", err)
		}

		_, err = stream.CloseAndRecv()
		if err == nil {
			t.Fatalf("Expected error but got none")
		}
		if !strings.Contains(err.Error(), "RemoteDownload information is missing") {
			t.Errorf("Expected error to contain 'RemoteDownload information is missing' but got '%v'", err)
		}
	})

	t.Run("SetPackageFailToReceiveRequest", func(t *testing.T) {
		mockClient := &ssc.DbusClient{}
		mock := gomonkey.ApplyMethod(reflect.TypeOf(mockClient), "DownloadImage", func(_ *ssc.DbusClient, path, filename string) error {
			return nil
		})
		defer mock.Reset()

		mock = gomonkey.ApplyMethod(reflect.TypeOf(mockClient), "InstallImage", func(_ *ssc.DbusClient, filename string) error {
			return nil
		})
		defer mock.Reset()

		spc := gnoi_system_pb.NewSystemClient(conn)
		stream, err := spc.SetPackage(ctx)
		if err != nil {
			t.Fatalf("SetPackage failed: %v", err)
		}

		// Simulate failure to receive request
		stream.CloseSend()

		_, err = stream.CloseAndRecv()
		if err == nil {
			t.Fatalf("Expected error but got none")
		}
	})
}