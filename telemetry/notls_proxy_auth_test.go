package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/alicebob/miniredis/v2"
	filepb "github.com/openconfig/gnoi/file"
	ospb "github.com/openconfig/gnoi/os"
	systempb "github.com/openconfig/gnoi/system"
	"github.com/redis/go-redis/v9"
	"github.com/sonic-net/sonic-gnmi/common_utils"
	gnmi "github.com/sonic-net/sonic-gnmi/gnmi_server"
	"github.com/sonic-net/sonic-gnmi/pkg/interceptors/dpuproxy"
	jwtpb "github.com/sonic-net/sonic-gnmi/proto/gnoi/jwt"
	testcert "github.com/sonic-net/sonic-gnmi/testdata/tls"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// The backend only acknowledges fixtures; it never accesses files, packages, or hardware.
type authTestDPU struct {
	systempb.UnimplementedSystemServer
	filepb.UnimplementedFileServer
	ospb.UnimplementedOSServer
	calls atomic.Int32
}

func (d *authTestDPU) Time(context.Context, *systempb.TimeRequest) (*systempb.TimeResponse, error) {
	d.calls.Add(1)
	return &systempb.TimeResponse{Time: 1234567890}, nil
}

func (d *authTestDPU) Verify(context.Context, *ospb.VerifyRequest) (*ospb.VerifyResponse, error) {
	d.calls.Add(1)
	return &ospb.VerifyResponse{}, nil
}

func (d *authTestDPU) Activate(context.Context, *ospb.ActivateRequest) (*ospb.ActivateResponse, error) {
	d.calls.Add(1)
	return &ospb.ActivateResponse{}, nil
}

func (d *authTestDPU) Put(stream filepb.File_PutServer) error {
	d.calls.Add(1)
	for {
		if _, err := stream.Recv(); err != nil {
			if err == io.EOF {
				return stream.SendAndClose(&filepb.PutResponse{})
			}
			return err
		}
	}
}

func (d *authTestDPU) SetPackage(stream systempb.System_SetPackageServer) error {
	d.calls.Add(1)
	for {
		if _, err := stream.Recv(); err != nil {
			if err == io.EOF {
				return stream.SendAndClose(&systempb.SetPackageResponse{})
			}
			return err
		}
	}
}

func proxyAuthClientCert(t *testing.T) (tls.Certificate, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "writer"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		IsCA:        true, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, path
}

func TestNoTLSProductionProxyAuthentication(t *testing.T) {
	for _, mode := range []string{"cert", "none", "cert,password", "cert,jwt", "tls-cert", "uds-only"} {
		t.Run(mode, func(t *testing.T) {
			if os.Getenv("SONIC_GNMI_PROXY_AUTH_TEST") != mode {
				executable, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				cmd := exec.CommandContext(ctx, executable, "-test.run=^TestNoTLSProductionProxyAuthentication$/^"+regexp.QuoteMeta(mode)+"$", "-test.v", "-logtostderr")
				cmd.Env = append(os.Environ(), "SONIC_GNMI_PROXY_AUTH_TEST="+mode)
				output, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("isolated production-chain test failed: %v\n%s", err, output)
				}
				if mode == "cert" && !strings.Contains(string(output), "Keeping cert mode enabled") {
					t.Fatalf("missing certificate-only startup warning:\n%s", output)
				}
				t.Logf("%s", output)
				return
			}

			certificate, err := testcert.NewCert()
			if err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			dpu := &authTestDPU{}
			backend := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{
				Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12,
			})))
			systempb.RegisterSystemServer(backend, dpu)
			filepb.RegisterFileServer(backend, dpu)
			ospb.RegisterOSServer(backend, dpu)
			go backend.Serve(listener)
			t.Cleanup(backend.Stop)
			host, port, err := net.SplitHostPort(listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			db := miniredis.RunT(t)
			db.Select(dpuproxy.StateDB)
			db.HSet("CHASSIS_MIDPLANE_TABLE|DPU0", "ip_address", host)
			db.HSet("CHASSIS_MIDPLANE_TABLE|DPU0", "access", "True")
			db.Select(dpuproxy.ConfigDB)
			db.HSet("DPU|dpu0", "gnmi_port", port)
			patches := gomonkey.ApplyFunc(dpuproxy.NewRedisClient, func(_ string, database int) *redis.Client {
				return redis.NewClient(&redis.Options{Addr: db.Addr(), DB: database})
			})
			t.Cleanup(patches.Reset)
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
			patches.ApplyFunc(gnmi.UserPwAuth, func(username, password string) (bool, error) {
				if (username == "reader" || username == "writer" || username == "denied" || username == "roleless") && password == "fixture-password" {
					return true, nil
				}
				return false, errors.New("invalid fixture credentials")
			})
			patches.ApplyFunc(user.Lookup, func(username string) (*user.User, error) {
				return &user.User{Username: username}, nil
			})
			patches.ApplyFunc(gnmi.GetUserRoles, func(usr *user.User) ([]string, error) {
				switch usr.Username {
				case "reader":
					return []string{"gnoi_readonly"}, nil
				case "writer":
					return []string{"gnoi_readwrite"}, nil
				case "denied":
					return []string{"gnoi_readwrite", "gnoi_noaccess"}, nil
				default:
					return []string{"sonic_linux"}, nil
				}
			})
			socket := filepath.Join(t.TempDir(), "gnmi.sock")
			args := []string{"-client_auth", mode}
			var clientCert tls.Certificate
			if mode == "tls-cert" {
				var caPath string
				clientCert, caPath = proxyAuthClientCert(t)
				args = []string{"-noTLS=false", "-insecure", "-allow_no_client_auth", "-client_auth", "cert", "-ca_crt", caPath}
			} else if mode == "uds-only" {
				args = []string{"-client_auth", "cert", "-port", "0", "-unix_socket", socket}
			}
			telemetryCfg, cfg, err := setupNoTLSAuthTest(t, args...)
			if err != nil {
				t.Fatal(err)
			}
			cfg.EnableNativeWrite, cfg.EnableTranslibWrite = true, true
			if mode != "uds-only" {
				cfg.Port = 0
			}
			if mode == "cert" {
				// Exercise both listener security boundaries on the same server.
				reservation, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				cfg.Port = int64(reservation.Addr().(*net.TCPAddr).Port)
				if err := reservation.Close(); err != nil {
					t.Fatal(err)
				}
				cfg.UnixSocket = socket
			}
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
			var wg sync.WaitGroup
			wg.Add(1)
			go startGNMIServer(telemetryCfg, cfg, stop, make(chan bool, 1), &wg)
			t.Cleanup(func() {
				stop <- ServerStop
				done := make(chan struct{})
				go func() { wg.Wait(); close(done) }()
				select {
				case <-done:
				case <-time.After(10 * time.Second):
					t.Error("server did not stop")
				}
				dpuproxy.SetDefaultProxy(nil)
			})
			var address string
			select {
			case address = <-ready:
			case <-time.After(10 * time.Second):
				t.Fatal("server did not create a listener")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			dial := func(address string, cert *tls.Certificate) *grpc.ClientConn {
				t.Helper()
				transport := credentials.TransportCredentials(insecure.NewCredentials())
				if mode == "tls-cert" {
					config := &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}
					if cert != nil {
						config.Certificates = []tls.Certificate{*cert}
					}
					transport = credentials.NewTLS(config)
				}
				conn, err := grpc.DialContext(ctx, address, grpc.WithBlock(), grpc.WithTransportCredentials(transport))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { conn.Close() })
				return conn
			}
			conn := dial(address, nil)
			check := func(conn *grpc.ClientConn, requestCtx context.Context, readCode, writeCode, fileCode codes.Code) {
				t.Helper()
				routed := metadata.AppendToOutgoingContext(requestCtx,
					dpuproxy.MetadataKeyTargetType, "dpu", dpuproxy.MetadataKeyTargetIndex, "0")
				methods := []struct {
					name string
					code codes.Code
					call func() error
				}{
					{"Time", readCode, func() error {
						_, err := systempb.NewSystemClient(conn).Time(routed, &systempb.TimeRequest{})
						return err
					}},
					{"Verify", readCode, func() error { _, err := ospb.NewOSClient(conn).Verify(routed, &ospb.VerifyRequest{}); return err }},
					{"Activate", writeCode, func() error { _, err := ospb.NewOSClient(conn).Activate(routed, &ospb.ActivateRequest{}); return err }},
					{"Put", writeCode, func() error {
						stream, err := filepb.NewFileClient(conn).Put(routed)
						if err != nil {
							return err
						}
						if err := stream.Send(&filepb.PutRequest{}); err != nil && err != io.EOF {
							return err
						}
						_, err = stream.CloseAndRecv()
						return err
					}},
					{"SetPackage", writeCode, func() error {
						stream, err := systempb.NewSystemClient(conn).SetPackage(routed)
						if err != nil {
							return err
						}
						if err := stream.Send(&systempb.SetPackageRequest{}); err != nil && err != io.EOF {
							return err
						}
						_, err = stream.CloseAndRecv()
						return err
					}},
				}
				for _, method := range methods {
					before := dpu.calls.Load()
					if err := method.call(); status.Code(err) != method.code {
						t.Fatalf("%s returned %v, want %v", method.name, err, method.code)
					}
					if method.code != codes.OK && dpu.calls.Load() != before {
						t.Fatalf("rejected %s contacted the backend", method.name)
					}
					if method.code == codes.OK && dpu.calls.Load() <= before {
						t.Fatalf("authorized %s did not reach the backend", method.name)
					}
				}
				_, err := filepb.NewFileClient(conn).Stat(requestCtx, &filepb.StatRequest{})
				if status.Code(err) != fileCode {
					t.Fatalf("local File.Stat returned %v, want %v", err, fileCode)
				}
				stream, err := filepb.NewFileClient(conn).Get(requestCtx, &filepb.GetRequest{})
				if err == nil {
					_, err = stream.Recv()
				}
				if status.Code(err) != fileCode {
					t.Fatalf("local File.Get returned %v, want %v", err, fileCode)
				}
			}
			if mode == "uds-only" {
				check(conn, ctx, codes.OK, codes.OK, codes.InvalidArgument)
			} else {
				check(conn, ctx, codes.Unauthenticated, codes.Unauthenticated, codes.Unauthenticated)
			}
			if mode == "cert" {
				check(conn, metadata.AppendToOutgoingContext(ctx, "username", "writer", "password", "fixture-password"),
					codes.Unauthenticated, codes.Unauthenticated, codes.Unauthenticated)
				check(dial("unix:"+socket, nil), ctx, codes.OK, codes.OK, codes.InvalidArgument)
			} else if mode == "tls-cert" {
				check(dial(address, &clientCert), ctx, codes.OK, codes.OK, codes.InvalidArgument)
			} else if mode == "cert,password" || mode == "cert,jwt" {
				key, invalid := "password", "wrong"
				if mode == "cert,jwt" {
					key, invalid = "access_token", "invalid"
				}
				check(conn, metadata.AppendToOutgoingContext(ctx, "username", "writer", key, invalid),
					codes.Unauthenticated, codes.Unauthenticated, codes.Unauthenticated)
				for _, username := range []string{"reader", "writer", "denied", "roleless"} {
					authCtx := metadata.AppendToOutgoingContext(ctx, "username", username, "password", "fixture-password")
					if mode == "cert,jwt" {
						token, err := jwtpb.NewSonicJwtServiceClient(conn).Authenticate(ctx, &jwtpb.AuthenticateRequest{
							Username: username, Password: "fixture-password",
						})
						if err != nil {
							t.Fatal(err)
						}
						authCtx = metadata.AppendToOutgoingContext(ctx, "access_token", token.Token.AccessToken)
					}
					readCode, writeCode, fileCode := codes.OK, codes.PermissionDenied, codes.InvalidArgument
					switch username {
					case "writer":
						writeCode = codes.OK
					case "denied":
						readCode, fileCode = codes.PermissionDenied, codes.PermissionDenied
					case "roleless":
						fileCode = codes.PermissionDenied
					}
					check(conn, authCtx, readCode, writeCode, fileCode)
				}
			}
			if dpu.calls.Load() > 0 {
				connection, err := dpuproxy.GetDPUConnection(ctx, "0")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { connection.Close() })
			}
		})
	}
}
