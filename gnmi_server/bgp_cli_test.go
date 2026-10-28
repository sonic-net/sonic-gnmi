package gnmi

// Tests SHOW bgp running-config

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	pb "github.com/openconfig/gnmi/proto/gnmi"
	testcert "github.com/sonic-net/sonic-gnmi/testdata/tls"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

func TestGetBGPRunningConfig(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "gnmi.sock")
	certificate, err := testcert.NewCert()
	if err != nil {
		t.Fatalf("Loading server certificate failed: %v", err)
	}
	tlsConfig := &tls.Config{
		ClientAuth:   tls.RequestClientCert,
		Certificates: []tls.Certificate{certificate},
	}
	s, err := NewServer(&Config{
		Port:                ServerPort,
		UnixSocket:          socketPath,
		EnableTranslibWrite: true,
		EnableNativeWrite:   true,
		Threshold:           100,
		ImgDir:              "/tmp",
	}, []grpc.ServerOption{grpc.Creds(credentials.NewTLS(tlsConfig))}, nil)
	if err != nil {
		t.Fatalf("Creating server failed: %v", err)
	}
	go runServer(t, s)
	defer s.ForceStop()

	clientTLSConfig := &tls.Config{InsecureSkipVerify: true}
	tcpConn, err := grpc.Dial(TargetAddr, grpc.WithTransportCredentials(credentials.NewTLS(clientTLSConfig)))
	if err != nil {
		t.Fatalf("Dialing to %q failed: %v", TargetAddr, err)
	}
	defer tcpConn.Close()

	udsConn, err := grpc.Dial("unix://"+socketPath, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Dialing to %q failed: %v", socketPath, err)
	}
	defer udsConn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), QueryTimeout*time.Second)
	defer cancel()

	mockOutputFile := "../testdata/VTYSH_SHOW_BGP_RUNNING_CONFIG.txt"
	mockOutput, err := os.ReadFile(mockOutputFile)
	if err != nil {
		t.Fatalf("Reading %q failed: %v", mockOutputFile, err)
	}
	wantRunningConfig, err := json.Marshal(string(mockOutput))
	if err != nil {
		t.Fatalf("Marshaling mock output failed: %v", err)
	}

	tests := []struct {
		desc           string
		client         pb.GNMIClient
		wantRetCode    codes.Code
		wantRespVal    interface{}
		valTest        bool
		mockOutputFile string
		mockError      error
	}{
		{
			desc:        "deny SHOW bgp running-config over TCP",
			client:      pb.NewGNMIClient(tcpConn),
			wantRetCode: codes.PermissionDenied,
		},
		{
			desc:        "query SHOW bgp running-config read error",
			client:      pb.NewGNMIClient(udsConn),
			wantRetCode: codes.NotFound,
			mockError:   errors.New("vtysh failed"),
		},
		{
			desc:           "query SHOW bgp running-config",
			client:         pb.NewGNMIClient(udsConn),
			wantRetCode:    codes.OK,
			wantRespVal:    wantRunningConfig,
			valTest:        true,
			mockOutputFile: mockOutputFile,
		},
	}

	textPbPath := `
		elem: <name: "bgp" >
		elem: <name: "running-config" >
	`
	for _, test := range tests {
		var patches *gomonkey.Patches
		if test.mockOutputFile != "" {
			patches = MockNSEnterCommand(t, test.mockOutputFile)
		} else if test.mockError != nil {
			patches = MockNSEnterCommandError(test.mockError)
		}

		t.Run(test.desc, func(t *testing.T) {
			runTestGet(t, ctx, test.client, "SHOW", textPbPath, test.wantRetCode, test.wantRespVal, test.valTest)
		})
		if patches != nil {
			patches.Reset()
		}
	}
}

func TestContainsBGPRunningConfigPath(t *testing.T) {
	tests := []struct {
		name   string
		prefix *pb.Path
		paths  []*pb.Path
		want   bool
	}{
		{
			name:  "nil prefix",
			paths: []*pb.Path{{Elem: []*pb.PathElem{{Name: "bgp"}, {Name: "running-config"}}}},
		},
		{
			name:   "elem path",
			prefix: &pb.Path{Target: "SHOW"},
			paths:  []*pb.Path{{Elem: []*pb.PathElem{{Name: "bgp"}, {Name: "running-config"}}}},
			want:   true,
		},
		{
			name:   "split prefix",
			prefix: &pb.Path{Target: "SHOW", Elem: []*pb.PathElem{{Name: "bgp"}}},
			paths:  []*pb.Path{{Elem: []*pb.PathElem{{Name: "running-config"}}}},
			want:   true,
		},
		{
			name:   "deprecated prefix does not bypass elem path",
			prefix: &pb.Path{Target: "SHOW", Element: []string{"ignored"}},
			paths:  []*pb.Path{{Elem: []*pb.PathElem{{Name: "bgp"}, {Name: "running-config"}}}},
			want:   true,
		},
		{
			name:   "deprecated element path is not routed by SHOW client",
			prefix: &pb.Path{Target: "SHOW"},
			paths:  []*pb.Path{{Element: []string{"bgp", "running-config"}}},
		},
		{
			name:   "other SHOW path",
			prefix: &pb.Path{Target: "SHOW"},
			paths:  []*pb.Path{{Elem: []*pb.PathElem{{Name: "ipv6"}, {Name: "bgp"}, {Name: "summary"}}}},
		},
		{
			name:   "other target",
			prefix: &pb.Path{Target: "CONFIG_DB"},
			paths:  []*pb.Path{{Elem: []*pb.PathElem{{Name: "bgp"}, {Name: "running-config"}}}},
		},
		{
			name:   "sensitive path after benign path",
			prefix: &pb.Path{Target: "SHOW"},
			paths: []*pb.Path{
				{Elem: []*pb.PathElem{{Name: "clock"}}},
				{Elem: []*pb.PathElem{{Name: "bgp"}, {Name: "running-config"}}},
			},
			want: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := containsBGPRunningConfigPath(test.prefix, test.paths); got != test.want {
				t.Fatalf("containsBGPRunningConfigPath() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestGetBGPRunningConfigMixedPathEncodingDeniedOverTCP(t *testing.T) {
	ctx := peer.NewContext(context.Background(), &peer.Peer{Addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234}})
	s := &Server{config: &Config{}}
	_, err := s.Get(ctx, &pb.GetRequest{
		Prefix: &pb.Path{Target: "SHOW", Element: []string{"ignored"}},
		Path: []*pb.Path{{
			Elem: []*pb.PathElem{{Name: "bgp"}, {Name: "running-config"}},
		}},
		Encoding: pb.Encoding_JSON_IETF,
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("Get() code = %v, want PermissionDenied; err=%v", status.Code(err), err)
	}
}

func TestGetBGPRunningConfigAuthenticationPrecedesTCPDenial(t *testing.T) {
	ctx := peer.NewContext(context.Background(), &peer.Peer{Addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234}})
	s := &Server{config: &Config{UserAuth: AuthTypes{"cert": true}}}
	_, err := s.Get(ctx, &pb.GetRequest{
		Prefix: &pb.Path{Target: "SHOW"},
		Path: []*pb.Path{{
			Elem: []*pb.PathElem{{Name: "bgp"}, {Name: "running-config"}},
		}},
		Encoding: pb.Encoding_JSON_IETF,
	})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("Get() code = %v, want Unauthenticated; err=%v", status.Code(err), err)
	}
}
