package gnmi

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	pb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/sonic-net/sonic-gnmi/common_utils"
	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// TestAuthenticateRecordsTarget covers cert + JWT gates for authTarget gnmi_records
// (Subscribe uses writeAccess=false).
func TestAuthenticateRecordsTarget(t *testing.T) {
	cfgCert := &Config{
		ConfigTableName: "GNMI_CLIENT_CERT",
		UserAuth:        AuthTypes{"password": false, "cert": true, "jwt": false},
	}
	ctx, cancel := CreateAuthorizationCtx()
	defer cancel()

	var roles []string
	certPatch := gomonkey.ApplyFunc(ClientCertAuthenAndAuthor, func(ctx context.Context, _ string, _ bool) (context.Context, error) {
		rc, ctx := common_utils.GetContext(ctx)
		rc.Auth.User = "certname1"
		rc.Auth.Roles = append([]string(nil), roles...)
		return ctx, nil
	})
	defer certPatch.Reset()

	// gnmi_readonly does not prefix-match gnmi_records; Subscribe (read) still allowed.
	roles = []string{"gnmi_readonly"}
	if _, err := authenticate(cfgCert, ctx, "gnmi_records", false); err != nil {
		t.Fatalf("cert gnmi_readonly Subscribe to gnmi_records should pass: %v", err)
	}

	// Explicit RECORDS readonly role.
	roles = []string{"gnmi_records_readonly"}
	if _, err := authenticate(cfgCert, ctx, "gnmi_records", false); err != nil {
		t.Fatalf("cert gnmi_records_readonly Subscribe should pass: %v", err)
	}
	if _, err := authenticate(cfgCert, ctx, "gnmi_records", true); err == nil {
		t.Fatal("cert gnmi_records_readonly write should fail")
	}

	// Explicit deny for RECORDS.
	roles = []string{"gnmi_records_noaccess"}
	if _, err := authenticate(cfgCert, ctx, "gnmi_records", false); err == nil {
		t.Fatal("cert gnmi_records_noaccess Subscribe should fail")
	}

	// JWT: any authenticated token authorizes Subscribe (no target-prefixed check).
	GenerateJwtSecretKey()
	if JwtValidInt == 0 {
		JwtValidInt = time.Hour
	}
	cfgJWT := &Config{UserAuth: AuthTypes{"password": false, "cert": false, "jwt": true}}
	token := generateJWT("records-user", []string{"gnmi_readonly"}, time.Now().Add(time.Hour))
	jwtCtx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("access_token", token))
	if _, err := authenticate(cfgJWT, jwtCtx, "gnmi_records", false); err != nil {
		t.Fatalf("JWT Subscribe to gnmi_records should pass: %v", err)
	}

	badCtx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("access_token", "not-a-jwt"))
	if _, err := authenticate(cfgJWT, badCtx, "gnmi_records", false); err == nil {
		t.Fatal("invalid JWT should fail for gnmi_records")
	}
}

type jwtCreds struct {
	token string
}

func (c *jwtCreds) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{"access_token": c.token}, nil
}

func (c *jwtCreds) RequireTransportSecurity() bool { return true }

func recordsSubscribeReq() *pb.SubscribeRequest {
	return &pb.SubscribeRequest{
		Request: &pb.SubscribeRequest_Subscribe{
			Subscribe: &pb.SubscriptionList{
				Mode:   pb.SubscriptionList_STREAM,
				Prefix: &pb.Path{Target: "RECORDS"},
				Subscription: []*pb.Subscription{{
					Path: &pb.Path{Elem: []*pb.PathElem{
						{Name: "localhost"},
						{Name: "APPL_DB"},
						{Name: "ROUTE_TABLE"},
						{Name: "10.1.0.0"},
						{Name: "24"},
					}},
					Mode: pb.SubscriptionMode_ON_CHANGE,
				}},
			},
		},
	}
}

// recvRecordsSample waits for one update Notification and a sync_response.
func recvRecordsSample(t *testing.T, stream pb.GNMI_SubscribeClient) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	var gotUpdate, gotSync bool
	for !(gotUpdate && gotSync) {
		recvDone := make(chan struct{})
		var resp *pb.SubscribeResponse
		var err error
		go func() {
			resp, err = stream.Recv()
			close(recvDone)
		}()
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for RECORDS update+sync (update=%v sync=%v)", gotUpdate, gotSync)
		case <-recvDone:
			if err == io.EOF {
				t.Fatalf("stream closed early (update=%v sync=%v)", gotUpdate, gotSync)
			}
			if err != nil {
				t.Fatalf("Subscribe Recv: %v", err)
			}
			switch resp.GetResponse().(type) {
			case *pb.SubscribeResponse_Update:
				gotUpdate = true
			case *pb.SubscribeResponse_SyncResponse:
				gotSync = true
			}
		}
	}
}

func stubRecordsNamespaces(t *testing.T) *gomonkey.Patches {
	t.Helper()
	p := gomonkey.ApplyFunc(sdcfg.GetDbAllNamespaces, func() ([]string, error) {
		return []string{""}, nil
	})
	t.Cleanup(p.Reset)
	return p
}

func TestRecordsSubscribeAuthPasswordE2E(t *testing.T) {
	stubRecordsNamespaces(t)
	mock := gomonkey.ApplyFunc(UserPwAuth, func(username string, passwd string) (bool, error) {
		return true, nil
	})
	defer mock.Reset()
	// Avoid NSS lookup for a synthetic username (same pattern as TestAuthCapabilities).
	pop := gomonkey.ApplyFunc(PopulateAuthStruct, func(username string, auth *common_utils.AuthInfo, r []string) error {
		auth.User = username
		auth.Roles = []string{"gnmi_readonly"}
		return nil
	})
	defer pop.Reset()

	const port int64 = 18091
	s := createAuthServer(t, port)
	go runServer(t, s)
	defer s.ForceStop()

	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	cred := &loginCreds{Username: "records-tester", Password: "dummy"}
	conn, err := grpc.Dial(fmt.Sprintf("127.0.0.1:%d", port),
		grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)),
		grpc.WithPerRPCCredentials(cred),
	)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := pb.NewGNMIClient(conn).Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if err := stream.Send(recordsSubscribeReq()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	recvRecordsSample(t, stream)
}

func TestRecordsSubscribeAuthJWTE2E(t *testing.T) {
	stubRecordsNamespaces(t)
	GenerateJwtSecretKey()
	if JwtValidInt == 0 {
		JwtValidInt = time.Hour
	}

	const port int64 = 18092
	s := createAuthServer(t, port)
	go runServer(t, s)
	defer s.ForceStop()

	token := generateJWT("records-jwt", []string{"gnmi_readonly"}, time.Now().Add(time.Hour))
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	conn, err := grpc.Dial(fmt.Sprintf("127.0.0.1:%d", port),
		grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)),
		grpc.WithPerRPCCredentials(&jwtCreds{token: token}),
	)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := pb.NewGNMIClient(conn).Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if err := stream.Send(recordsSubscribeReq()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	recvRecordsSample(t, stream)
}

func TestRecordsSubscribeAuthRequiredE2E(t *testing.T) {
	stubRecordsNamespaces(t)

	const port int64 = 18093
	s := createAuthServer(t, port)
	go runServer(t, s)
	defer s.ForceStop()

	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	conn, err := grpc.Dial(fmt.Sprintf("127.0.0.1:%d", port),
		grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)),
	)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := pb.NewGNMIClient(conn).Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if err := stream.Send(recordsSubscribeReq()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	_, err = stream.Recv()
	if err == nil {
		t.Fatal("expected Unauthenticated without credentials")
	}
	st, ok := status.FromError(err)
	if !ok {
		// Some stacks wrap the status; accept message match.
		if !strings.Contains(strings.ToLower(err.Error()), "unauthenticated") {
			t.Fatalf("want Unauthenticated, got %v", err)
		}
		return
	}
	if st.Code() != codes.Unauthenticated {
		t.Fatalf("want Unauthenticated, got %v (%v)", st.Code(), err)
	}
}
