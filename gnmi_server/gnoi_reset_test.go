package gnmi

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	reset_pb "github.com/openconfig/gnoi/factory_reset"
	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	json "google.golang.org/protobuf/encoding/protojson"
	"testing"
)

const (
	factoryResetSuccess = `{"reset_success": {}}`
	factoryResetFail    = `{"reset_error": {"other": true, "detail": "Previous reset is ongoing."}}`
)

type resetTestCase struct {
	desc       string
	dbusCaller ssc.Caller
	expect     func(t *testing.T, resp *reset_pb.StartResponse, err error)
}

var resetTests = []resetTestCase{
	{
		desc:       "Successful Reset",
		dbusCaller: &ssc.FakeDbusCaller{Msg: factoryResetSuccess},
		expect: func(t *testing.T, resp *reset_pb.StartResponse, err error) {
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			if _, ok := resp.GetResponse().(*reset_pb.StartResponse_ResetSuccess); !ok {
				t.Fatalf("Expected ResetSuccess, got: %#v", resp.Response)
			}
		},
	},
	{
		desc:       "DBUS Client Error",
		dbusCaller: nil,
		expect: func(t *testing.T, _ *reset_pb.StartResponse, err error) {
			if err == nil || status.Code(err) != codes.Internal {
				t.Fatalf("Expected Internal error, got: %#v", err)
			}
		},
	},
	{
		desc:       "Reset Error from DBUS",
		dbusCaller: &ssc.FakeDbusCaller{Msg: factoryResetFail},
		expect: func(t *testing.T, resp *reset_pb.StartResponse, err error) {
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			if _, ok := resp.GetResponse().(*reset_pb.StartResponse_ResetError); !ok {
				t.Fatalf("Expected ResetError, got: %#v", resp.Response)
			}
		},
	},
}

func TestGnoiResetServer(t *testing.T) {
	for _, tc := range resetTests {
		t.Run(tc.desc, func(t *testing.T) {
			s := createServer(t, 8081)
			go runServer(t, s)
			defer s.Stop()

			// Override NewDbusClient
			orig := ssc.NewDbusClient
			defer func() { ssc.NewDbusClient = orig }()
			ssc.NewDbusClient = func(_ ssc.Caller) (ssc.Service, error) {
				if tc.dbusCaller == nil {
					return nil, errors.New("fake dbus error")
				}
				return &fakeDbusClient{msg: tc.dbusCaller.(*ssc.FakeDbusCaller).Msg}, nil
			}

			tlsConfig := &tls.Config{InsecureSkipVerify: true}
			conn, err := grpc.Dial(fmt.Sprintf("127.0.0.1:%d", s.config.Port),
				grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
			if err != nil {
				t.Fatalf("Dial failed: %v", err)
			}
			defer conn.Close()

			client := reset_pb.NewFactoryResetClient(conn)
			ctx := context.Background()
			req, err := json.Marshal(&reset_pb.StartRequest{})
			fmt.Sprintf("the StartRequest: [%s], err %v", req.String(), err)
			resp, err := client.Start(ctx, &reset_pb.StartRequest{})
			tc.expect(t, resp, err)
		})
	}
}

type fakeDbusClient struct {
	msg string
}

func (f *fakeDbusClient) Close() error                                  { return nil }
func (f *fakeDbusClient) ConfigReload(string) error                     { return nil }
func (f *fakeDbusClient) ConfigReplace(string) error                    { return nil }
func (f *fakeDbusClient) ConfigSave(string) error                       { return nil }
func (f *fakeDbusClient) ApplyPatchYang(string) error                   { return nil }
func (f *fakeDbusClient) ApplyPatchDb(string) error                     { return nil }
func (f *fakeDbusClient) CreateCheckPoint(string) error                 { return nil }
func (f *fakeDbusClient) DeleteCheckPoint(string) error                 { return nil }
func (f *fakeDbusClient) StopService(string) error                      { return nil }
func (f *fakeDbusClient) RestartService(string) error                   { return nil }
func (f *fakeDbusClient) GetFileStat(string) (map[string]string, error) { return nil, nil }
func (f *fakeDbusClient) DownloadFile(string, string, string, string, string, string) error {
	return nil
}
func (f *fakeDbusClient) RemoveFile(string) error                 { return nil }
func (f *fakeDbusClient) DownloadImage(string, string) error      { return nil }
func (f *fakeDbusClient) InstallImage(string) error               { return nil }
func (f *fakeDbusClient) ListImages() (string, error)             { return "", nil }
func (f *fakeDbusClient) ActivateImage(string) error              { return nil }
func (f *fakeDbusClient) LoadDockerImage(string) error            { return nil }
func (f *fakeDbusClient) FactoryReset(cmd string) (string, error) { return f.msg, nil }

// Make sure it implements the interface
var _ ssc.Service = (*fakeDbusClient)(nil)
