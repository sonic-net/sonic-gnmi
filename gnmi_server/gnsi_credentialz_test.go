package gnmi

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redismock/v9"
	log "github.com/golang/glog"
	"github.com/google/go-cmp/cmp"
	credz "github.com/openconfig/gnsi/credentialz"
	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"
	testcert "github.com/sonic-net/sonic-gnmi/testdata/tls"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

const (
	sshMetaPathTest     = "../testdata/gnsi/ssh-version.json"
	consoleMetaPathTest = "../testdata/gnsi/console-version.json"
	stateDbKeyForGlome  = "CREDENTIALS|GLOME_CONFIG"

	expectedSshCreateCmd          = ssc.NamePrefix + "ssh_mgmt" + string(ssc.CredzCPCreate)
	expectedSshDeleteCmd          = ssc.NamePrefix + "ssh_mgmt" + string(ssc.CredzCPDelete)
	expectedSshRestoreCmd         = ssc.NamePrefix + "ssh_mgmt" + string(ssc.CredzCPRestore)
	expectedSshSetCmd             = ssc.NamePrefix + "ssh_mgmt.set"
	expectedConsoleCreateCmd      = ssc.NamePrefix + "gnsi_console" + string(ssc.CredzCPCreate)
	expectedConsoleDeleteCmd      = ssc.NamePrefix + "gnsi_console" + string(ssc.CredzCPDelete)
	expectedConsoleRestoreCmd     = ssc.NamePrefix + "gnsi_console" + string(ssc.CredzCPRestore)
	expectedConsoleSetCmd         = ssc.NamePrefix + "gnsi_console.set"
	expectedGlomePushConfigCmd    = ssc.NamePrefix + "glome" + string(ssc.CredzGlomePushConfig) // Used for both create checkpoint and set new glome config.
	expectedGlomeConfigRestoreCmd = ssc.NamePrefix + "glome" + string(ssc.CredzCPRestore)

	expectedValidGlomeRequestInJson   = `{"enabled":true,"key":"test-key","key_version":1,"url_prefix":"https://test.com/"}`
	expectedDisableGlomeRequestInJson = `{"enabled":false,"key":"","key_version":0,"url_prefix":""}`
)

var (
	// glomePushConfigDbusMessageForValidRequest is the expected D-Bus message for pushing a valid GLOME config.
	glomePushConfigDbusMessageForValidRequest = mockDBusMessage{
		methodName: expectedGlomePushConfigCmd,
		cmd:        expectedValidGlomeRequestInJson,
	}

	// glomePushConfigDbusMessageForDisableRequest is the expected D-Bus message for pushing a config for disabling GLOME.
	glomePushConfigDbusMessageForDisableRequest = mockDBusMessage{
		methodName: expectedGlomePushConfigCmd,
		cmd:        expectedDisableGlomeRequestInJson,
	}

	// glomeRestoreDbusMessage is the expected D-Bus message for restoring the GLOME config to the
	// checkpoint state.
	glomeRestoreDbusMessage = mockDBusMessage{
		methodName: expectedGlomeConfigRestoreCmd,
	}

	// validRotateHostParametersGlomeRequest is a valid RotateHostParametersRequest for GLOME.
	validRotateHostParametersGlomeRequest = &credz.RotateHostParametersRequest{
		Request: &credz.RotateHostParametersRequest_Glome{
			Glome: &credz.GlomeRequest{
				Enabled:    true,
				Key:        "test-key",
				KeyVersion: 1,
				UrlPrefix:  "https://test.com/",
			},
		},
	}

	// rotateHostParametersDisableGlomeRequest is a RotateHostParametersRequest for disabling GLOME.
	rotateHostParametersDisableGlomeRequest = &credz.RotateHostParametersRequest{
		Request: &credz.RotateHostParametersRequest_Glome{
			Glome: &credz.GlomeRequest{
				Enabled: false,
			},
		},
	}
)

func createCredzServer(t *testing.T, cfg *Config) *Server {
	t.Helper()
	certificate, err := testcert.NewCert()
	if err != nil {
		t.Fatalf("could not load server key pair: %s", err)
	}
	tlsCfg := &tls.Config{
		ClientAuth:   tls.RequestClientCert,
		Certificates: []tls.Certificate{certificate},
	}

	opts := []grpc.ServerOption{grpc.Creds(credentials.NewTLS(tlsCfg))}
	s, err := NewServer(cfg, opts)
	if err != nil {
		t.Fatalf("Failed to create gNMI server: %v", err)
	}
	return s
}

var sshTests = []struct {
	desc string
	f    func(t *testing.T, ctx context.Context, sc credz.CredentialzClient, ch <-chan []string)
}{
	{
		desc: "Unimplemented ServerKeys",
		f: func(t *testing.T, ctx context.Context, sc credz.CredentialzClient, ch <-chan []string) {
			c, err := sc.RotateHostParameters(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err = c.Send(&credz.RotateHostParametersRequest{
				Request: &credz.RotateHostParametersRequest_ServerKeys{}}); err != nil {
				t.Fatal(err)
			}
			// Check that the client receives an Unimplemented error.
			if resp, err := c.Recv(); status.Code(err) != codes.Unimplemented || resp != nil {
				t.Fatalf("expected Unimplemented error ; got resp: %v, err: %v", resp, err)
			}
		},
	},
	{
		desc: "Unimplemented GenerateKeys",
		f: func(t *testing.T, ctx context.Context, sc credz.CredentialzClient, ch <-chan []string) {
			c, err := sc.RotateHostParameters(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err = c.Send(&credz.RotateHostParametersRequest{
				Request: &credz.RotateHostParametersRequest_GenerateKeys{}}); err != nil {
				t.Fatal(err)
			}
			// Check that the client receives an Unimplemented error.
			if resp, err := c.Recv(); status.Code(err) != codes.Unimplemented || resp != nil {
				t.Fatalf("expected Unimplemented error ; got resp: %v, err: %v", resp, err)
			}
		},
	},
	{
		desc: "Unimplemented AuthenticationAllowed",
		f: func(t *testing.T, ctx context.Context, sc credz.CredentialzClient, ch <-chan []string) {
			c, err := sc.RotateHostParameters(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err = c.Send(&credz.RotateHostParametersRequest{
				Request: &credz.RotateHostParametersRequest_AuthenticationAllowed{}}); err != nil {
				t.Fatal(err)
			}
			// Check that the client receives an Unimplemented error.
			if resp, err := c.Recv(); status.Code(err) != codes.Unimplemented || resp != nil {
				t.Fatalf("expected Unimplemented error ; got resp: %v, err: %v", resp, err)
			}
		},
	},
	{
		desc: "Unimplemented AuthorizedPrincipalCheck",
		f: func(t *testing.T, ctx context.Context, sc credz.CredentialzClient, ch <-chan []string) {
			c, err := sc.RotateHostParameters(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err = c.Send(&credz.RotateHostParametersRequest{
				Request: &credz.RotateHostParametersRequest_AuthorizedPrincipalCheck{}}); err != nil {
				t.Fatal(err)
			}
			// Check that the client receives an Unimplemented error.
			if resp, err := c.Recv(); status.Code(err) != codes.Unimplemented || resp != nil {
				t.Fatalf("expected Unimplemented error ; got resp: %v, err: %v", resp, err)
			}
		},
	},
	{
		desc: "User scenario: keys, users, finalize",
		f: func(t *testing.T, ctx context.Context, sc credz.CredentialzClient, ch <-chan []string) {
			c, err := sc.RotateAccountCredentials(ctx)
			if err != nil {
				t.Fatal(err)
			}
			err = c.Send(&credz.RotateAccountCredentialsRequest{
				Request: &credz.RotateAccountCredentialsRequest_Credential{
					Credential: &credz.AuthorizedKeysRequest{
						Credentials: []*credz.AccountCredentials{
							{
								Account:   "root",
								Version:   "root-version-1",
								CreatedOn: 123,
								AuthorizedKeys: []*credz.AccountCredentials_AuthorizedKey{
									{
										AuthorizedKey: []byte("Authorized-key #1"),
										Options: []*credz.Option{
											{
												Key:   &credz.Option_Name{Name: "from"},
												Value: "*.sales.example.net,!pc.sales.example.net",
											},
										},
									},
									{
										AuthorizedKey: []byte("Authorized-key #2"),
									},
								},
							},
						},
					},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			resp, err := c.Recv()
			if err != nil {
				t.Fatal(err)
			}
			if resp.GetResponse() == nil {
				t.Fatal("expected a message")
			}

			if dbus := dbusListen(ch); !reflect.DeepEqual(dbus, []string{expectedSshCreateCmd, ""}) {
				t.Fatalf("DBUS Failure wanted: [%v ]; got: %+v", expectedSshCreateCmd, dbus)
			}

			if dbus := dbusListen(ch); !reflect.DeepEqual(dbus, []string{expectedSshSetCmd, `[{ "SshAccountKeys": [ { "account": "root", "keys": [ { "key" : "unspecified QXV0aG9yaXplZC1rZXkgIzE= ", "options" : [ { "name" : "from", "value": "*.sales.example.net,!pc.sales.example.net" } ] }, { "key" : "unspecified QXV0aG9yaXplZC1rZXkgIzI= ", "options" : [ ] } ] } ] }]`}) {
				t.Fatalf("DBUS Failure wanted ssh_mgmt.set; got: %v", dbus)
			}

			err = c.Send(&credz.RotateAccountCredentialsRequest{
				Request: &credz.RotateAccountCredentialsRequest_User{
					User: &credz.AuthorizedUsersRequest{
						Policies: []*credz.UserPolicy{
							{
								Account:   "root",
								Version:   "root-version-2",
								CreatedOn: 123,
								AuthorizedPrincipals: &credz.UserPolicy_SshAuthorizedPrincipals{
									AuthorizedPrincipals: []*credz.UserPolicy_SshAuthorizedPrincipal{
										&credz.UserPolicy_SshAuthorizedPrincipal{
											AuthorizedUser: "alice",
											Options: []*credz.Option{
												{
													Key:   &credz.Option_Name{Name: "from"},
													Value: "*.sales.example.net,!pc.sales.example.net",
												},
											},
										},
										&credz.UserPolicy_SshAuthorizedPrincipal{
											AuthorizedUser: "bob",
										},
									},
								},
							},
						},
					},
				},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			resp, err = c.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			if resp.GetResponse() == nil {
				t.Fatal("Expected a message.")
			}

			if dbus := dbusListen(ch); !reflect.DeepEqual(dbus, []string{expectedSshSetCmd, `[{ "SshAccountUsers": [ { "account": "root", "users": [ { "name" : "alice", "options" : [ { "name" : "from", "value": "*.sales.example.net,!pc.sales.example.net" } ] }, { "name" : "bob", "options" : [ ] } ] } ] }]`}) {
				t.Fatalf("DBUS Failure wanted ssh_mgmt.create_checkpoint; got: %v", dbus)
			}

			if err = c.Send(&credz.RotateAccountCredentialsRequest{
				Request: &credz.RotateAccountCredentialsRequest_Finalize{},
			}); err != nil {
				t.Fatal(err.Error())
			}
			if resp, err := c.Recv(); err != io.EOF || resp.GetResponse() != nil {
				t.Fatalf("expected EOF; err: %v; resp: %v", err, resp)
			}

			if dbus := dbusListen(ch); !reflect.DeepEqual(dbus, []string{expectedSshDeleteCmd, ""}) {
				t.Fatalf("DBUS Failure wanted %v; got: %v", expectedSshDeleteCmd, dbus)
			}
		},
	},
	/*{
		desc: "User scenario: keys, finalize",
		f: func(t *testing.T, ctx context.Context, sc credz.CredentialzClient, ch <-chan []string) {

			c, err := sc.RotateAccountCredentials(ctx)
			if err != nil {
				t.Fatal(err)
			}
			err = c.Send(&credz.RotateAccountCredentialsRequest{
				Request: &credz.RotateAccountCredentialsRequest_Credential{
					Credential: &credz.AuthorizedKeysRequest{
						Credentials: []*credz.AccountCredentials{
							{
								Account:   "root",
								Version:   "root-version-1",
								CreatedOn: 123,
								AuthorizedKeys: []*credz.AccountCredentials_AuthorizedKey{
									{
										AuthorizedKey: []byte("Authorized-key #1"),
									},
									{
										AuthorizedKey: []byte("Authorized-key #2"),
									},
								},
							},
						},
					},
				},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			resp, err := c.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			if resp.GetResponse() == nil {
				t.Fatal("Expected a message.")
			}

			if dbus := dbusListen(ch); !reflect.DeepEqual(dbus, []string{expectedSshCreateCmd, ""}) {
				t.Fatalf("DBUS Failure wanted: [%v ]; got: %+v", expectedSshCreateCmd, dbus)
			}

			if dbus := dbusListen(ch); !reflect.DeepEqual(dbus, []string{expectedSshSetCmd, `[{ "SshAccountKeys": [ { "account": "root", "keys": [ { "key" : "unspecified QXV0aG9yaXplZC1rZXkgIzE= ", "options" : [ ] }, { "key" : "unspecified QXV0aG9yaXplZC1rZXkgIzI= ", "options" : [ ] } ] } ] }]`}) {
				t.Fatalf("DBUS Failure wanted ssh_mgmt.set; got: %v", dbus)
			}
			if err = c.Send(&credz.RotateAccountCredentialsRequest{
				Request: &credz.RotateAccountCredentialsRequest_Finalize{},
			}); err != nil {
				t.Fatal(err.Error())
			}
			if resp, err := c.Recv(); err != io.EOF || resp.GetResponse() != nil {
				t.Fatalf("expected EOF; err: %v; resp: %v", err, resp)
			}

			if dbus := dbusListen(ch); !reflect.DeepEqual(dbus, []string{expectedSshDeleteCmd, ""}) {
				t.Fatalf("DBUS Failure wanted %v; got: %v", expectedSshDeleteCmd, dbus)
			}
		},
	},
	{
		desc: "User scenario: users, finalize",
		f: func(t *testing.T, ctx context.Context, sc credz.CredentialzClient, ch <-chan []string) {
			c, err := sc.RotateAccountCredentials(ctx)
			if err != nil {
				t.Fatal(err)
			}
			err = c.Send(&credz.RotateAccountCredentialsRequest{
				Request: &credz.RotateAccountCredentialsRequest_User{
					User: &credz.AuthorizedUsersRequest{
						Policies: []*credz.UserPolicy{
							{
								Account:   "root",
								Version:   "2021-09-10T18:22:46",
								CreatedOn: 1631298166,
								AuthorizedPrincipals: &credz.UserPolicy_SshAuthorizedPrincipals{
									AuthorizedPrincipals: []*credz.UserPolicy_SshAuthorizedPrincipal{
										&credz.UserPolicy_SshAuthorizedPrincipal{
											AuthorizedUser: "alice",
										},
										&credz.UserPolicy_SshAuthorizedPrincipal{
											AuthorizedUser: "bob",
										},
									},
								},
							},
						},
					},
				},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			resp, err := c.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			if resp.GetResponse() == nil {
				t.Fatal("Expected a message.")
			}

			if dbus := dbusListen(ch); !reflect.DeepEqual(dbus, []string{expectedSshCreateCmd, ""}) {
				t.Fatalf("DBUS Failure wanted: [%v ]; got: %+v", expectedSshCreateCmd, dbus)
			}

			if dbus := dbusListen(ch); !reflect.DeepEqual(dbus, []string{expectedSshSetCmd, `[{ "SshAccountUsers": [ { "account": "root", "users": [ { "name" : "alice", "options" : [ ] }, { "name" : "bob", "options" : [ ] } ] } ] }]`}) {
				t.Fatalf("DBUS Failure wanted ssh_mgmt.set; got: %v", dbus)
			}
			if err = c.Send(&credz.RotateAccountCredentialsRequest{
				Request: &credz.RotateAccountCredentialsRequest_Finalize{},
			}); err != nil {
				t.Fatal(err.Error())
			}
			if resp, err := c.Recv(); err != io.EOF || resp.GetResponse() != nil {
				t.Fatalf("expected EOF; err: %v; resp: %v", err, resp)
			}

			if dbus := dbusListen(ch); !reflect.DeepEqual(dbus, []string{expectedSshDeleteCmd, ""}) {
				t.Fatalf("DBUS Failure wanted %v; got: %v", expectedSshDeleteCmd, dbus)
			}
		},
	},
	{
		desc: "User scenario: keys, users, no finalize",
		f: func(t *testing.T, ctx context.Context, sc credz.CredentialzClient, ch <-chan []string) {
			c, err := sc.RotateAccountCredentials(ctx)
			if err != nil {
				t.Fatal(err)
			}
			err = c.Send(&credz.RotateAccountCredentialsRequest{
				Request: &credz.RotateAccountCredentialsRequest_Credential{
					Credential: &credz.AuthorizedKeysRequest{
						Credentials: []*credz.AccountCredentials{
							{
								Account:   "root",
								Version:   "root-version-1",
								CreatedOn: 123,
								AuthorizedKeys: []*credz.AccountCredentials_AuthorizedKey{
									{
										AuthorizedKey: []byte("Authorized-key #1"),
									},
									{
										AuthorizedKey: []byte("Authorized-key #2"),
									},
								},
							},
						},
					},
				},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			resp, err := c.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			if resp.GetResponse() == nil {
				t.Fatal("Expected a message.")
			}

			if dbus := dbusListen(ch); !reflect.DeepEqual(dbus, []string{expectedSshCreateCmd, ""}) {
				t.Fatalf("DBUS Failure wanted: [%v ]; got: %+v", expectedSshCreateCmd, dbus)
			}

			if dbus := dbusListen(ch); !reflect.DeepEqual(dbus, []string{expectedSshSetCmd, `[{ "SshAccountKeys": [ { "account": "root", "keys": [ { "key" : "unspecified QXV0aG9yaXplZC1rZXkgIzE= ", "options" : [ ] }, { "key" : "unspecified QXV0aG9yaXplZC1rZXkgIzI= ", "options" : [ ] } ] } ] }]`}) {
				t.Fatalf("DBUS Failure wanted ssh_mgmt.set; got: %v", dbus)
			}
			err = c.Send(&credz.RotateAccountCredentialsRequest{
				Request: &credz.RotateAccountCredentialsRequest_User{
					User: &credz.AuthorizedUsersRequest{
						Policies: []*credz.UserPolicy{
							{
								Account:   "root",
								Version:   "root-version-2",
								CreatedOn: 123,
								AuthorizedPrincipals: &credz.UserPolicy_SshAuthorizedPrincipals{
									AuthorizedPrincipals: []*credz.UserPolicy_SshAuthorizedPrincipal{
										&credz.UserPolicy_SshAuthorizedPrincipal{
											AuthorizedUser: "alice",
										},
										&credz.UserPolicy_SshAuthorizedPrincipal{
											AuthorizedUser: "bob",
										},
									},
								},
							},
						},
					},
				},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			resp, err = c.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			if resp.GetResponse() == nil {
				t.Fatal("Expected a message.")
			}

			if dbus := dbusListen(ch); !reflect.DeepEqual(dbus, []string{expectedSshSetCmd, `[{ "SshAccountUsers": [ { "account": "root", "users": [ { "name" : "alice", "options" : [ ] }, { "name" : "bob", "options" : [ ] } ] } ] }]`}) {
				t.Fatalf("DBUS Failure wanted ssh_mgmt.set; got: %v", dbus)
			}
			if err = c.CloseSend(); err != nil {
				t.Fatal(err.Error())
			}
			resp, err = c.Recv()
			if err == nil {
				t.Fatal("Expected an error reporting premature closure of the stream.")
			}
			if status.Code(err) != codes.Aborted {
				t.Fatalf("Unexpected error: %v", err)
			}
			if resp != nil {
				t.Fatal("Received unexpected message after closing connection.")
			}

			if dbus := dbusListen(ch); !reflect.DeepEqual(dbus, []string{expectedSshRestoreCmd, ""}) {
				t.Fatalf("DBUS Failure wanted %v; got: %v", expectedSshRestoreCmd, dbus)
			}
		},
	},
	{
		desc: "User scenario: keys, no finalize",
		f: func(t *testing.T, ctx context.Context, sc credz.CredentialzClient, ch <-chan []string) {
			c, err := sc.RotateAccountCredentials(ctx)
			if err != nil {
				t.Fatal(err)
			}
			err = c.Send(&credz.RotateAccountCredentialsRequest{
				Request: &credz.RotateAccountCredentialsRequest_Credential{
					Credential: &credz.AuthorizedKeysRequest{
						Credentials: []*credz.AccountCredentials{
							{
								Account:   "root",
								Version:   "root-version-1",
								CreatedOn: 123,
								AuthorizedKeys: []*credz.AccountCredentials_AuthorizedKey{
									{
										AuthorizedKey: []byte("Authorized-key #1"),
									},
									{
										AuthorizedKey: []byte("Authorized-key #2"),
									},
								},
							},
						},
					},
				},
			})

			if dbus := dbusListen(ch); !reflect.DeepEqual(dbus, []string{expectedSshCreateCmd, ""}) {
				t.Fatalf("DBUS Failure wanted: [%v ]; got: %+v", expectedSshCreateCmd, dbus)
			}
			if err != nil {
				t.Fatal(err.Error())
			}
			resp, err := c.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			if resp.GetResponse() == nil {
				t.Fatal("Expected a message.")
			}

			if dbus := dbusListen(ch); !reflect.DeepEqual(dbus, []string{expectedSshSetCmd, `[{ "SshAccountKeys": [ { "account": "root", "keys": [ { "key" : "unspecified QXV0aG9yaXplZC1rZXkgIzE= ", "options" : [ ] }, { "key" : "unspecified QXV0aG9yaXplZC1rZXkgIzI= ", "options" : [ ] } ] } ] }]`}) {
				t.Fatalf("DBUS Failure wanted ssh_mgmt.set; got: %v", dbus)
			}
			if err = c.CloseSend(); err != nil {
				t.Fatal(err.Error())
			}
			resp, err = c.Recv()
			if err == nil {
				t.Fatal("Expected an error reporting premature closure of the stream.")
			}
			if status.Code(err) != codes.Aborted {
				t.Fatalf("Unexpected error: %v", err)
			}
			if resp != nil {
				t.Fatal("Received unexpected message after closing connection.")
			}

			if dbus := dbusListen(ch); !reflect.DeepEqual(dbus, []string{expectedSshRestoreCmd, ""}) {
				t.Fatalf("DBUS Failure wanted %v; got: %v", expectedSshRestoreCmd, dbus)
			}
		},
	},
	{
		desc: "User scenario: users, no finalize",
		f: func(t *testing.T, ctx context.Context, sc credz.CredentialzClient, ch <-chan []string) {
			c, err := sc.RotateAccountCredentials(ctx)
			if err != nil {
				t.Fatal(err)
			}
			err = c.Send(&credz.RotateAccountCredentialsRequest{
				Request: &credz.RotateAccountCredentialsRequest_User{
					User: &credz.AuthorizedUsersRequest{
						Policies: []*credz.UserPolicy{
							{
								Account:   "root",
								Version:   "root-version-2",
								CreatedOn: 123,
								AuthorizedPrincipals: &credz.UserPolicy_SshAuthorizedPrincipals{
									AuthorizedPrincipals: []*credz.UserPolicy_SshAuthorizedPrincipal{
										&credz.UserPolicy_SshAuthorizedPrincipal{
											AuthorizedUser: "alice",
										},
										&credz.UserPolicy_SshAuthorizedPrincipal{
											AuthorizedUser: "bob",
										},
									},
								},
							},
						},
					},
				},
			})
			if err != nil {
				t.Fatal(err.Error())
			}
			resp, err := c.Recv()
			if err != nil {
				t.Fatal(err.Error())
			}
			if resp.GetResponse() == nil {
				t.Fatal("Expected a message.")
			}

			if dbus := dbusListen(ch); !reflect.DeepEqual(dbus, []string{expectedSshCreateCmd, ""}) {
				t.Fatalf("DBUS Failure wanted: [%v ]; got: %+v", expectedSshCreateCmd, dbus)
			}

			if dbus := dbusListen(ch); !reflect.DeepEqual(dbus, []string{expectedSshSetCmd, `[{ "SshAccountUsers": [ { "account": "root", "users": [ { "name" : "alice", "options" : [ ] }, { "name" : "bob", "options" : [ ] } ] } ] }]`}) {
				t.Fatalf("DBUS Failure wanted ssh_mgmt.set; got: %v", dbus)
			}
			if err = c.CloseSend(); err != nil {
				t.Fatal(err.Error())
			}
			resp, err = c.Recv()
			if err == nil {
				t.Fatal("Expected an error reporting premature closure of the stream.")
			}
			if status.Code(err) != codes.Aborted {
				t.Fatalf("Unexpected error: %v", err)
			}
			if resp != nil {
				t.Fatal("Received unexpected message after closing connection.")
			}
			if dbus := dbusListen(ch); !reflect.DeepEqual(dbus, []string{expectedSshRestoreCmd, ""}) {
				t.Fatalf("DBUS Failure wanted %v; got: %v", expectedSshRestoreCmd, dbus)
			}
		},
	},
	{
		desc: "User scenario: graceful close",
		f: func(t *testing.T, ctx context.Context, sc credz.CredentialzClient, ch <-chan []string) {
			c, err := sc.RotateAccountCredentials(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err = c.CloseSend(); err != nil {
				t.Fatal(err.Error())
			}
			resp, err := c.Recv()
			if err == nil {
				t.Fatal("Expected an error reporting premature closure of the stream.")
			}
			if status.Code(err) != codes.Aborted {
				t.Fatalf("Unexpected error: %v", err)
			}
			if resp != nil {
				t.Fatal("Received unexpected message after closing connection.")
			}

			select {
			case msg := <-ch:
				t.Fatalf("Unexpected DBUS msg %v", msg)
			default:
			}
		},
	},
	{
		desc: "Host scenario: ca_public_key, finalize",
		f: func(t *testing.T, ctx context.Context, sc credz.CredentialzClient, ch <-chan []string) {
			var err error
			var h credz.Credentialz_RotateHostParametersClient
			runRotateHostParameters("OpenStream", ch, t, []mockDBusMessage{}, func() error {
				h, err = sc.RotateHostParameters(ctx)
				return err
			})
			runRotateHostParameters("ModifySshCaPublicKey", ch, t,
				[]mockDBusMessage{
					{
						methodName: expectedSshCreateCmd,
						cmd:        "",
					},
					{
						methodName: expectedSshSetCmd,
						cmd:        `[{ "SshCaPublicKey": [ "ssh-ed25519 VEVTVC1DRVJUICMx test#1", "ssh-rsa VEVTVC1DRVJUICMy test#2" ] }]`,
					},
				},
				func() error {
					err = h.Send(&credz.RotateHostParametersRequest{
						Request: &credz.RotateHostParametersRequest_SshCaPublicKey{
							SshCaPublicKey: &credz.CaPublicKeyRequest{
								Version:   "CA-trust-bundle-1",
								CreatedOn: 123,
								SshCaPublicKeys: []*credz.PublicKey{
									&credz.PublicKey{
										PublicKey:   []byte("TEST-CERT #1"),
										KeyType:     credz.KeyType_KEY_TYPE_ED25519,
										Description: "test#1",
									},
									&credz.PublicKey{
										PublicKey:   []byte("TEST-CERT #2"),
										KeyType:     credz.KeyType_KEY_TYPE_RSA_2048,
										Description: "test#2",
									},
								},
							},
						},
					})
					if err != nil {
						t.Fatal(err.Error())
					}
					resp, err := h.Recv()
					if err != nil {
						t.Fatal(err.Error())
					}
					if resp.GetResponse() == nil {
						t.Fatal("Expected a message.")
					}
					return nil
				})
			runRotateHostParameters("ModifyFinalize", ch, t, []mockDBusMessage{{methodName: expectedSshDeleteCmd, cmd: ""}}, func() error {
				err = h.Send(&credz.RotateHostParametersRequest{
					Request: &credz.RotateHostParametersRequest_Finalize{},
				})
				if err != nil {
					t.Fatal(err.Error())
				}
				if resp, err := h.Recv(); err != io.EOF || resp.GetResponse() != nil {
					t.Fatalf("expected EOF; err: %v; resp: %v", err, resp)
				}
				return nil
			})
		},
	},
	{
		desc: "Host scenario: ca_public_key, no finalize",
		f: func(t *testing.T, ctx context.Context, sc credz.CredentialzClient, ch <-chan []string) {
			var err error
			var h credz.Credentialz_RotateHostParametersClient
			runRotateHostParameters("OpenStream", ch, t, []mockDBusMessage{}, func() error {
				h, err = sc.RotateHostParameters(ctx)
				return err
			})
			runRotateHostParameters("ModifySshCaPublicKey", ch, t,
				[]mockDBusMessage{
					{
						methodName: expectedSshCreateCmd,
						cmd:        "",
					},
					{
						methodName: expectedSshSetCmd,
						cmd:        `[{ "SshCaPublicKey": [ "ssh-ed25519 VEVTVC1DRVJUICMx test#1", "ssh-rsa VEVTVC1DRVJUICMy test#2" ] }]`,
					},
				},
				func() error {
					err = h.Send(&credz.RotateHostParametersRequest{
						Request: &credz.RotateHostParametersRequest_SshCaPublicKey{
							SshCaPublicKey: &credz.CaPublicKeyRequest{
								Version:   "CA-trust-bundle-1",
								CreatedOn: 123,
								SshCaPublicKeys: []*credz.PublicKey{
									&credz.PublicKey{
										PublicKey:   []byte("TEST-CERT #1"),
										KeyType:     credz.KeyType_KEY_TYPE_ED25519,
										Description: "test#1",
									},
									&credz.PublicKey{
										PublicKey:   []byte("TEST-CERT #2"),
										KeyType:     credz.KeyType_KEY_TYPE_RSA_2048,
										Description: "test#2",
									},
								},
							},
						},
					})
					if err != nil {
						t.Fatal(err.Error())
					}
					resp, err := h.Recv()
					if err != nil {
						t.Fatal(err.Error())
					}
					if resp.GetResponse() == nil {
						t.Fatal("Expected a message.")
					}
					return nil
				})
			runRotateHostParameters("CloseStream", ch, t, []mockDBusMessage{{methodName: expectedSshRestoreCmd, cmd: ""}}, func() error {
				if err = h.CloseSend(); err != nil {
					t.Fatal(err.Error())
				}
				resp, err := h.Recv()
				if err == nil {
					t.Fatal("Expected an error reporting premature closure of the stream.")
				}
				if status.Code(err) != codes.Aborted {
					t.Fatalf("Unexpected error: %v", err)
				}
				if resp != nil {
					t.Fatal("Received unexpected message after closing connection.")
				}
				return nil
			})
		},
	},
	{
		desc: "Host scenario: graceful close",
		f: func(t *testing.T, ctx context.Context, sc credz.CredentialzClient, ch <-chan []string) {
			var err error
			var h credz.Credentialz_RotateHostParametersClient
			runRotateHostParameters("OpenStream", ch, t, []mockDBusMessage{}, func() error {
				h, err = sc.RotateHostParameters(ctx)
				return err
			})
			runRotateHostParameters("CloseStream", ch, t, []mockDBusMessage{}, func() error {
				if err = h.CloseSend(); err != nil {
					t.Fatal(err.Error())
				}
				resp, err := h.Recv()
				if status.Code(err) != codes.Aborted {
					t.Fatalf("Unexpected error: %v", err)
				}
				if resp != nil {
					t.Fatal("Received unexpected message after closing connection.")
				}
				return nil
			})
		},
	},
	{
		desc: "read JSON with version info, fail",
		f: func(t *testing.T, ctx context.Context, sc credz.CredentialzClient, ch <-chan []string) {
			s := &GNSICredentialzServer{
				sshCredMetadata: NewSshCredMetadata(),
			}
			if err := s.loadCredentialFreshness(""); err == nil {
				t.Fatal("Expected file read error")
			}
		},
	},
	{
		desc: "read JSON with version info, success",
		f: func(t *testing.T, ctx context.Context, sc credz.CredentialzClient, ch <-chan []string) {
			s := &GNSICredentialzServer{
				sshCredMetadata: NewSshCredMetadata(),
			}
			if err := s.loadCredentialFreshness(sshMetaPathTest); err != nil {
				t.Fatal(err)
			}
		},
	},
	{
		desc: "write JSON with version info, fail",
		f: func(t *testing.T, ctx context.Context, sc credz.CredentialzClient, ch <-chan []string) {
			s := &GNSICredentialzServer{
				sshCredMetadata: NewSshCredMetadata(),
			}
			if err := s.saveCredentialsFreshness(""); err == nil {
				t.Fatal("Expected write file error")
			}
		},
	},
	{
		desc: "write JSON with version info, success",
		f: func(t *testing.T, ctx context.Context, sc credz.CredentialzClient, ch <-chan []string) {
			s := &GNSICredentialzServer{
				sshCredMetadata: NewSshCredMetadata(),
			}
			if err := s.saveCredentialsFreshness(sshMetaPathTest); err != nil {
				t.Fatal(err)
			}
		},
	},
	{
		desc: "Reject concurrent account",
		f: func(t *testing.T, ctx context.Context, sc credz.CredentialzClient, ch <-chan []string) {
			c, err := sc.RotateAccountCredentials(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err = c.Send(&credz.RotateAccountCredentialsRequest{}); err != nil {
				t.Fatal(err)
			}
			c2, err := sc.RotateAccountCredentials(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err = c2.Send(&credz.RotateAccountCredentialsRequest{}); err != nil {
				t.Fatal(err)
			}
			if _, err := c2.Recv(); status.Code(err) != codes.Aborted {
				t.Errorf("expected: Aborted, got: %+v", err)
			}
		},
	},
	{
		desc: "Reject concurrent host",
		f: func(t *testing.T, ctx context.Context, sc credz.CredentialzClient, ch <-chan []string) {
			c, err := sc.RotateHostParameters(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err = c.Send(&credz.RotateHostParametersRequest{}); err != nil {
				t.Fatal(err)
			}
			c2, err := sc.RotateHostParameters(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err = c2.Send(&credz.RotateHostParametersRequest{}); err != nil {
				t.Fatal(err)
			}
			if _, err := c2.Recv(); status.Code(err) != codes.Aborted {
				t.Errorf("expected: Aborted, got: %+v", err)
			}
		},
	},
	{
		desc: "Host scenario: ca_public_key, ca_public_key",
		f: func(t *testing.T, ctx context.Context, sc credz.CredentialzClient, ch <-chan []string) {
			var err error
			var h credz.Credentialz_RotateHostParametersClient
			runRotateHostParameters("OpenStream", ch, t, []mockDBusMessage{}, func() error {
				h, err = sc.RotateHostParameters(ctx)
				return err
			})
			runRotateHostParameters("ModifySshCaPublicKey", ch, t,
				[]mockDBusMessage{
					{
						methodName: expectedSshCreateCmd,
						cmd:        "",
					},
					{
						methodName: expectedSshSetCmd,
						cmd:        `[{ "SshCaPublicKey": [ "ssh-ed25519 VEVTVC1DRVJUICMx test#1", "ssh-rsa VEVTVC1DRVJUICMy test#2" ] }]`,
					},
				},
				func() error {
					if err = h.Send(&credz.RotateHostParametersRequest{
						Request: &credz.RotateHostParametersRequest_SshCaPublicKey{
							SshCaPublicKey: &credz.CaPublicKeyRequest{
								Version:   "CA-trust-bundle-1",
								CreatedOn: 123,
								SshCaPublicKeys: []*credz.PublicKey{
									&credz.PublicKey{
										PublicKey:   []byte("TEST-CERT #1"),
										KeyType:     credz.KeyType_KEY_TYPE_ED25519,
										Description: "test#1",
									},
									&credz.PublicKey{
										PublicKey:   []byte("TEST-CERT #2"),
										KeyType:     credz.KeyType_KEY_TYPE_RSA_2048,
										Description: "test#2",
									},
								},
							},
						},
					}); err != nil {
						t.Fatalf("h.Send() failed: %v", err)
					}
					resp, err := h.Recv()
					if err != nil {
						t.Fatalf("h.Recv() failed: %v", err)
					}
					if resp.GetResponse() == nil {
						t.Fatal("resp.GetResponse() returned nil; expected a message")
					}
					return nil
				})
			runRotateHostParameters("SecondModifySshCaPublicKey", ch, t, []mockDBusMessage{{methodName: expectedSshRestoreCmd, cmd: ""}},
				func() error {
					if err = h.Send(&credz.RotateHostParametersRequest{
						Request: &credz.RotateHostParametersRequest_SshCaPublicKey{
							SshCaPublicKey: &credz.CaPublicKeyRequest{
								Version:   "CA-trust-bundle-2",
								CreatedOn: 123,
								SshCaPublicKeys: []*credz.PublicKey{
									&credz.PublicKey{
										PublicKey:   []byte("TEST-CERT #3"),
										KeyType:     credz.KeyType_KEY_TYPE_ED25519,
										Description: "test#3",
									},
									&credz.PublicKey{
										PublicKey:   []byte("TEST-CERT #4"),
										KeyType:     credz.KeyType_KEY_TYPE_RSA_2048,
										Description: "test#4",
									},
								},
							},
						},
					}); err != nil {
						t.Fatalf("h.Send() failed: %v", err)
					}
					// Check that the client receives an Aborted error due to multiple SSH CA public key requests.
					if resp, err := h.Recv(); status.Code(err) != codes.Aborted || resp != nil {
						t.Errorf("h.Recv() returned resp: %v, err: %v; expected error status code: %v", resp, err, codes.Aborted)
					}
					return nil
				})
		},
	},
	{
		desc: "Host scenario: ca_public_key, glome",
		f: func(t *testing.T, ctx context.Context, sc credz.CredentialzClient, ch <-chan []string) {
			var err error
			var h credz.Credentialz_RotateHostParametersClient
			runRotateHostParameters("OpenStream", ch, t, []mockDBusMessage{}, func() error {
				h, err = sc.RotateHostParameters(ctx)
				return err
			})
			runRotateHostParameters("ModifySshCaPublicKey", ch, t,
				[]mockDBusMessage{
					{
						methodName: expectedSshCreateCmd,
						cmd:        "",
					},
					{
						methodName: expectedSshSetCmd,
						cmd:        `[{ "SshCaPublicKey": [ "ssh-ed25519 VEVTVC1DRVJUICMx test#1", "ssh-rsa VEVTVC1DRVJUICMy test#2" ] }]`,
					},
				},
				func() error {
					if err = h.Send(&credz.RotateHostParametersRequest{
						Request: &credz.RotateHostParametersRequest_SshCaPublicKey{
							SshCaPublicKey: &credz.CaPublicKeyRequest{
								Version:   "CA-trust-bundle-1",
								CreatedOn: 123,
								SshCaPublicKeys: []*credz.PublicKey{
									&credz.PublicKey{
										PublicKey:   []byte("TEST-CERT #1"),
										KeyType:     credz.KeyType_KEY_TYPE_ED25519,
										Description: "test#1",
									},
									&credz.PublicKey{
										PublicKey:   []byte("TEST-CERT #2"),
										KeyType:     credz.KeyType_KEY_TYPE_RSA_2048,
										Description: "test#2",
									},
								},
							},
						},
					}); err != nil {
						t.Fatalf("h.Send() failed: %v", err)
					}
					resp, err := h.Recv()
					if err != nil {
						t.Fatalf("h.Recv() failed: %v", err)
					}
					if resp.GetResponse() == nil {
						t.Fatal("resp.GetResponse() returned nil; expected a message")
					}
					return nil
				})
			runRotateHostParameters("GlomeRequest", ch, t, []mockDBusMessage{{methodName: expectedSshRestoreCmd, cmd: ""}},
				func() error {
					if err = h.Send(validRotateHostParametersGlomeRequest); err != nil {
						t.Fatalf("h.Send() failed: %v", err)
					}
					// Check that the client receives an Aborted error due to Glome request after SSH CA public key request.
					if resp, err := h.Recv(); status.Code(err) != codes.Aborted || resp != nil {
						t.Errorf("h.Recv() returned resp: %v, err: %v; expected error status code: %v", resp, err, codes.Aborted)
					}
					return nil
				})
		},
	},*/
}

// TestSSHServer tests implementation of gnsi.Ssh server.
func TestGnsiCredzSSHServer(t *testing.T) {
	t.Helper()
	cfg := &Config{SshCredMetaFile: sshMetaPathTest, ConsoleCredMetaFile: consoleMetaPathTest, Port: 8081}
	s := createCredzServer(t, cfg)
	go runServer(t, s)
	defer s.Stop()

	metaBackup, err := os.ReadFile(sshMetaPathTest)
	if err != nil {
		t.Fatal(err)
	}
	defer os.WriteFile(sshMetaPathTest, metaBackup, 0600)

	var dbusListener chan []string
	var done chan bool
	// dbusCaller is a package variable
	dbusListener, done, dbusCaller = newMockSshDbusServer()
	defer func() { close(dbusListener); close(done) }()

	// Create a gNSI.ssh client and connect it to the gNSI.ssh server.
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}
	targetAddr := fmt.Sprintf("127.0.0.1:%d", s.config.Port)
	var credzMu sync.Mutex
	for _, tc := range sshTests {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		t.Run(tc.desc, func(t *testing.T) {
			credzMu.Lock()
			defer credzMu.Unlock()
			conn, err := grpc.Dial(targetAddr, opts...)
			if err != nil {
				t.Fatalf("Dialing to %s failed: %v", targetAddr, err)
			}
			defer conn.Close()
			credzClient := credz.NewCredentialzClient(conn)
			tc.f(t, ctx, credzClient, dbusListener)
		})
		cancel()
	}
	done <- true
	// Save the SSH Credentials metadata to a file.
	if err := s.gnsiCredz.saveCredentialsFreshness(s.config.SshCredMetaFile); err != nil {
		t.Fatal(err)
	}
}

func newMockSshDbusServer() (chan []string, chan bool, ssc.Caller) {
	ch := make(chan []string, 10)
	pass := make(chan []string, 10)
	done := make(chan bool, 1)
	go func() {
		checkpoint := false
		var mu sync.Mutex
		for {
			select {
			case <-done: // if cancel() execute
				//log.V(lvl.INFO).Infoln("Shutting down the DBUS server.")
				return
			case msg := <-ch:
				mu.Lock()
				//log.V(lvl.INFO).Infof("DBUS service received %+v", msg)
				switch msg[0] {
				case expectedSshCreateCmd:
					if checkpoint {
						log.Fatal("mock ssh_mgmt service: checkpoint already exists.")
					}
					checkpoint = true
				case expectedSshDeleteCmd:
					if !checkpoint {
						log.Fatal("mock ssh_mgmt service: checkpoint does not exists.")
					}
					checkpoint = false
				case expectedSshRestoreCmd:
					if !checkpoint {
						log.Fatal("mock ssh_mgmt service: checkpoint does not exists.")
					}
					checkpoint = false
				case expectedSshSetCmd:
					if !checkpoint {
						log.Fatal("mock ssh_mgmt service: set without checkpoint.")
					}
				default:
					//log.V(lvl.INFO).Infof("mock ssh_mgmt service: unknown service: %+v", msg)
				}
				mu.Unlock()
				pass <- msg
			}
		}
	}()

	return pass, done, &ssc.SpyDbusCaller{Command: ch}
}

func dbusListen(dbus <-chan []string) []string {
	select {
	case resp := <-dbus:
		return resp
	case <-time.After(time.Second * 3):
		return []string{"dbus listener timed out"}
	}
}

func run(name string, dbus <-chan []string, t *testing.T, cmd []string, f func() error) {
	t.Run(name, func(t *testing.T) {
		var wg sync.WaitGroup
		wg.Add(1)
		finished := make(chan error, 1)
		go func() {
			finished <- f()
			wg.Done()
		}()
		count := 2
		if len(cmd) == 0 {
			count = 1
		}
		for i := 0; i < count; i++ {
			select {
			case resp := <-dbus:
				//log.V(lvl.INFO).Infof("received on DBUS: %v\n", resp)
				for i := range cmd {
					if cmd[i] != resp[i] {
						t.Errorf("expected: '%v' but got '%v'", cmd, resp)
					}
				}
				if len(cmd) == 2 && !json.Valid([]byte(cmd[1])) {
					t.Errorf("malformed JSON string: '%v'", cmd[1])
				}
			case err := <-finished:
				//log.V(lvl.INFO).Infof("f() is done with err=%v\n", err)
				if err != nil {
					t.Error(err.Error())
				}
			case <-time.After(time.Second * 3):
				t.Errorf("did not get expected DBUS message and/or %v() did not finish within 5s", name)
			}
		}
		wg.Wait()
		//log.V(lvl.INFO).Infoln("Finished:", name)
	})
}

// runRotateHostParameters runs the clientAction() and waits for the DBUS messages.
// The clientAction() is expected to send a request to the server and wait for the response.
// The DBUS messages are expected to be sent by the server to the host service.
func runRotateHostParameters(name string, dbusListener <-chan []string, t *testing.T, expectedDbusMsgs []mockDBusMessage, clientAction func() error) {
	testName := "RotateHostParameters_" + name
	t.Run(testName, func(t *testing.T) {
		// finished is used to wait for the clientAction() to finish.
		finished := make(chan error, 1)
		go func() {
			finished <- clientAction()
		}()

		// Wait for the DBus messages and check if they are the same as expected.
		for _, expectedDbusMsg := range expectedDbusMsgs {
			select {
			case resp := <-dbusListener:
				//log.V(lvl.INFO).Infof("received on DBUS: %v\n", resp)
				if expectedDbusMsg.methodName != resp[0] {
					t.Errorf("expected DBUS message's method name: '%v' but got '%v'", expectedDbusMsg.methodName, resp[0])
					continue
				}
				// If restore checkpoint methodName, nothing should be sent for the command.
				if expectedDbusMsg.methodName == expectedGlomeConfigRestoreCmd {
					if len(resp) != 1 {
						t.Errorf("for DBUS methodName %v, no message string (cmd) expected to be set", expectedGlomeConfigRestoreCmd)
					}
					continue
				}
				// Check if the received DBUS message's cmd is the same as expected.
				// A direct string comparison is a fast path. If they differ, and a non-empty
				// cmd is expected, fall back to a more lenient JSON comparison.
				if expectedDbusMsg.cmd != resp[1] {
					if expectedDbusMsg.cmd == "" {
						t.Errorf("expected empty DBUS message cmd, but got: '%v'", resp[1])
					} else {
						// Use json.Unmarshal to compare the JSON strings regardless of field order.
						var expected, received interface{}
						if err := json.Unmarshal([]byte(expectedDbusMsg.cmd), &expected); err != nil {
							t.Errorf("failed to unmarshal expected DBUS message's cmd: %v", err)
							continue
						}
						if err := json.Unmarshal([]byte(resp[1]), &received); err != nil {
							t.Errorf("failed to unmarshal received DBUS message's cmd: %v", err)
							continue
						}
						if !reflect.DeepEqual(expected, received) {
							t.Errorf("expected DBUS message's cmd: '%v' but got '%v'", expectedDbusMsg.cmd, resp[1])
							continue
						}
					}
				}
			case <-time.After(time.Second * 5):
				t.Errorf("did not get expected DBUS message and/or clientAction() did not finish within 5s")
			}
		}

		// Wait for the clientAction() to finish.
		select {
		case err := <-finished:
			//log.V(lvl.INFO).Infof("clientAction() is done with err=%v\n", err)
			if err != nil {
				t.Error(err.Error())
			}
		case <-time.After(time.Second * 5):
			t.Errorf("clientAction() did not finish within 5s")
		}

		// Check for any unexpected messages.
		select {
		case resp := <-dbusListener:
			t.Errorf("received unexpected DBUS message: %v", resp)
		default:
		}
		//log.V(lvl.INFO).Infoln("Finished:", testName)
	})
}

// mockDBusMessage is a struct that represents a DBUS message.
type mockDBusMessage struct {
	methodName string // endpoint of the host service (intName)
	cmd        string // content of the DBUS message in JSON-formatted string
}

// CONSOLE

var consoleTests = []struct {
	desc string
	f    func(t *testing.T, ctx context.Context, sc credz.CredentialzClient, ch <-chan []string, targetAddr string)
}{
	{
		desc: "two accounts, finalize",
		f: func(t *testing.T, ctx context.Context, sc credz.CredentialzClient, ch <-chan []string, targetAddr string) {
			c, err := sc.RotateAccountCredentials(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if c.Send(&credz.RotateAccountCredentialsRequest{
				Request: &credz.RotateAccountCredentialsRequest_Password{
					Password: &credz.PasswordRequest{
						Accounts: []*credz.PasswordRequest_Account{
							{
								Account: "alice",
								Password: &credz.PasswordRequest_Password{
									Value: &credz.PasswordRequest_Password_Plaintext{
										Plaintext: "password-alice"}},
								Version:   "version-1",
								CreatedOn: 123,
							},
							{
								Account: "bob",
								Password: &credz.PasswordRequest_Password{
									Value: &credz.PasswordRequest_Password_Plaintext{
										Plaintext: "password-bob"}},
								Version:   "version-2",
								CreatedOn: 321,
							},
						},
					},
				},
			}); err != nil {
				t.Fatal(err)
			}
			if resp, err := c.Recv(); err != nil || resp.GetResponse() == nil {
				t.Fatalf("expected Response; err: %v; resp: %v", err, resp)
			}

			if dbus := dbusListen(ch); !reflect.DeepEqual(dbus, []string{expectedConsoleCreateCmd, ""}) {
				t.Fatalf("DBUS Failure wanted: [%v ]; got: %+v", expectedConsoleCreateCmd, dbus)
			}

			if dbus := dbusListen(ch); !reflect.DeepEqual(dbus, []string{expectedConsoleSetCmd, `[{ "ConsolePasswords": [ { "name": "alice", "password" : "password-alice" },{ "name": "bob", "password" : "password-bob" } ] }]`}) {
				t.Fatalf("DBUS Failure wanted gnsi_console.set; got: %v", dbus)
			}
			if err = c.Send(&credz.RotateAccountCredentialsRequest{
				Request: &credz.RotateAccountCredentialsRequest_Finalize{},
			}); err != nil {
				t.Fatal(err)
			}
			if resp, err := c.Recv(); err != io.EOF || resp.GetResponse() != nil {
				t.Fatalf("expected EOF; err: %v; resp: %v", err, resp)
			}
			if dbus := dbusListen(ch); !reflect.DeepEqual(dbus, []string{expectedConsoleDeleteCmd, ""}) {
				t.Fatalf("DBUS Failure wanted gnsi_console.delete_checkpoint; got: %v", dbus)
			}
		},
	},
	{
		desc: "two accounts, no finalize",
		f: func(t *testing.T, ctx context.Context, sc credz.CredentialzClient, ch <-chan []string, targetAddr string) {
			c, err := sc.RotateAccountCredentials(ctx)
			if err != nil {
				t.Fatal(err)
			}
			err = c.Send(&credz.RotateAccountCredentialsRequest{
				Request: &credz.RotateAccountCredentialsRequest_Password{
					Password: &credz.PasswordRequest{
						Accounts: []*credz.PasswordRequest_Account{
							{
								Account: "alice",
								Password: &credz.PasswordRequest_Password{
									Value: &credz.PasswordRequest_Password_Plaintext{
										Plaintext: "password-alice"}},
								Version:   "version-1",
								CreatedOn: 123,
							},
							{
								Account: "bob",
								Password: &credz.PasswordRequest_Password{
									Value: &credz.PasswordRequest_Password_Plaintext{
										Plaintext: "password-bob"}},
								Version:   "version-2",
								CreatedOn: 321,
							},
						},
					},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			resp, err := c.Recv()
			if err != nil {
				t.Fatal(err)
			}
			if resp.GetResponse() == nil {
				t.Fatal("Expected a message.")
			}
			if dbus := dbusListen(ch); !reflect.DeepEqual(dbus, []string{expectedConsoleCreateCmd, ""}) {
				t.Fatalf("DBUS Failure wanted: [%v ]; got: %+v", expectedConsoleCreateCmd, dbus)
			}
			if dbus := dbusListen(ch); !reflect.DeepEqual(dbus, []string{expectedConsoleSetCmd, `[{ "ConsolePasswords": [ { "name": "alice", "password" : "password-alice" },{ "name": "bob", "password" : "password-bob" } ] }]`}) {
				t.Fatalf("DBUS Failure wanted gnsi_console.set; got: %v", dbus)
			}
			if err = c.CloseSend(); err != nil {
				t.Fatal(err)
			}
			resp, err = c.Recv()
			if err == nil {
				t.Fatal("Expected an error but did not get it")
			}
			if status.Code(err) != codes.Aborted {
				t.Fatal(err)
			}
			if resp != nil {
				t.Fatal("Received unexpected message after closing connection.")
			}
			if dbus := dbusListen(ch); !reflect.DeepEqual(dbus, []string{expectedConsoleRestoreCmd, ""}) {
				t.Fatalf("DBUS Failure wanted: [%v ]; got: %+v", expectedConsoleRestoreCmd, dbus)
			}
		},
	},
	{
		desc: "incomplete set request (password), connection closed",
		f: func(t *testing.T, ctx context.Context, sc credz.CredentialzClient, ch <-chan []string, targetAddr string) {
			c, err := sc.RotateAccountCredentials(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err = c.Send(&credz.RotateAccountCredentialsRequest{
				Request: &credz.RotateAccountCredentialsRequest_Password{
					Password: &credz.PasswordRequest{
						Accounts: []*credz.PasswordRequest_Account{
							{
								Account:   "alice",
								Version:   "version-1",
								CreatedOn: 123,
							},
						},
					},
				},
			}); err != nil {
				t.Fatal(err)
			}
			if resp, err := c.Recv(); status.Code(err) != codes.Aborted {
				t.Fatalf("expected Aborted; err: %v; resp: %v", err, resp)
			}
			if dbus := dbusListen(ch); !reflect.DeepEqual(dbus, []string{expectedConsoleCreateCmd, ""}) {
				t.Fatalf("DBUS Failure wanted: [%v ]; got: %+v", expectedConsoleCreateCmd, dbus)
			}
			if dbus := dbusListen(ch); !reflect.DeepEqual(dbus, []string{expectedConsoleRestoreCmd, ""}) {
				t.Fatalf("DBUS Failure wanted: [%v ]; got: %+v", expectedConsoleRestoreCmd, dbus)
			}
		},
	},
	{
		desc: "incomplete set request (password blank), connection closed",
		f: func(t *testing.T, ctx context.Context, sc credz.CredentialzClient, ch <-chan []string, targetAddr string) {
			c, err := sc.RotateAccountCredentials(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err = c.Send(&credz.RotateAccountCredentialsRequest{
				Request: &credz.RotateAccountCredentialsRequest_Password{
					Password: &credz.PasswordRequest{
						Accounts: []*credz.PasswordRequest_Account{
							{
								Account:   "alice",
								Version:   "version-1",
								CreatedOn: 123,
								Password: &credz.PasswordRequest_Password{
									Value: &credz.PasswordRequest_Password_Plaintext{
										Plaintext: ""}},
							},
						},
					},
				},
			}); err != nil {
				t.Fatal(err)
			}
			if resp, err := c.Recv(); status.Code(err) != codes.Aborted {
				t.Fatalf("expected Aborted; err: %v; resp: %v", err, resp)
			}
			if dbus := dbusListen(ch); !reflect.DeepEqual(dbus, []string{expectedConsoleCreateCmd, ""}) {
				t.Fatalf("DBUS Failure wanted: [%v ]; got: %+v", expectedConsoleCreateCmd, dbus)
			}
			if dbus := dbusListen(ch); !reflect.DeepEqual(dbus, []string{expectedConsoleRestoreCmd, ""}) {
				t.Fatalf("DBUS Failure wanted: [%v ]; got: %+v", expectedConsoleRestoreCmd, dbus)
			}
		},
	},
	{
		desc: "incomplete set request (username), connection closed",
		f: func(t *testing.T, ctx context.Context, sc credz.CredentialzClient, ch <-chan []string, targetAddr string) {
			c, err := sc.RotateAccountCredentials(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err = c.Send(&credz.RotateAccountCredentialsRequest{
				Request: &credz.RotateAccountCredentialsRequest_Password{
					Password: &credz.PasswordRequest{
						Accounts: []*credz.PasswordRequest_Account{
							{
								Password: &credz.PasswordRequest_Password{
									Value: &credz.PasswordRequest_Password_Plaintext{
										Plaintext: "alice-password"}},
								Version:   "version-1",
								CreatedOn: 123,
							},
						},
					},
				},
			}); err != nil {
				t.Fatal(err)
			}
			_, err = c.Recv()
			if err == nil {
				t.Fatal("Expected an error but did not get it")
			}
			if status.Code(err) != codes.Aborted {
				t.Fatal(err)
			}
			if dbus := dbusListen(ch); !reflect.DeepEqual(dbus, []string{expectedConsoleCreateCmd, ""}) {
				t.Fatalf("DBUS Failure wanted: [%v ]; got: %+v", expectedConsoleCreateCmd, dbus)
			}
			if dbus := dbusListen(ch); !reflect.DeepEqual(dbus, []string{expectedConsoleRestoreCmd, ""}) {
				t.Fatalf("DBUS Failure wanted: [%v ]; got: %+v", expectedConsoleRestoreCmd, dbus)
			}
		},
	},
	{
		desc: "incomplete set request (version), connection closed",
		f: func(t *testing.T, ctx context.Context, sc credz.CredentialzClient, ch <-chan []string, targetAddr string) {
			c, err := sc.RotateAccountCredentials(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err = c.Send(&credz.RotateAccountCredentialsRequest{
				Request: &credz.RotateAccountCredentialsRequest_Password{
					Password: &credz.PasswordRequest{
						Accounts: []*credz.PasswordRequest_Account{
							{
								Account: "alice",
								Password: &credz.PasswordRequest_Password{
									Value: &credz.PasswordRequest_Password_Plaintext{
										Plaintext: "alice-password"}},
								CreatedOn: 123,
							},
						},
					},
				},
			}); err != nil {
				t.Fatal(err)
			}
			_, err = c.Recv()
			if err == nil {
				t.Fatal("Expected an error but did not get it")
			}
			if status.Code(err) != codes.Aborted {
				t.Fatal(err)
			}
			if dbus := dbusListen(ch); !reflect.DeepEqual(dbus, []string{expectedConsoleCreateCmd, ""}) {
				t.Fatalf("DBUS Failure wanted: [%v ]; got: %+v", expectedConsoleCreateCmd, dbus)
			}
			if dbus := dbusListen(ch); !reflect.DeepEqual(dbus, []string{expectedConsoleRestoreCmd, ""}) {
				t.Fatalf("DBUS Failure wanted: [%v ]; got: %+v", expectedConsoleRestoreCmd, dbus)
			}
		},
	},
	{
		desc: "incomplete set request (created_on), connection closed",
		f: func(t *testing.T, ctx context.Context, sc credz.CredentialzClient, ch <-chan []string, targetAddr string) {
			c, err := sc.RotateAccountCredentials(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err = c.Send(&credz.RotateAccountCredentialsRequest{
				Request: &credz.RotateAccountCredentialsRequest_Password{
					Password: &credz.PasswordRequest{
						Accounts: []*credz.PasswordRequest_Account{
							{
								Account: "alice",
								Password: &credz.PasswordRequest_Password{
									Value: &credz.PasswordRequest_Password_Plaintext{
										Plaintext: "alice-password"}},
								Version: "version-1",
							},
						},
					},
				},
			}); err != nil {
				t.Fatal(err)
			}
			_, err = c.Recv()
			if err == nil {
				t.Fatal("Expected an error but did not get it")
			}
			if status.Code(err) != codes.Aborted {
				t.Fatal(err)
			}
			if dbus := dbusListen(ch); !reflect.DeepEqual(dbus, []string{expectedConsoleCreateCmd, ""}) {
				t.Fatalf("DBUS Failure wanted: [%v ]; got: %+v", expectedConsoleCreateCmd, dbus)
			}
			if dbus := dbusListen(ch); !reflect.DeepEqual(dbus, []string{expectedConsoleRestoreCmd, ""}) {
				t.Fatalf("DBUS Failure wanted: [%v ]; got: %+v", expectedConsoleRestoreCmd, dbus)
			}
		},
	},
	{
		desc: "no accounts, no finalize, abrupt close",
		f: func(t *testing.T, _ context.Context, _ credz.CredentialzClient, ch <-chan []string, targetAddr string) {
			// Create a gNSI.console client and connect it to the gNSI.console server.
			tlsConfig := &tls.Config{InsecureSkipVerify: true}
			opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}
			// targetAddr := "127.0.0.1:8081"
			conn, err := grpc.Dial(targetAddr, opts...)
			if err != nil {
				t.Fatalf("Dialing to %s failed: %v", targetAddr, err)
			}
			defer conn.Close()
			sc := credz.NewCredentialzClient(conn)
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)

			c, err := sc.RotateAccountCredentials(ctx)
			if err != nil {
				t.Fatal(err)
			}
			cancel()
			resp, err := c.Recv()
			if err == nil {
				t.Fatal("Expected an error but did not get it")
			}
			if status.Code(err) != codes.Canceled {
				t.Fatal(err)
			}
			if resp != nil {
				t.Fatal("Received unexpected message after closing connection.")
			}
			select {
			case msg := <-ch:
				t.Fatalf("Unexpected DBUS msg %v", msg)
			default:
			}
		},
	},
	{
		desc: "read JSON with version info, fail",
		f: func(t *testing.T, ctx context.Context, sc credz.CredentialzClient, ch <-chan []string, targetAddr string) {
			s := &GNSICredentialzServer{
				consoleCredMetadata: NewConsoleCredMetadata(),
			}
			if err := s.loadConsoleCredentialFreshness(""); err == nil {
				t.Fatal("Expected file read error")
			}
		},
	},
	{
		desc: "read JSON with version info, success",
		f: func(t *testing.T, ctx context.Context, sc credz.CredentialzClient, ch <-chan []string, targetAddr string) {
			s := &GNSICredentialzServer{
				consoleCredMetadata: NewConsoleCredMetadata(),
			}
			if err := s.loadConsoleCredentialFreshness("../testdata/gnsi/console-version.json"); err != nil {
				t.Fatal(err)
			}
		},
	},
	{
		desc: "write JSON with version info, fail",
		f: func(t *testing.T, ctx context.Context, sc credz.CredentialzClient, ch <-chan []string, targetAddr string) {
			s := &GNSICredentialzServer{
				consoleCredMetadata: NewConsoleCredMetadata(),
			}
			if err := s.saveConsoleCredentialsFreshness(""); err == nil {
				t.Fatal("Expected file write error")
			}
		},
	},
	{
		desc: "write JSON with version info, success",
		f: func(t *testing.T, ctx context.Context, sc credz.CredentialzClient, ch <-chan []string, targetAddr string) {
			s := &GNSICredentialzServer{
				consoleCredMetadata: NewConsoleCredMetadata(),
			}
			if err := s.saveConsoleCredentialsFreshness("../testdata/gnsi/console-version.json"); err != nil {
				t.Fatal(err)
			}
		},
	},
}

// TestConsoleServer tests implementation of gnsi.Ssh server.
func TestGnsiCredzConsoleServer(t *testing.T) {
	cfg := &Config{Port: 8081}
	s := createCredzServer(t, cfg)
	go runServer(t, s)
	defer s.Stop()

	var dbusListener chan []string
	var done chan bool
	// dbusCaller is a package variable
	dbusListener, done, dbusCaller = newMockConsoleDbusServer()
	defer func() { close(dbusListener); close(done) }()

	// Create a gNSI.ssh client and connect it to the gNSI.ssh server.
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}
	targetAddr := fmt.Sprintf("127.0.0.1:%d", s.config.Port)
	conn, err := grpc.Dial(targetAddr, opts...)
	if err != nil {
		t.Fatalf("Dialing to %s failed: %v", targetAddr, err)
	}
	defer conn.Close()
	sc := credz.NewCredentialzClient(conn)
	for _, tc := range consoleTests {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		t.Run(tc.desc, func(t *testing.T) {
			tc.f(t, ctx, sc, dbusListener, targetAddr)
			credzMu.Lock()
			credzMu.Unlock()
		})
		cancel()
	}

	done <- true
}

var consoleTestsBadDBUS = []struct {
	desc string
	f    func(t *testing.T, ctx context.Context, sc credz.CredentialzClient, ch <-chan []string)
}{
	{
		desc: "RPC fails",
		f: func(t *testing.T, ctx context.Context, sc credz.CredentialzClient, ch <-chan []string) {
			if _, err := sc.RotateAccountCredentials(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case msg := <-ch:
				t.Fatalf("Unexpected DBUS msg %v", msg)
			default:
			}
		},
	},
}

// TestConsoleServerNoDBUS tests implementation of gnsi.console server.
func TestGnsiCredzConsoleServerNoDBUS(t *testing.T) {
	cfg := &Config{Port: 8081}
	s := createCredzServer(t, cfg)
	go runServer(t, s)
	defer s.Stop()

	var dbusListener chan []string
	var done chan bool
	// dbusCaller is a package variable
	dbusListener, done, dbusCaller = newFailingConsoleDbusServer()
	defer func() { close(dbusListener); close(done) }()

	// Create a gNSI.ssh client and connect it to the gNSI.ssh server.
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}
	targetAddr := fmt.Sprintf("127.0.0.1:%d", s.config.Port)
	conn, err := grpc.Dial(targetAddr, opts...)
	if err != nil {
		t.Fatalf("Dialing to %s failed: %v", targetAddr, err)
	}
	defer conn.Close()
	credzClient := credz.NewCredentialzClient(conn)

	var mu sync.Mutex
	for _, tc := range consoleTestsBadDBUS {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		t.Run(tc.desc, func(t *testing.T) {
			mu.Lock()
			defer mu.Unlock()
			tc.f(t, ctx, credzClient, dbusListener)
		})
		cancel()
	}

	// Shutdown the mock gnsi_console service server.
	done <- true
	s.gnsiCredz.saveConsoleCredentialsFreshness(s.config.ConsoleCredMetaFile)
}

func newMockConsoleDbusServer() (chan []string, chan bool, ssc.Caller) {
	ch := make(chan []string, 10)
	pass := make(chan []string, 10)
	done := make(chan bool, 1)
	go func() {
		checkpoint := false
		var mu sync.Mutex
		for {
			select {
			case <-done: // if cancel() execute
				log.Infoln("Shutting down the DBUS server.")
				return
			case msg := <-ch:
				mu.Lock()
				fmt.Printf("Received %v. Sending it out.\n", msg)
				switch msg[0] {
				case expectedConsoleCreateCmd:
					if checkpoint {
						log.Fatal("mock gnsi_console service: checkpoint already exists.")
					}
					checkpoint = true
				case expectedConsoleDeleteCmd:
					if !checkpoint {
						log.Fatal("mock gnsi_console service: checkpoint does not exists.")
					}
					checkpoint = false
				case expectedConsoleRestoreCmd:
					if !checkpoint {
						log.Fatal("mock gnsi_console service: checkpoint does not exists.")
					}
					checkpoint = false
				case expectedConsoleSetCmd:
					if !checkpoint {
						log.Fatal("mock gnsi_console service: set without checkpoint.")
					}
				default:
					log.Fatalf(`mock gnsi_console service: unknown service: "%v"`, msg[0])
				}
				mu.Unlock()
				pass <- msg
			}
		}
	}()

	return pass, done, &ssc.SpyDbusCaller{Command: ch}
}

func newFailingConsoleDbusServer() (chan []string, chan bool, ssc.Caller) {
	ch := make(chan []string, 1)
	done := make(chan bool, 1)
	go func() {
		for {
			select {
			case <-done: // if cancel() execute
				log.Infoln("Shutting down the faulty DBUS server.")
				return
			case msg := <-ch:
				fmt.Printf("Received %v. Sending it out.\n", msg)
				ch <- msg
			}
		}
	}()

	return ch, done, &ssc.FailDbusCaller{}
}

func TestGnsiCredzUnimplemented(t *testing.T) {
	cs := GNSICredentialzServer{}
	t.Run("CanGenerateKeyUnimplemented", func(t *testing.T) {
		if _, err := cs.CanGenerateKey(nil, nil); status.Code(err) != codes.Unimplemented {
			t.Errorf("expected: Unimplemented, got: %+v", err)
		}
	})
	t.Run("GetPublicKeysUnimplemented", func(t *testing.T) {
		if _, err := cs.GetPublicKeys(nil, nil); status.Code(err) != codes.Unimplemented {
			t.Errorf("expected: Unimplemented, got: %+v", err)
		}
	})
}

var sshAcctIncompleteMsg = []struct {
	desc string
	msg  *credz.RotateAccountCredentialsRequest
}{
	{
		desc: "user; missing version",
		msg: &credz.RotateAccountCredentialsRequest{
			Request: &credz.RotateAccountCredentialsRequest_User{
				User: &credz.AuthorizedUsersRequest{
					Policies: []*credz.UserPolicy{
						{
							Account: "root",
							// Version:   "root-version-2",
							CreatedOn: uint64(time.Now().Unix()),
							AuthorizedPrincipals: &credz.UserPolicy_SshAuthorizedPrincipals{
								AuthorizedPrincipals: []*credz.UserPolicy_SshAuthorizedPrincipal{
									&credz.UserPolicy_SshAuthorizedPrincipal{
										AuthorizedUser: "alice",
									},
								},
							},
						},
					},
				},
			},
		},
	},
	{
		desc: "user; missing users",
		msg: &credz.RotateAccountCredentialsRequest{
			Request: &credz.RotateAccountCredentialsRequest_User{
				User: &credz.AuthorizedUsersRequest{
					Policies: []*credz.UserPolicy{
						{
							Account:   "root",
							Version:   "root-version-2",
							CreatedOn: uint64(time.Now().Unix()),
							AuthorizedPrincipals: &credz.UserPolicy_SshAuthorizedPrincipals{
								AuthorizedPrincipals: []*credz.UserPolicy_SshAuthorizedPrincipal{},
							},
						},
					},
				},
			},
		},
	},
	{
		desc: "user; missing user list",
		msg: &credz.RotateAccountCredentialsRequest{
			Request: &credz.RotateAccountCredentialsRequest_User{
				User: &credz.AuthorizedUsersRequest{
					Policies: []*credz.UserPolicy{
						{
							Account:   "root",
							Version:   "root-version-2",
							CreatedOn: uint64(time.Now().Unix()),
						},
					},
				},
			},
		},
	},
	{
		desc: "user;missing account",
		msg: &credz.RotateAccountCredentialsRequest{
			Request: &credz.RotateAccountCredentialsRequest_User{
				User: &credz.AuthorizedUsersRequest{
					Policies: []*credz.UserPolicy{
						{
							Version:   "root-version-2",
							CreatedOn: uint64(time.Now().Unix()),
						},
					},
				},
			},
		},
	},
	{
		desc: "user; missing timestamp",
		msg: &credz.RotateAccountCredentialsRequest{
			Request: &credz.RotateAccountCredentialsRequest_User{
				User: &credz.AuthorizedUsersRequest{
					Policies: []*credz.UserPolicy{
						{
							Account: "root",
							Version: "root-version-2",
						},
					},
				},
			},
		},
	},
	{
		desc: "user; missing user",
		msg: &credz.RotateAccountCredentialsRequest{
			Request: &credz.RotateAccountCredentialsRequest_User{},
		},
	},
	{
		desc: "cred; missing account",
		msg: &credz.RotateAccountCredentialsRequest{
			Request: &credz.RotateAccountCredentialsRequest_Credential{
				Credential: &credz.AuthorizedKeysRequest{
					Credentials: []*credz.AccountCredentials{
						{
							Version:   "root-version-1",
							CreatedOn: uint64(time.Now().Unix()),
							AuthorizedKeys: []*credz.AccountCredentials_AuthorizedKey{
								{
									AuthorizedKey: []byte("Authorized-key #1"),
									Options: []*credz.Option{
										{
											Key:   &credz.Option_Name{Name: "from"},
											Value: "*.sales.example.net,!pc.sales.example.net",
										},
									},
								},
								{
									AuthorizedKey: []byte("Authorized-key #2"),
									KeyType:       credz.KeyType_KEY_TYPE_UNSPECIFIED,
									Description:   "test#2",
								},
							},
						},
					},
				},
			},
		},
	},
	{
		desc: "cred; missing keys #1",
		msg: &credz.RotateAccountCredentialsRequest{
			Request: &credz.RotateAccountCredentialsRequest_Credential{
				Credential: &credz.AuthorizedKeysRequest{
					Credentials: []*credz.AccountCredentials{
						{
							Account:        "root",
							Version:        "root-version-1",
							CreatedOn:      uint64(time.Now().Unix()),
							AuthorizedKeys: []*credz.AccountCredentials_AuthorizedKey{},
						},
					},
				},
			},
		},
	},
	{
		desc: "cred; missing cred",
		msg: &credz.RotateAccountCredentialsRequest{
			Request: &credz.RotateAccountCredentialsRequest_Credential{},
		},
	},
	{
		desc: "cred; missing timestamp",
		msg: &credz.RotateAccountCredentialsRequest{
			Request: &credz.RotateAccountCredentialsRequest_Credential{
				Credential: &credz.AuthorizedKeysRequest{
					Credentials: []*credz.AccountCredentials{
						{
							Account: "root",
							Version: "root-version-1",
							AuthorizedKeys: []*credz.AccountCredentials_AuthorizedKey{
								{AuthorizedKey: []byte("Authorized-key #2")},
							},
						},
					},
				},
			},
		},
	},
	{
		desc: "cred; missing account",
		msg: &credz.RotateAccountCredentialsRequest{
			Request: &credz.RotateAccountCredentialsRequest_Credential{
				Credential: &credz.AuthorizedKeysRequest{
					Credentials: []*credz.AccountCredentials{
						{
							Version:   "root-version-1",
							CreatedOn: uint64(time.Now().Unix()),
							AuthorizedKeys: []*credz.AccountCredentials_AuthorizedKey{
								{AuthorizedKey: []byte("Authorized-key #2")},
							},
						},
					},
				},
			},
		},
	},
	{
		desc: "cred; missing version",
		msg: &credz.RotateAccountCredentialsRequest{
			Request: &credz.RotateAccountCredentialsRequest_Credential{
				Credential: &credz.AuthorizedKeysRequest{
					Credentials: []*credz.AccountCredentials{
						{
							Account:   "root",
							CreatedOn: uint64(time.Now().Unix()),
						},
					},
				},
			},
		},
	},
}

var sshHostIncompleteMsg = []struct {
	desc string
	msg  *credz.RotateHostParametersRequest
}{
	{
		desc: "host missing request",
		msg: &credz.RotateHostParametersRequest{
			Request: &credz.RotateHostParametersRequest_SshCaPublicKey{},
		},
	},
	{
		desc: "host missing keys #1",
		msg: &credz.RotateHostParametersRequest{
			Request: &credz.RotateHostParametersRequest_SshCaPublicKey{
				SshCaPublicKey: &credz.CaPublicKeyRequest{
					SshCaPublicKeys: []*credz.PublicKey{&credz.PublicKey{
						PublicKey:   []byte{},
						KeyType:     credz.KeyType_KEY_TYPE_UNSPECIFIED,
						Description: "test",
					}},
					Version:   "CA-trust-bundle-1",
					CreatedOn: uint64(time.Now().Unix()),
				},
			},
		},
	},
	{
		desc: "host missing timestamp",
		msg: &credz.RotateHostParametersRequest{
			Request: &credz.RotateHostParametersRequest_SshCaPublicKey{
				SshCaPublicKey: &credz.CaPublicKeyRequest{
					Version:   "CA-trust-bundle-1",
					CreatedOn: uint64(time.Now().Unix()),
				},
			},
		},
	},
	{
		desc: "host missing version",
		msg: &credz.RotateHostParametersRequest{
			Request: &credz.RotateHostParametersRequest_SshCaPublicKey{
				SshCaPublicKey: &credz.CaPublicKeyRequest{
					Version:   "CA-trust-bundle-1",
					CreatedOn: uint64(time.Now().Unix()),
				},
			},
		},
	},
}

func TestGnsiCredzMissingRequests(t *testing.T) {
	cfg := &Config{Port: 8081}
	s := createCredzServer(t, cfg)
	go runServer(t, s)
	defer s.Stop()

	var dbusListener chan []string
	var done chan bool
	// dbusCaller is a package variable
	dbusListener, done, dbusCaller = newMockSshDbusServer()
	defer func() { close(dbusListener); close(done) }()

	// Create a gNSI.ssh client and connect it to the gNSI.ssh server.
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}
	targetAddr := fmt.Sprintf("127.0.0.1:%d", s.config.Port)
	conn, err := grpc.Dial(targetAddr, opts...)
	if err != nil {
		t.Fatalf("Dialing to %s failed: %v", targetAddr, err)
	}
	defer conn.Close()
	sc := credz.NewCredentialzClient(conn)

	for _, m := range sshAcctIncompleteMsg {
		t.Run(m.desc, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			c, err := sc.RotateAccountCredentials(ctx)
			if err != nil {
				t.Fatalf("error opening a streaming RotateAccountCredentials RPC: %v", err)
			}
			if err = c.Send(m.msg); err != nil {
				t.Fatalf("error sending an incomplete '%v'message: %v", m.desc, err)
			}
			if _, err := c.Recv(); status.Code(err) != codes.Aborted {
				t.Errorf("expected: Aborted, got: %+v", err)
			}
			if dbus := dbusListen(dbusListener); !reflect.DeepEqual(dbus, []string{expectedSshCreateCmd, ""}) {
				t.Errorf("DBUS Failure wanted: [%v ]; got: %+v", expectedSshCreateCmd, dbus)
			}
			if dbus := dbusListen(dbusListener); !reflect.DeepEqual(dbus, []string{expectedSshRestoreCmd, ""}) {
				t.Errorf("DBUS Failure wanted %v; got: %v", expectedSshRestoreCmd, dbus)
			}
		})
	}
	for _, m := range sshHostIncompleteMsg {
		t.Run(m.desc, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			c, err := sc.RotateHostParameters(ctx)
			if err != nil {
				t.Fatalf("error opening a streaming RotateHostParameters RPC: %v", err)
			}
			if err = c.Send(m.msg); err != nil {
				t.Fatalf("error sending an incomplete '%v'message: %v", m.desc, err)
			}
			if _, err := c.Recv(); status.Code(err) != codes.Aborted {
				t.Errorf("expected: Aborted, got: %+v", err)
			}
			if dbus := dbusListen(dbusListener); !reflect.DeepEqual(dbus, []string{expectedSshCreateCmd, ""}) {
				t.Errorf("DBUS Failure wanted: [%v ]; got: %+v", expectedSshCreateCmd, dbus)
			}
			if dbus := dbusListen(dbusListener); !reflect.DeepEqual(dbus, []string{expectedSshRestoreCmd, ""}) {
				t.Errorf("DBUS Failure wanted %v; got: %v", expectedSshRestoreCmd, dbus)
			}
		})
	}
}

// glomeTests contains the test cases for valid and invalid Glome transactions.

var glomeTests = []struct {
	desc string
	f    func(t *testing.T, ctx context.Context, credzClient credz.CredentialzClient, ch <-chan []string)
}{
	{
		// Valid scenario where a client sends a GlomeRequest to enable GLOME followed by a Finalize request.
		// Stream is opened, GlomeRequest is sent which triggers a DBus call with 'push_config' cmd,
		// FinalizeRequest is sent committing the transaction, then the stream is closed.
		desc: "GLOME scenario: glome enable, finalize",
		f: func(t *testing.T, ctx context.Context, credzClient credz.CredentialzClient, ch <-chan []string) {
			var rhpClient credz.Credentialz_RotateHostParametersClient
			var err error
			runRotateHostParameters("OpenStream", ch, t, []mockDBusMessage{}, func() error {
				rhpClient, err = credzClient.RotateHostParameters(ctx)
				return err
			})
			runRotateHostParameters("GlomeRequest", ch, t,
				[]mockDBusMessage{glomePushConfigDbusMessageForValidRequest},
				func() error {
					if err = rhpClient.Send(validRotateHostParametersGlomeRequest); err != nil {
						t.Fatalf("rhpClient.Send() failed; err: %v", err)
					}
					resp, err := rhpClient.Recv()
					if err != nil {
						t.Fatalf("rhpClient.Recv() failed; err: %v", err)
					}
					if resp.GetGlome() == nil {
						t.Fatal("resp.GetGlome() is nil; expected a GlomeResponse")
					}
					return nil
				})
			runRotateHostParameters("FinalizeRequest", ch, t, []mockDBusMessage{}, func() error {
				if err = rhpClient.Send(&credz.RotateHostParametersRequest{
					Request: &credz.RotateHostParametersRequest_Finalize{}}); err != nil {
					t.Fatalf("rhpClient.Send() failed; err: %v", err)
				}
				// Check that the client receives an EOF after the Finalize request.
				if resp, err := rhpClient.Recv(); err != io.EOF || resp.GetResponse() != nil {
					t.Errorf("rhpClient.Recv() returned resp: %v, err: %v; expected no response and error: %v", resp, err, io.EOF)
				}
				return nil
			})
		},
	},
	{
		// Valid scenario where a client sends a GlomeRequest to disable GLOME followed by a Finalize request.
		// Stream is opened, GlomeRequest is sent which triggers a DBus call with 'push_config' cmd,
		// FinalizeRequest is sent committing the transaction, then the stream is closed.
		desc: "GLOME scenario: glome disable, finalize",
		f: func(t *testing.T, ctx context.Context, credzClient credz.CredentialzClient, ch <-chan []string) {
			var rhpClient credz.Credentialz_RotateHostParametersClient
			var err error
			runRotateHostParameters("OpenStream", ch, t, []mockDBusMessage{}, func() error {
				rhpClient, err = credzClient.RotateHostParameters(ctx)
				return err
			})
			runRotateHostParameters("GlomeRequest", ch, t,
				[]mockDBusMessage{glomePushConfigDbusMessageForDisableRequest},
				func() error {
					if err = rhpClient.Send(rotateHostParametersDisableGlomeRequest); err != nil {
						t.Fatalf("rhpClient.Send() failed; err: %v", err)
					}
					resp, err := rhpClient.Recv()
					if err != nil {
						t.Fatalf("rhpClient.Recv() failed; err: %v", err)
					}
					if resp.GetGlome() == nil {
						t.Fatal("resp.GetGlome() is nil; expected a GlomeResponse")
					}
					return nil
				})
			runRotateHostParameters("FinalizeRequest", ch, t, []mockDBusMessage{}, func() error {
				if err = rhpClient.Send(&credz.RotateHostParametersRequest{
					Request: &credz.RotateHostParametersRequest_Finalize{}}); err != nil {
					t.Fatalf("rhpClient.Send() failed; err: %v", err)
				}
				// Check that the client receives an EOF after the Finalize request.
				if resp, err := rhpClient.Recv(); err != io.EOF || resp.GetResponse() != nil {
					t.Errorf("rhpClient.Recv() returned resp: %v, err: %v; expected no response and error: %v", resp, err, io.EOF)
				}
				return nil
			})
		},
	},
	{
		// Invalid scenario where a client sends a GlomeRequest but not a Finalize request before closing the stream.
		// Stream is opened, GlomeRequest is sent which triggers a DBus call with 'push_config' cmd,
		// Stream is closed without a Finalize request, the server aborts the transaction and sends 'restore_checkpoint' DBus message,
		// the server returns an Aborted error to the client.
		desc: "GLOME scenario: glome, no finalize",
		f: func(t *testing.T, ctx context.Context, credzClient credz.CredentialzClient, ch <-chan []string) {
			var rhpClient credz.Credentialz_RotateHostParametersClient
			var err error
			runRotateHostParameters("OpenStream", ch, t, []mockDBusMessage{}, func() error {
				rhpClient, err = credzClient.RotateHostParameters(ctx)
				return err
			})
			runRotateHostParameters("GlomeRequest", ch, t,
				[]mockDBusMessage{glomePushConfigDbusMessageForValidRequest},
				func() error {
					if err = rhpClient.Send(validRotateHostParametersGlomeRequest); err != nil {
						t.Fatalf("rhpClient.Send() failed; err: %v", err)
					}
					resp, err := rhpClient.Recv()
					if err != nil {
						t.Fatalf("rhpClient.Recv() failed; err: %v", err)
					}
					if resp.GetGlome() == nil {
						t.Fatal("resp.GetGlome() is nil; expected a GlomeResponse")
					}
					return nil
				})
			runRotateHostParameters("CloseStream", ch, t, []mockDBusMessage{glomeRestoreDbusMessage},
				func() error {
					if err = rhpClient.CloseSend(); err != nil {
						t.Fatalf("rhpClient.CloseSend() failed; err: %v", err)
					}
					// Check that the client receives an Aborted error from the server due to closing the stream without a Finalize request.
					if resp, err := rhpClient.Recv(); status.Code(err) != codes.Aborted || resp != nil {
						t.Errorf("rhpClient.Recv() returned resp: %v, err: %v; expected error status code: %v", resp, err, codes.Aborted)
					}
					return nil
				})
		},
	},
	{
		// Invalid scenario where a client sends a Finalize request without a Glome request.
		// Stream is opened, FinalizeRequest is sent, the server aborts the transaction and sends 'restore_checkpoint' DBus message,
		// the server returns an Aborted error to the client.
		desc: "GLOME scenario: no glome, finalize",
		f: func(t *testing.T, ctx context.Context, credzClient credz.CredentialzClient, ch <-chan []string) {
			var rhpClient credz.Credentialz_RotateHostParametersClient
			var err error
			runRotateHostParameters("OpenStream", ch, t, []mockDBusMessage{}, func() error {
				rhpClient, err = credzClient.RotateHostParameters(ctx)
				return err
			})
			runRotateHostParameters("FinalizeRequest", ch, t, []mockDBusMessage{},
				func() error {
					if err = rhpClient.Send(&credz.RotateHostParametersRequest{
						Request: &credz.RotateHostParametersRequest_Finalize{}}); err != nil {
						//t.Fatal("rhpClient.Send() failed; err: %v", err)
						t.Fatalf("rhpClient.Send() failed; err: %v", err)
					}
					// Check that the client receives an Aborted error due to receiving a Finalize request without a Glome request.
					if resp, err := rhpClient.Recv(); status.Code(err) != codes.Aborted || resp != nil {
						t.Errorf("rhpClient.Recv() returned resp: %v, err: %v; expected error status code: %v", resp, err, codes.Aborted)
					}
					return nil
				})
		},
	},
	{
		// Invalid scenario where a client sends a second GlomeRequest after the first one.
		// Stream is opened, GlomeRequest is sent which triggers a DBus call with 'push_config' cmd,
		// Second GlomeRequest is sent, the server aborts the transaction and sends 'restore_checkpoint' DBus message,
		// the server returns an Aborted error to the client.
		desc: "GLOME scenario: glome, glome",
		f: func(t *testing.T, ctx context.Context, credzClient credz.CredentialzClient, ch <-chan []string) {
			var rhpClient credz.Credentialz_RotateHostParametersClient
			var err error
			runRotateHostParameters("OpenStream", ch, t, []mockDBusMessage{}, func() error {
				rhpClient, err = credzClient.RotateHostParameters(ctx)
				return err
			})
			runRotateHostParameters("GlomeRequest", ch, t,
				[]mockDBusMessage{glomePushConfigDbusMessageForValidRequest},
				func() error {
					if err = rhpClient.Send(validRotateHostParametersGlomeRequest); err != nil {
						t.Fatalf("rhpClient.Send() failed; err: %v", err)
					}
					resp, err := rhpClient.Recv()
					if err != nil {
						t.Fatalf("rhpClient.Recv() failed; err: %v", err)
					}
					if resp.GetGlome() == nil {
						t.Fatal("resp.GetGlome() is nil; expected a GlomeResponse")
					}
					return nil
				})
			runRotateHostParameters("SecondGlomeRequest", ch, t, []mockDBusMessage{glomeRestoreDbusMessage},
				func() error {
					if err = rhpClient.Send(&credz.RotateHostParametersRequest{
						Request: &credz.RotateHostParametersRequest_Glome{
							Glome: &credz.GlomeRequest{
								Enabled:    true,
								Key:        "test-key-2",
								KeyVersion: 2,
								UrlPrefix:  "https://test.com/",
							},
						},
					}); err != nil {
						t.Fatalf("rhpClient.Send() failed; err: %v", err)
					}
					// Check that the client receives an Aborted error due to another Glome request after the first one. The server expects
					// Finalize request after the first Glome request.
					if resp, err := rhpClient.Recv(); status.Code(err) != codes.Aborted || resp != nil {
						t.Errorf("rhpClient.Recv() returned resp: %v, err: %v; expected error status code: %v", resp, err, codes.Aborted)
					}
					return nil
				})
		},
	},
	{
		// Invalid scenario where a client sends a GlomeRequest followed by a SshCaPublicKey request.
		// Stream is opened, GlomeRequest is sent which triggers a DBus call with 'push_config' cmd,
		// SshCaPublicKey request is sent, the server aborts the transaction and sends 'restore_checkpoint' DBus message,
		// the server returns an Aborted error to the client.
		desc: "GLOME scenario: glome, sshCaPublicKey",
		f: func(t *testing.T, ctx context.Context, credzClient credz.CredentialzClient, ch <-chan []string) {
			var rhpClient credz.Credentialz_RotateHostParametersClient
			var err error
			runRotateHostParameters("OpenStream", ch, t, []mockDBusMessage{}, func() error {
				rhpClient, err = credzClient.RotateHostParameters(ctx)
				return err
			})
			runRotateHostParameters("GlomeRequest", ch, t,
				[]mockDBusMessage{glomePushConfigDbusMessageForValidRequest},
				func() error {
					if err = rhpClient.Send(validRotateHostParametersGlomeRequest); err != nil {
						t.Fatalf("rhpClient.Send() failed; err: %v", err)
					}
					resp, err := rhpClient.Recv()
					if err != nil {
						t.Fatalf("rhpClient.Recv() failed; err: %v", err)
					}
					if resp.GetGlome() == nil {
						t.Fatal("resp.GetGlome() is nil; expected a GlomeResponse")
					}
					return nil
				})
			runRotateHostParameters("ModifySshCaPublicKey", ch, t,
				[]mockDBusMessage{glomeRestoreDbusMessage},
				func() error {
					if err = rhpClient.Send(&credz.RotateHostParametersRequest{
						Request: &credz.RotateHostParametersRequest_SshCaPublicKey{
							SshCaPublicKey: &credz.CaPublicKeyRequest{
								Version:   "CA-trust-bundle-1",
								CreatedOn: 123,
								SshCaPublicKeys: []*credz.PublicKey{
									&credz.PublicKey{
										PublicKey:   []byte("TEST-CERT #1"),
										KeyType:     credz.KeyType_KEY_TYPE_ED25519,
										Description: "test#1",
									},
									&credz.PublicKey{
										PublicKey:   []byte("TEST-CERT #2"),
										KeyType:     credz.KeyType_KEY_TYPE_RSA_2048,
										Description: "test#2",
									},
								},
							},
						},
					}); err != nil {
						t.Fatalf("rhpClient.Send() failed; err: %v", err)
					}
					// Check that the client receives an Aborted error due to SSH CA public key request after Glome request.
					if resp, err := rhpClient.Recv(); status.Code(err) != codes.Aborted || resp != nil {
						t.Errorf("rhpClient.Recv() returned resp: %v, err: %v; expected error status code: %v", resp, err, codes.Aborted)
					}
					return nil
				})
		},
	},
	{
		desc: "GLOME scenario: invalid url_prefix in glome request", // Invalid case.
		f: func(t *testing.T, ctx context.Context, credzClient credz.CredentialzClient, ch <-chan []string) {
			var rhpClient credz.Credentialz_RotateHostParametersClient
			var err error
			runRotateHostParameters("OpenStream", ch, t, []mockDBusMessage{}, func() error {
				rhpClient, err = credzClient.RotateHostParameters(ctx)
				return err
			})
			runRotateHostParameters("GlomeRequest", ch, t,
				[]mockDBusMessage{glomeRestoreDbusMessage},
				func() error {
					if err = rhpClient.Send(&credz.RotateHostParametersRequest{
						Request: &credz.RotateHostParametersRequest_Glome{
							Glome: &credz.GlomeRequest{
								Enabled:    true,
								Key:        "test-key",
								KeyVersion: 1,
								UrlPrefix:  "%%test.com",
							},
						},
					}); err != nil {
						t.Fatalf("rhpClient.Send() failed; err: %v", err)
					}
					// Check that the client receives an Aborted error due to invalid url_prefix in glome request.
					resp, err := rhpClient.Recv()
					if status.Code(err) != codes.Aborted || resp != nil {
						t.Errorf("rhpClient.Recv() returned resp: %v, err: %v; expected error status code: %v", resp, err, codes.Aborted)
					}
					// Check that the error message contains the expected error message.
					if !strings.Contains(err.Error(), "GLOME URL prefix is not valid") {
						t.Errorf("rhpClient.Recv() returned err: %v; expected error message: %v", err, "GLOME URL prefix is not valid")
					}
					return nil
				})
		},
	},
	{
		desc: "GLOME scenario: glome enabled false, but other fields are set", // Invalid case.
		f: func(t *testing.T, ctx context.Context, credzClient credz.CredentialzClient, ch <-chan []string) {
			var rhpClient credz.Credentialz_RotateHostParametersClient
			var err error
			runRotateHostParameters("OpenStream", ch, t, []mockDBusMessage{}, func() error {
				rhpClient, err = credzClient.RotateHostParameters(ctx)
				return err
			})
			runRotateHostParameters("GlomeRequest", ch, t,
				[]mockDBusMessage{glomeRestoreDbusMessage},
				func() error {
					if err = rhpClient.Send(&credz.RotateHostParametersRequest{
						Request: &credz.RotateHostParametersRequest_Glome{
							Glome: &credz.GlomeRequest{
								Enabled:    false,
								Key:        "test-key",
								KeyVersion: 1,
								UrlPrefix:  "%%test.com",
							},
						},
					}); err != nil {
						t.Fatalf("rhpClient.Send() failed; err: %v", err)
					}
					// Check that the client receives an Aborted error due to invalid url_prefix in glome request.
					resp, err := rhpClient.Recv()
					if status.Code(err) != codes.Aborted || resp != nil {
						t.Errorf("rhpClient.Recv() returned resp: %v, err: %v; expected error status code: %v", resp, err, codes.Aborted)
					}
					// Check that the error message contains the expected error message.
					if !strings.Contains(err.Error(), "GLOME key, key_version, and url_prefix cannot be set if GLOME is disabled") {
						t.Errorf("rhpClient.Recv() returned err: %v; expected error message: %v", err, "GLOME key, key_version, and url_prefix cannot be set if GLOME is disabled")
					}
					return nil
				})
		},
	},
	{
		// This test case is to ensure that the proto JSON marshaling of the GlomeRequest works correctly
		// with special characters that need escaping.
		desc: "GLOME scenario: proto json marshaling with specical characters",
		f: func(t *testing.T, ctx context.Context, credzClient credz.CredentialzClient, ch <-chan []string) {
			var rhpClient credz.Credentialz_RotateHostParametersClient
			var err error
			runRotateHostParameters("OpenStream", ch, t, []mockDBusMessage{}, func() error {
				rhpClient, err = credzClient.RotateHostParameters(ctx)
				return err
			})

			// GlomeRequest with special characters in the url_prefix field.
			glomeRequestWithSpecialChars := &credz.RotateHostParametersRequest{
				Request: &credz.RotateHostParametersRequest_Glome{
					Glome: &credz.GlomeRequest{
						Enabled:    true,
						Key:        "test-key",
						KeyVersion: 1,
						UrlPrefix:  "https://example.com/?q=\"value\"",
					},
				},
			}

			// The expected JSON string after marshaling and compacting. Quotes and
			// backslashes in the url_prefix field should be escaped.
			expectedDbusMessageWithSpecialChars := mockDBusMessage{
				methodName: expectedGlomePushConfigCmd,
				cmd:        `{"enabled":true,"key":"test-key","key_version":1,"url_prefix":"https://example.com/?q=\"value\""}`,
			}

			runRotateHostParameters("GlomeRequestWithSpecialCharacters", ch, t, []mockDBusMessage{expectedDbusMessageWithSpecialChars},
				func() error {
					if err = rhpClient.Send(glomeRequestWithSpecialChars); err != nil {
						t.Fatalf("rhpClient.Send() failed; err: %v", err)
					}
					resp, err := rhpClient.Recv()
					if err != nil {
						t.Fatalf("rhpClient.Recv() failed; err: %v", err)
					}
					if resp.GetGlome() == nil {
						t.Fatal("resp.GetGlome() is nil; expected a GlomeResponse")
					}
					return nil
				})

			runRotateHostParameters("FinalizeRequest", ch, t, []mockDBusMessage{}, func() error {
				if err = rhpClient.Send(&credz.RotateHostParametersRequest{
					Request: &credz.RotateHostParametersRequest_Finalize{}}); err != nil {
					t.Fatalf("rhpClient.Send() failed; err: %v", err)
				}
				// Check that the client receives an EOF after the Finalize request.
				if resp, err := rhpClient.Recv(); err != io.EOF || resp.GetResponse() != nil {
					t.Errorf("rhpClient.Recv() returned resp: %v, err: %v; expected no response and error: %v", resp, err, io.EOF)
				}
				return nil
			})
		},
	},
}

// TestGnsiCredzGlome tests the valid and invalid Glome use cases. The only
// valid case is Glome request -> Finalize request. All other cases are invalid
// and should return an Aborted error.
func TestGnsiCredzGlome(t *testing.T) {
	cfg := &Config{Port: 8081}
	s := createCredzServer(t, cfg)
	go runServer(t, s)
	defer s.Stop()

	var dbusListener chan []string
	var shutdown chan bool
	dbusListener, shutdown, dbusCaller = newMockGlomeDbusServer()
	defer func() { close(dbusListener); close(shutdown) }()

	// Create a credz client and connect it to the gNSI Glome service.
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	targetAddr := fmt.Sprintf("127.0.0.1:%d", s.config.Port)
	conn, err := grpc.Dial(targetAddr, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
	if err != nil {
		t.Fatalf("Dialing to %s failed: %v", targetAddr, err)
	}
	defer conn.Close()
	credzClient := credz.NewCredentialzClient(conn)

	for _, tc := range glomeTests {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		t.Run(tc.desc, func(t *testing.T) {
			tc.f(t, ctx, credzClient, dbusListener)
			credzMu.Lock()
			credzMu.Unlock()
		})
		cancel()
	}

	// Shutdown the mock Glome DBUS server.
	shutdown <- true
}

// newMockGlomeDbusServer creates a mock DBUS server for Glome that runs in the background as
// a goroutine. It returns:
// - listenerChan: A channel that the test function listens on to receive and verify the DBUS messages.
// - shutdownChan: A channel that is used to signal the mock DBUS server to shutdown.
// - dbusCaller: A spy caller that the gNSI server uses to send DBUS messages to the mock DBUS server.
func newMockGlomeDbusServer() (chan []string, chan bool, ssc.Caller) {
	// dbusServerInputChan is a channel that the code under test (the gNSI server) sends the DBUS messages to.
	dbusServerInputChan := make(chan []string, 10)
	// listenerChan is a channel that the test function listens on to receive and verify the DBUS messages.
	listenerChan := make(chan []string, 10)
	// shutdownChan is used to signal the mock Dbus server's goroutine to shutdown.
	shutdownChan := make(chan bool, 1)

	go func() {
		glomeCheckpoint := false
		for {
			select {
			case <-shutdownChan: // if shutdownChan is signaled from TestGnsiCredzGlome()
				//log.V(lvl.INFO).Infoln("Shutting down the DBUS server.")
				return
			case msg := <-dbusServerInputChan:
				//log.V(lvl.INFO).Infof("DBUS service received %+v", msg)
				switch msg[0] {
				case expectedGlomePushConfigCmd:
					glomeCheckpoint = true
				case expectedGlomeConfigRestoreCmd:
					if !glomeCheckpoint {
						log.Fatal("mock glome service: checkpoint does not exists.")
					}
				default:
					//log.V(lvl.INFO).Infof("mock glome service: unknown service: %+v", msg)
				}
				listenerChan <- msg
			}
		}
	}()
	return listenerChan, shutdownChan, &ssc.SpyDbusCaller{Command: dbusServerInputChan}
}

// TestReadGlomeConfigMetadataFromStateDB tests STATE_DB read for GlomeConfigMetadata.
// The read function is expected to return:
// - GlomeConfigMetadata exists in STATE_DB, return the data.
// - GlomeConfigMetadata does not exist in STATE_DB, return defaultGlomeConfigMetadata.
// - Redis error reading from STATE_DB, return error.
func TestReadGlomeConfigMetadataFromStateDB(t *testing.T) {
	tests := []struct {
		name     string
		dbResult map[string]string
		dbErr    error
		want     *GlomeConfigMetadata
		wantErr  bool
	}{
		{
			name: "glome config metadata exists in STATE_DB",
			dbResult: map[string]string{
				"enabled":      "true",
				"key_version":  "1",
				"last_updated": "1234567890",
			},
			want: &GlomeConfigMetadata{
				Enabled:     true,
				KeyVersion:  1,
				LastUpdated: 1234567890,
			},
			wantErr: false,
		},
		{
			name:     "no glome config metadata in STATE_DB",
			dbResult: map[string]string{},
			want:     defaultGlomeConfigMetadata,
			wantErr:  false,
		},
		{
			name:    "error reading from STATE_DB",
			dbErr:   fmt.Errorf("error reading from STATE_DB"),
			wantErr: true,
		},
		{
			name: "invalid 'enabled' field in STATE_DB",
			dbResult: map[string]string{
				"enabled":      "invalid",
				"key_version":  "1",
				"last_updated": "1234567890",
			},
			wantErr: true,
		},
		{
			name: "invalid 'key_version' field in STATE_DB",
			dbResult: map[string]string{
				"enabled":      "true",
				"key_version":  "invalid",
				"last_updated": "1234567890",
			},
			wantErr: true,
		},
		{
			name: "invalid 'last_updated' field in STATE_DB",
			dbResult: map[string]string{
				"enabled":      "true",
				"key_version":  "1",
				"last_updated": "invalid",
			},
			wantErr: true,
		},
	}

	for _, test := range tests {
		test := test // Capture range variable to avoid race conditions.
		t.Run(test.name, func(t *testing.T) {
			// Create redismock client
			mockClient, mock := redismock.NewClientMock()
			srv := &GNSICredentialzServer{stateDbClient: mockClient}

			// Set expectation for HGetAll call.
			expected := mock.ExpectHGetAll(stateDbKeyForGlome)
			if test.dbErr != nil {
				// If dbErr is set for the test, tell the mock to return the error.
				expected.SetErr(test.dbErr)
			} else {
				// Otherwise, tell the mock to return the expected dbResult.
				expected.SetVal(test.dbResult)
			}

			got, err := srv.readGlomeConfigMetadataFromStateDB(context.Background())
			if (err != nil) != test.wantErr {
				t.Errorf("readGlomeConfigMetadataFromStateDB() error = %v, but wantErr = %v", err, test.wantErr)
			}

			if !test.wantErr {
				if diff := cmp.Diff(test.want, got); diff != "" {
					t.Errorf("readGlomeConfigMetadataFromStateDB() returned an unexpected diff (-want +got): %v", diff)
				}
			}

			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("mock expectations were not met: %s", err)
			}
		})
	}
}

// TestWriteGlomeConfigMetadataToStateDB tests STATE_DB write for GlomeConfigMetadata.
// The write function is expected to:
// - Write the new GlomeConfigMetadata to STATE_DB.
// - Return error if newGlomeConfigMetadata (data to be written) is nil.
// - Return error if there is Redis error writing to STATE_DB.
func TestWriteGlomeConfigMetadataToStateDB(t *testing.T) {
	tests := []struct {
		name                string
		newData             *GlomeConfigMetadata
		dbErr               error
		wantErr             bool
		needMultiGoroutines bool
	}{
		{
			name: "success",
			newData: &GlomeConfigMetadata{
				Enabled:     true,
				KeyVersion:  1,
				LastUpdated: 1234567890,
			},
			wantErr: false,
		},
		{
			name:    "newGlomeConfigMetadata is nil",
			newData: nil,
			wantErr: true,
		},
		{
			name: "error writing to STATE_DB",
			newData: &GlomeConfigMetadata{
				Enabled:     true,
				KeyVersion:  1,
				LastUpdated: 1234567890,
			},
			dbErr:   fmt.Errorf("error writing to STATE_DB"),
			wantErr: true,
		},
		{
			name: "concurrent writes to STATE_DB are safe",
			newData: &GlomeConfigMetadata{
				Enabled:     true,
				KeyVersion:  1,
				LastUpdated: 1234567890,
			},
			wantErr:             false,
			needMultiGoroutines: true,
		},
	}

	for _, test := range tests {
		test := test // Capture range variable to avoid race conditions.
		t.Run(test.name, func(t *testing.T) {
			// Create redismock client
			mockClient, mock := redismock.NewClientMock()
			srv := &GNSICredentialzServer{stateDbClient: mockClient}

			// If needMultiGoroutines is set for the test, run multiple goroutines to test concurrent writes to STATE_DB.
			if test.needMultiGoroutines {
				var wg sync.WaitGroup
				numGoroutines := 10
				for i := 0; i < numGoroutines; i++ {
					mock.ExpectHSet(stateDbKeyForGlome, "enabled", test.newData.Enabled, "key_version", test.newData.KeyVersion, "last_updated", test.newData.LastUpdated).SetVal(1)
				}
				for i := 0; i < numGoroutines; i++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						err := srv.writeGlomeConfigMetadataToStateDB(context.Background(), test.newData)
						if (err != nil) != test.wantErr {
							t.Errorf("writeGlomeConfigMetadataToStateDB() error = %v, but wantErr = %v", err, test.wantErr)
						}
					}()
				}
				wg.Wait()
			} else {
				// Otherwise, run the test in a single goroutine.
				// Set expectation for HSet call.
				if test.newData != nil {
					expected := mock.ExpectHSet(stateDbKeyForGlome, "enabled", test.newData.Enabled, "key_version", test.newData.KeyVersion, "last_updated", test.newData.LastUpdated)
					if test.dbErr != nil {
						// If dbErr is set for the test, tell the mock to return the error.
						expected.SetErr(test.dbErr)
					} else {
						// Otherwise, tell the mock to succeed.
						expected.SetVal(1)
					}
				}

				err := srv.writeGlomeConfigMetadataToStateDB(context.Background(), test.newData)
				if (err != nil) != test.wantErr {
					t.Errorf("writeGlomeConfigMetadataToStateDB() error = %v, but wantErr = %v", err, test.wantErr)
				}
			}

			// Verify that all expectations were met.
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("mock expectations were not met: %v", err)
			}
		})
	}
}
