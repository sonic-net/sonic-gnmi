package gnmi

// Regression tests for role-based authorization applied uniformly across
// authentication mechanisms (password, JWT), added alongside the fix that
// moved the readonly/readwrite/noaccess role check out of the cert-only
// branch of authenticate() into the shared checkRoleAccess() helper.
//
// TestAuthenticate (in server_test.go) already covers the cert auth path.
// These tests exercise the same noaccess/readonly/readwrite matrix for the
// password and JWT paths, which previously bypassed the role check entirely.

import (
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/sonic-net/sonic-gnmi/common_utils"
	spb_jwt "github.com/sonic-net/sonic-gnmi/proto/gnoi/jwt"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

func TestAuthenticationDIContextAndShortCircuit(t *testing.T) {
	type contextKey struct{}
	for _, roles := range [][]string{{"gnmi_readwrite"}, {"gnmi_readonly"}} {
		t.Run(roles[0], func(t *testing.T) {
			ctx := context.Background()
			rc, ctx := common_utils.GetContext(ctx)
			bundle := "fixture-version"
			rc.BundleVersion = &bundle
			id := rc.ID
			rc.Auth.User, rc.Auth.Roles = "stale", []string{"gnmi_readwrite"}
			var calls []string
			patch := gomonkey.ApplyFunc(BasicAuthenAndAuthor, func(ctx context.Context) (context.Context, error) {
				calls = append(calls, "password")
				got, _ := common_utils.GetContext(ctx)
				if got != rc || got.Auth.User != "" || len(got.Auth.Roles) != 0 {
					t.Fatal("password received stale identity or a different request context")
				}
				got.Auth.User, got.Auth.Roles = "failed", []string{"gnmi_readwrite"}
				return context.WithValue(ctx, contextKey{}, "derived"), errors.New("rejected")
			})
			defer patch.Reset()
			patch.ApplyFunc(JwtAuthenAndAuthor, func(ctx context.Context) (*spb_jwt.JwtToken, context.Context, error) {
				calls = append(calls, "jwt")
				got, _ := common_utils.GetContext(ctx)
				if ctx.Value(contextKey{}) != "derived" || got != rc || got.Auth.User != "" || len(got.Auth.Roles) != 0 {
					t.Fatal("JWT lost derived context or inherited failed identity")
				}
				got.Auth.User, got.Auth.Roles = "accepted", roles
				return nil, ctx, nil
			})
			patch.ApplyFunc(ClientCertAuthenAndAuthor, func(ctx context.Context, _ string, _ bool) (context.Context, error) {
				t.Fatal("certificate fallback called after successful JWT authentication")
				return ctx, nil
			})
			cfg := &Config{UserAuth: AuthTypes{"password": true, "jwt": true, "cert": true}}
			gotCtx, err := authenticate(cfg, ctx, "gnmi", true)
			wantCode := codes.OK
			if roles[0] == "gnmi_readonly" {
				wantCode = codes.PermissionDenied
			}
			if status.Code(err) != wantCode || !reflect.DeepEqual(calls, []string{"password", "jwt"}) {
				t.Fatalf("authenticate: calls=%v err=%v, want %v", calls, err, wantCode)
			}
			got, _ := common_utils.GetContext(gotCtx)
			if got != rc || got.ID != id || got.BundleVersion != &bundle || gotCtx.Value(contextKey{}) != "derived" || got.Auth.User != "accepted" {
				t.Fatalf("request context changed: %+v", got)
			}
		})
	}
}

func TestAuthenticationDIConfigAndFailure(t *testing.T) {
	ctx := context.Background()
	rc, ctx := common_utils.GetContext(ctx)
	calls := 0
	patch := gomonkey.ApplyFunc(BasicAuthenAndAuthor, func(ctx context.Context) (context.Context, error) {
		calls++
		rc.Auth.User, rc.Auth.Roles = "last-failed", []string{"gnmi_readwrite"}
		return ctx, errors.New("credential rejected")
	})
	defer patch.Reset()
	cfg := &Config{UserAuth: AuthTypes{"password": true}}
	if _, err := authenticate(cfg, ctx, "gnmi", false); status.Code(err) != codes.Unauthenticated || status.Convert(err).Message() != "Unauthenticated" {
		t.Fatalf("failure result = %v", err)
	}
	// Retain the existing final-failure state; only the next attempt clears it.
	if rc.Auth.User != "last-failed" || !rc.Auth.AuthEnabled {
		t.Fatalf("unexpected final-failure identity: %+v", rc.Auth)
	}
	cfg.UserAuth["none"] = true
	if _, err := authenticate(cfg, ctx, "gnmi", true); err != nil || rc.Auth.AuthEnabled || calls != 1 {
		t.Fatalf("updated config not honored: err=%v calls=%d auth=%+v", err, calls, rc.Auth)
	}
}

func TestAuthenticationDIAuthInfoCompatibility(t *testing.T) {
	auth := common_utils.AuthInfo{User: "fixture", AuthEnabled: true, Roles: []string{"gnmi_readonly"}}
	data, err := json.Marshal(auth)
	if err != nil || string(data) != `{"User":"fixture","AuthEnabled":true,"Roles":["gnmi_readonly"]}` {
		t.Fatalf("AuthInfo serialization = %s, err=%v", data, err)
	}
	var decoded common_utils.AuthInfo
	if err := json.Unmarshal(data, &decoded); err != nil || !reflect.DeepEqual(auth, decoded) {
		t.Fatalf("AuthInfo roundtrip = %+v, err=%v", decoded, err)
	}
	err = checkRoleAccess(&auth, "GNMI", true)
	if _, ok := status.FromError(err); ok || err == nil || err.Error() != "fixture does not have access, target GNMI, role gnmi_readonly" {
		t.Fatalf("role helper error contract changed: %v", err)
	}
}

func newPasswordAuthCtx() context.Context {
	md := metadata.New(map[string]string{
		"username": "testuser",
		"password": "testpass",
	})
	return metadata.NewIncomingContext(context.Background(), md)
}

func newVerifiedClientCertCtx(commonName string) context.Context {
	cert := &x509.Certificate{Subject: pkix.Name{CommonName: commonName}}
	return peer.NewContext(context.Background(), &peer.Peer{
		AuthInfo: credentials.TLSInfo{
			State: tls.ConnectionState{
				VerifiedChains: [][]*x509.Certificate{{cert}},
			},
		},
	})
}

func TestAuthenticateRoleAccessPassword(t *testing.T) {
	var testRoles []string

	// PopulateAuthStruct normally resolves roles via the local user/group
	// database (or JWT claims); mock it to return the roles under test.
	mockPopulate := gomonkey.ApplyFunc(PopulateAuthStruct, func(username string, auth *common_utils.AuthInfo, r []string) error {
		auth.User = username
		auth.Roles = testRoles
		return nil
	})
	defer mockPopulate.Reset()

	// UserPwAuth normally authenticates over SSH; mock it to always succeed
	// so the test isolates the role-check behavior.
	mockUserPwAuth := gomonkey.ApplyFunc(UserPwAuth, func(username string, passwd string) (bool, error) {
		return true, nil
	})
	defer mockUserPwAuth.Reset()

	cfg := &Config{
		ConfigTableName: "",
		UserAuth:        AuthTypes{"password": true, "cert": false, "jwt": false},
	}

	testRoles = []string{"gnmi_noaccess"}
	if _, err := authenticate(cfg, newPasswordAuthCtx(), "gnmi", true); err == nil {
		t.Errorf("password auth with noaccess role should fail write access")
	}
	if _, err := authenticate(cfg, newPasswordAuthCtx(), "gnmi", false); err == nil {
		t.Errorf("password auth with noaccess role should fail read access")
	}

	testRoles = []string{"gnmi_readonly"}
	if _, err := authenticate(cfg, newPasswordAuthCtx(), "gnmi", true); err == nil {
		t.Errorf("password auth with readonly role should fail write access")
	}
	if _, err := authenticate(cfg, newPasswordAuthCtx(), "gnmi", false); err != nil {
		t.Errorf("password auth with readonly role should pass read access: %v", err)
	}

	testRoles = []string{"gnmi_readwrite"}
	if _, err := authenticate(cfg, newPasswordAuthCtx(), "gnmi", true); err != nil {
		t.Errorf("password auth with readwrite role should pass write access: %v", err)
	}
	if _, err := authenticate(cfg, newPasswordAuthCtx(), "gnmi", false); err != nil {
		t.Errorf("password auth with readwrite role should pass read access: %v", err)
	}
}

func TestAuthenticateRoleAccessJwt(t *testing.T) {
	var testRoles []string

	// JwtAuthenAndAuthor normally parses and validates a signed JWT; mock it
	// to populate the auth context with the roles under test, isolating the
	// role-check behavior from token generation/validation.
	mockJwtAuth := gomonkey.ApplyFunc(JwtAuthenAndAuthor, func(ctx context.Context) (*spb_jwt.JwtToken, context.Context, error) {
		rc, ctx := common_utils.GetContext(ctx)
		rc.Auth.User = "jwtuser"
		rc.Auth.Roles = testRoles
		return nil, ctx, nil
	})
	defer mockJwtAuth.Reset()

	cfg := &Config{
		ConfigTableName: "",
		UserAuth:        AuthTypes{"password": false, "cert": false, "jwt": true},
	}

	testRoles = []string{"gnmi_noaccess"}
	if _, err := authenticate(cfg, context.Background(), "gnmi", true); err == nil {
		t.Errorf("jwt auth with noaccess role should fail write access")
	}
	if _, err := authenticate(cfg, context.Background(), "gnmi", false); err == nil {
		t.Errorf("jwt auth with noaccess role should fail read access")
	}

	testRoles = []string{"gnmi_readonly"}
	if _, err := authenticate(cfg, context.Background(), "gnmi", true); err == nil {
		t.Errorf("jwt auth with readonly role should fail write access")
	}
	if _, err := authenticate(cfg, context.Background(), "gnmi", false); err != nil {
		t.Errorf("jwt auth with readonly role should pass read access: %v", err)
	}

	testRoles = []string{"gnmi_readwrite"}
	if _, err := authenticate(cfg, context.Background(), "gnmi", true); err != nil {
		t.Errorf("jwt auth with readwrite role should pass write access: %v", err)
	}
	if _, err := authenticate(cfg, context.Background(), "gnmi", false); err != nil {
		t.Errorf("jwt auth with readwrite role should pass read access: %v", err)
	}
}

func TestAuthenticateRoleAccessMixedAuth(t *testing.T) {
	passwordCalls := 0
	passwordStartedClean := false
	mockPasswordAuth := gomonkey.ApplyFunc(BasicAuthenAndAuthor, func(ctx context.Context) (context.Context, error) {
		passwordCalls++
		rc, ctx := common_utils.GetContext(ctx)
		passwordStartedClean = rc.Auth.User == "" && len(rc.Auth.Roles) == 0
		rc.Auth.User = "passworduser"
		rc.Auth.Roles = []string{"gnmi_readwrite"}
		return ctx, errors.New("password rejected")
	})
	defer mockPasswordAuth.Reset()

	jwtCalls := 0
	jwtStartedClean := false
	mockJwtAuth := gomonkey.ApplyFunc(JwtAuthenAndAuthor, func(ctx context.Context) (*spb_jwt.JwtToken, context.Context, error) {
		jwtCalls++
		rc, ctx := common_utils.GetContext(ctx)
		jwtStartedClean = rc.Auth.User == "" && len(rc.Auth.Roles) == 0
		rc.Auth.User = "jwtuser"
		rc.Auth.Roles = []string{"gnmi_readwrite"}
		return nil, ctx, errors.New("JWT rejected")
	})
	defer mockJwtAuth.Reset()

	certCalls := 0
	certStartedFresh := false
	mockCertMapping := gomonkey.ApplyFunc(PopulateAuthStructByCommonName, func(certCommonName string, auth *common_utils.AuthInfo, serviceConfigTableName string) error {
		certCalls++
		certStartedFresh = auth.User == certCommonName && len(auth.Roles) == 0
		return status.Error(codes.Unauthenticated, "unmapped certificate")
	})
	defer mockCertMapping.Reset()

	cfg := &Config{
		ConfigTableName: "GNMI_CLIENT_CERT",
		UserAuth:        AuthTypes{"password": true, "cert": true, "jwt": true},
	}

	ctx := newVerifiedClientCertCtx("unmapped-cert")
	rc, ctx := common_utils.GetContext(ctx)
	rc.Auth.User = "preexisting-user"
	rc.Auth.Roles = []string{"gnmi_readwrite"}

	if _, err := authenticate(cfg, ctx, "gnmi", true); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("mixed auth with an unmapped certificate returned %v, want Unauthenticated", err)
	}
	if passwordCalls != 1 || jwtCalls != 1 || certCalls != 1 {
		t.Fatalf("authentication calls = password %d, JWT %d, cert %d; want 1 each", passwordCalls, jwtCalls, certCalls)
	}
	if !passwordStartedClean {
		t.Error("password authentication inherited identity state from the incoming context")
	}
	if !jwtStartedClean {
		t.Error("JWT authentication inherited identity state from the rejected password attempt")
	}
	if !certStartedFresh {
		t.Error("certificate authentication inherited stale identity state from a rejected fallback attempt")
	}
}

func TestAuthenticateRoleAccessCertificateEmptyConfigTable(t *testing.T) {
	var testRoles []string
	mockPopulate := gomonkey.ApplyFunc(PopulateAuthStruct, func(username string, auth *common_utils.AuthInfo, r []string) error {
		auth.User = username
		auth.Roles = testRoles
		return nil
	})
	defer mockPopulate.Reset()

	cfg := &Config{
		ConfigTableName: "",
		UserAuth:        AuthTypes{"password": false, "cert": true, "jwt": false},
	}

	tests := []struct {
		name        string
		roles       []string
		writeAccess bool
		wantError   bool
	}{
		{
			name:        "noaccess denies write",
			roles:       []string{"gnmi_noaccess"},
			writeAccess: true,
			wantError:   true,
		},
		{
			name:      "noaccess denies read",
			roles:     []string{"gnmi_noaccess"},
			wantError: true,
		},
		{
			name:        "readonly denies write",
			roles:       []string{"gnmi_readonly"},
			writeAccess: true,
			wantError:   true,
		},
		{
			name:  "readonly allows read",
			roles: []string{"gnmi_readonly"},
		},
		{
			name:        "readwrite allows write",
			roles:       []string{"gnmi_readwrite"},
			writeAccess: true,
		},
		{
			name:  "readwrite allows read",
			roles: []string{"gnmi_readwrite"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			testRoles = test.roles
			_, err := authenticate(cfg, newVerifiedClientCertCtx("certuser"), "gnmi", test.writeAccess)
			if (err != nil) != test.wantError {
				t.Fatalf("authenticate() error = %v, wantError %t", err, test.wantError)
			}
		})
	}
}
