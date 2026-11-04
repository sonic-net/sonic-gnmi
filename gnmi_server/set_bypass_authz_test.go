package gnmi

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/sonic-net/sonic-gnmi/common_utils"
	"github.com/sonic-net/sonic-gnmi/pkg/bypass"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

func bypassCertificateContext(commonName string) context.Context {
	ctx := metadata.NewIncomingContext(
		context.Background(),
		metadata.Pairs(bypass.MetadataKeyBypassValidation, "true"),
	)
	cert := &x509.Certificate{Subject: pkix.Name{CommonName: commonName}}
	return peer.NewContext(ctx, &peer.Peer{
		AuthInfo: credentials.TLSInfo{
			State: tls.ConnectionState{
				VerifiedChains: [][]*x509.Certificate{{cert}},
			},
		},
	})
}

func TestNativeSetTarget(t *testing.T) {
	tests := []struct {
		name   string
		prefix *gnmipb.Path
		paths  []*gnmipb.Path
		want   string
	}{
		{
			name:   "prefix target",
			prefix: &gnmipb.Path{Origin: "sonic-db", Target: "CONFIG_DB"},
			paths:  []*gnmipb.Path{{Elem: []*gnmipb.PathElem{{Name: "VNET"}, {Name: "blue"}}}},
			want:   "CONFIG_DB",
		},
		{
			name:   "database in path",
			prefix: &gnmipb.Path{Origin: "sonic-db"},
			paths: []*gnmipb.Path{{Elem: []*gnmipb.PathElem{
				{Name: "CONFIG_DB"},
				{Name: "localhost"},
				{Name: "VNET"},
				{Name: "blue"},
			}}},
			want: "CONFIG_DB",
		},
		{
			name:   "conflicting databases",
			prefix: &gnmipb.Path{Origin: "sonic-db"},
			paths: []*gnmipb.Path{
				{Elem: []*gnmipb.PathElem{{Name: "CONFIG_DB"}, {Name: "localhost"}, {Name: "VNET"}}},
				{Elem: []*gnmipb.PathElem{{Name: "APPL_DB"}, {Name: "localhost"}, {Name: "VNET"}}},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := nativeSetTarget(test.prefix, test.paths); got != test.want {
				t.Fatalf("nativeSetTarget() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestSetBypassCertificateAuthorization(t *testing.T) {
	rolesByCommonName := map[string][]string{
		"mapped-readwrite": {"gnmi_config_db_readwrite"},
		"mapped-readonly":  {"gnmi_config_db_readonly"},
	}
	backend := map[string]string{}
	backendCalls := 0

	patches := gomonkey.NewPatches()
	defer patches.Reset()
	patches.ApplyFunc(PopulateAuthStructByCommonName, func(commonName string, auth *common_utils.AuthInfo, _ string) error {
		roles, ok := rolesByCommonName[commonName]
		if !ok {
			return status.Error(codes.Unauthenticated, "certificate identity is not mapped")
		}
		auth.User = commonName
		auth.Roles = append([]string(nil), roles...)
		return nil
	})
	patches.ApplyFunc(bypass.TrySet, func(_ context.Context, prefix *gnmipb.Path, _ []*gnmipb.Path, _ []*gnmipb.Update) (*gnmipb.SetResponse, bool, error) {
		backendCalls++
		backend["VNET|blue"] = "1000"
		return &gnmipb.SetResponse{Prefix: prefix}, true, nil
	})

	server := &Server{
		config: &Config{
			EnableNativeWrite: true,
			ConfigTableName:   "GNMI_CLIENT_CERT",
			UserAuth:          AuthTypes{"cert": true},
		},
		SaveStartupConfig: saveOnSetDisabled,
		ReqFromMaster:     ReqFromMasterDisabledMA,
	}
	request := &gnmipb.SetRequest{
		Prefix: &gnmipb.Path{Origin: "sonic-db", Target: "CONFIG_DB"},
		Update: []*gnmipb.Update{{
			Path: &gnmipb.Path{
				Elem: []*gnmipb.PathElem{
					{Name: "VNET"},
					{Name: "blue"},
				},
			},
		}},
	}

	tests := []struct {
		name             string
		commonName       string
		wantErr          bool
		wantCode         codes.Code
		wantBackendCalls int
	}{
		{
			name:             "mapped read-write success",
			commonName:       "mapped-readwrite",
			wantBackendCalls: 1,
		},
		{
			name:       "mapped read-only denial",
			commonName: "mapped-readonly",
			wantErr:    true,
		},
		{
			name:       "unmapped identity denial",
			commonName: "unknown",
			wantErr:    true,
			wantCode:   codes.Unauthenticated,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clear(backend)
			backendCalls = 0

			response, err := server.Set(bypassCertificateContext(test.commonName), request)
			if test.wantErr {
				if err == nil {
					t.Fatal("Set() succeeded; want access denial")
				}
				if test.wantCode != codes.OK && status.Code(err) != test.wantCode {
					t.Fatalf("Set() error code = %s, want %s", status.Code(err), test.wantCode)
				}
				if response != nil {
					t.Fatalf("Set() response = %v, want nil after denial", response)
				}
			} else {
				if err != nil {
					t.Fatalf("Set() error = %v, want nil", err)
				}
				if response == nil {
					t.Fatal("Set() response is nil after authorized bypass")
				}
			}

			if backendCalls != test.wantBackendCalls {
				t.Fatalf("bypass backend calls = %d, want %d", backendCalls, test.wantBackendCalls)
			}
			_, changed := backend["VNET|blue"]
			if changed != (test.wantBackendCalls == 1) {
				t.Fatalf("bypass backend changed = %t, want %t", changed, test.wantBackendCalls == 1)
			}
		})
	}
}
