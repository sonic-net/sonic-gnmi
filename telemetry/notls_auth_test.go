package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/alicebob/miniredis/v2"
	jwt "github.com/dgrijalva/jwt-go"
	gnoi_file_pb "github.com/openconfig/gnoi/file"
	"github.com/redis/go-redis/v9"
	"github.com/sonic-net/sonic-gnmi/common_utils"
	gnmi "github.com/sonic-net/sonic-gnmi/gnmi_server"
	"github.com/sonic-net/sonic-gnmi/pkg/interceptors"
	spb_jwt "github.com/sonic-net/sonic-gnmi/proto/gnoi/jwt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func setupNoTLSAuthTest(t *testing.T, extraArgs ...string) (*TelemetryConfig, *gnmi.Config, error) {
	t.Helper()
	originalArgs := os.Args
	originalRefresh := gnmi.JwtRefreshInt
	originalValid := gnmi.JwtValidInt
	originalCRL := gnmi.GetCrlExpireDuration()
	t.Cleanup(func() {
		os.Args = originalArgs
		gnmi.JwtRefreshInt = originalRefresh
		gnmi.JwtValidInt = originalValid
		gnmi.SetCrlExpireDuration(originalCRL)
	})
	os.Args = append([]string{"test", "-port", "8080", "-unix_socket", "",
		"-noTLS", "-bind_address", "127.0.0.1", "-gnmi_translib_write=false",
		"-gnmi_native_write=false"}, extraArgs...)
	return setupFlags(flag.NewFlagSet(t.Name(), flag.ContinueOnError))
}

func TestNoTLSAuthenticationConfiguration(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want gnmi.AuthTypes
		err  bool
	}{
		{"password", []string{"-client_auth", "password"}, gnmi.AuthTypes{"password": true}, false},
		{"jwt", []string{"-client_auth", "jwt"}, gnmi.AuthTypes{"jwt": true}, false},
		{"password and jwt", []string{"-client_auth", "password,jwt"}, gnmi.AuthTypes{"password": true, "jwt": true}, false},
		{"certificate only", []string{"-client_auth", "cert"}, nil, true},
		{"certificate only with CA", []string{"-client_auth", "cert", "-ca_crt", "unused.pem"}, nil, true},
		{"mixed certificate and password", []string{"-client_auth", "cert,password"}, gnmi.AuthTypes{"password": true}, false},
		{"mixed with CA", []string{"-client_auth", "cert,jwt", "-ca_crt", "unused.pem"}, gnmi.AuthTypes{"jwt": true}, false},
		{"explicit none", []string{"-client_auth", "none"}, gnmi.AuthTypes{}, false},
		{"readonly default", nil, gnmi.AuthTypes{}, false},
		{"translib write default", []string{"-gnmi_translib_write=true"}, gnmi.AuthTypes{"password": true, "jwt": true}, false},
		{"native write default", []string{"-gnmi_native_write=true"}, gnmi.AuthTypes{"password": true, "jwt": true}, false},
		{"UDS only", []string{"-port", "0", "-unix_socket", "/tmp/test.sock", "-client_auth", "cert"}, gnmi.AuthTypes{}, false},
		{"UDS only negative port", []string{"-port", "-1", "-unix_socket", "/tmp/test.sock", "-client_auth", "cert"}, gnmi.AuthTypes{}, false},
		{"dual listener certificate only", []string{"-unix_socket", "/tmp/test.sock", "-client_auth", "cert"}, nil, true},
		{"TLS certificate", []string{"-noTLS=false", "-insecure", "-ca_crt", "unused.pem", "-client_auth", "cert"}, gnmi.AuthTypes{"cert": true}, false},
		{"TLS without CA unchanged", []string{"-noTLS=false", "-insecure", "-client_auth", "cert"}, gnmi.AuthTypes{}, false},
		{"TLS mixed without CA", []string{"-noTLS=false", "-insecure", "-client_auth", "cert,password"}, gnmi.AuthTypes{"password": true}, false},
		{"invalid authentication mode", []string{"-client_auth", "invalid"}, nil, true},
		{"malformed boolean", []string{"-noTLS=invalid"}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			telemetryCfg, cfg, err := setupNoTLSAuthTest(t, tt.args...)
			if tt.err {
				if err == nil || telemetryCfg != nil || cfg != nil {
					t.Fatalf("invalid configuration returned (%v, %v, %v)", telemetryCfg, cfg, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, mode := range []string{"cert", "password", "jwt"} {
				if cfg.UserAuth.Enabled(mode) != tt.want.Enabled(mode) {
					t.Errorf("server authentication %q = %v, want %v", mode, cfg.UserAuth.Enabled(mode), tt.want.Enabled(mode))
				}
			}
			if cfg.UserAuth.Any() != tt.want.Any() {
				t.Errorf("server authentication enabled = %v, want %v", cfg.UserAuth.Any(), tt.want.Any())
			}
		})
	}
}

func TestNoTLSFileRPCAuthentication(t *testing.T) {
	for _, mode := range []string{"password", "jwt", "native-default", "none", "uds-only"} {
		t.Run(mode, func(t *testing.T) {
			if os.Getenv("SONIC_GNMI_NOTLS_RPC_TEST") != mode {
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNoTLSFileRPCAuthentication/"+mode+"$", "-test.v")
				cmd.Env = append(os.Environ(), "SONIC_GNMI_NOTLS_RPC_TEST="+mode)
				output, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("isolated RPC test failed: %v\n%s", err, output)
				}
				t.Logf("%s", output)
				return
			}
			extraArgs := []string{"-client_auth", mode}
			socket := filepath.Join(t.TempDir(), "gnmi.sock")
			switch mode {
			case "native-default":
				extraArgs = []string{"-gnmi_native_write=true"}
			case "uds-only":
				extraArgs = []string{"-port", "0", "-unix_socket", socket, "-client_auth", "cert"}
			}
			telemetryCfg, cfg, err := setupNoTLSAuthTest(t, extraArgs...)
			if err != nil {
				t.Fatal(err)
			}
			cfg.EnableTranslibWrite = mode != "native-default"
			cfg.EnableNativeWrite = true
			cfg.ConfigTableName = "GNMI_CLIENT_CERT"
			if mode != "uds-only" {
				cfg.Port = 0 // Let the real server allocate an isolated TCP port.
			}

			patches := gomonkey.ApplyFunc(interceptors.NewServerChain, func() (*interceptors.ServerChain, error) {
				return &interceptors.ServerChain{}, nil
			})
			db := miniredis.RunT(t)
			var dbClients []*redis.Client
			var dbMu sync.Mutex
			t.Cleanup(func() {
				for _, client := range dbClients {
					if err := client.Close(); err != nil && !errors.Is(err, redis.ErrClosed) {
						t.Error(err)
					}
				}
			})
			patches.ApplyFunc(common_utils.GetRedisDBClient, func() (*redis.Client, error) {
				client := redis.NewClient(&redis.Options{Addr: db.Addr()})
				dbMu.Lock()
				dbClients = append(dbClients, client)
				dbMu.Unlock()
				return client, nil
			})
			patches.ApplyMethod(reflect.TypeOf(&interceptors.ServerChain{}), "GetServerOptions",
				func(*interceptors.ServerChain) []grpc.ServerOption { return nil })
			patches.ApplyFunc(gnmi.UserPwAuth, func(username, password string) (bool, error) {
				if (username == "fixture" || username == "denied") && password == "fixture-password" {
					return true, nil
				}
				return false, errors.New("invalid fixture credentials")
			})
			patches.ApplyFunc(user.Lookup, func(username string) (*user.User, error) {
				if username != "fixture" && username != "denied" {
					return nil, errors.New("unknown fixture user")
				}
				return &user.User{Username: username}, nil
			})
			patches.ApplyFunc(gnmi.GetUserRoles, func(usr *user.User) ([]string, error) {
				if usr.Username == "denied" {
					return []string{"gnoi_noaccess"}, nil
				}
				return []string{"gnoi_readonly"}, nil
			})
			t.Cleanup(patches.Reset)

			ready := make(chan string, 1)
			patches.ApplyMethod(reflect.TypeOf(&gnmi.Server{}), "Address", func(*gnmi.Server) string {
				address := fmt.Sprintf("127.0.0.1:%d", cfg.Port)
				if mode == "uds-only" {
					address = "unix:" + socket
				}
				select {
				case ready <- address:
				default:
				}
				return address
			})
			stop := make(chan ServerControlValue, 1)
			signalStop := make(chan bool, 1)
			var wg sync.WaitGroup
			wg.Add(1)
			go startGNMIServer(telemetryCfg, cfg, stop, signalStop, &wg)
			t.Cleanup(func() {
				stop <- ServerStop
				done := make(chan struct{})
				go func() { wg.Wait(); close(done) }()
				select {
				case <-done:
				case <-time.After(10 * time.Second):
					t.Error("server did not stop")
				}
			})
			var address string
			select {
			case address = <-ready:
			case <-time.After(10 * time.Second):
				t.Fatal("server did not create a listener")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			conn, err := grpc.DialContext(ctx, address, grpc.WithBlock(),
				grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			client := gnoi_file_pb.NewFileClient(conn)
			check := func(ctx context.Context, code codes.Code) {
				t.Helper()
				_, err := client.Stat(ctx, &gnoi_file_pb.StatRequest{})
				if status.Code(err) != code {
					t.Fatalf("Stat returned %v, want %v", err, code)
				}
				stream, err := client.Get(ctx, &gnoi_file_pb.GetRequest{})
				if err == nil {
					_, err = stream.Recv()
				}
				if status.Code(err) != code {
					t.Fatalf("Get returned %v, want %v", err, code)
				}
			}

			if mode == "none" || mode == "uds-only" {
				check(ctx, codes.InvalidArgument)
				return
			}
			check(ctx, codes.Unauthenticated)
			if mode == "password" || mode == "native-default" {
				check(metadata.NewOutgoingContext(ctx, metadata.Pairs("username", "fixture", "password", "wrong")), codes.Unauthenticated)
				check(metadata.NewOutgoingContext(ctx, metadata.Pairs("username", "fixture", "password", "fixture-password")), codes.InvalidArgument)
				check(metadata.NewOutgoingContext(ctx, metadata.Pairs("username", "denied", "password", "fixture-password")), codes.Unknown)
				if mode == "password" {
					return
				}
			}
			authClient := spb_jwt.NewSonicJwtServiceClient(conn)
			token, err := authClient.Authenticate(ctx, &spb_jwt.AuthenticateRequest{Username: "fixture", Password: "fixture-password"})
			if err != nil {
				t.Fatal(err)
			}
			check(metadata.NewOutgoingContext(ctx, metadata.Pairs("access_token", "invalid")), codes.Unauthenticated)
			check(metadata.NewOutgoingContext(ctx, metadata.Pairs("access_token", token.Token.AccessToken)), codes.InvalidArgument)
			claims := &gnmi.Claims{Username: "fixture", StandardClaims: jwt.StandardClaims{ExpiresAt: time.Now().Add(time.Hour).Unix()}}
			forged, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(make([]byte, 16))
			if err != nil {
				t.Fatal(err)
			}
			check(metadata.NewOutgoingContext(ctx, metadata.Pairs("access_token", forged)), codes.Unauthenticated)
		})
	}
}

func TestNoTLSCertificateOnlyStartupFails(t *testing.T) {
	if os.Getenv("SONIC_GNMI_NOTLS_STARTUP_TEST") == "1" {
		os.Args = []string{"telemetry", "-logtostderr", "-port", "8080", "-noTLS",
			"-bind_address", "127.0.0.1", "-client_auth", "cert"}
		main()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNoTLSCertificateOnlyStartupFails$")
	cmd.Env = append(os.Environ(), "SONIC_GNMI_NOTLS_STARTUP_TEST=1")
	output, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("startup error = %v, want exit status 1\n%s", err, output)
	}
	if !strings.Contains(string(output), "cannot authenticate a --noTLS TCP listener") {
		t.Fatalf("missing actionable startup error:\n%s", output)
	}
}
