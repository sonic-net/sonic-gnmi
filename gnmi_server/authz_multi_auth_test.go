package gnmi

// Regression tests for role-based authorization applied uniformly across
// authentication mechanisms (password, JWT), added alongside the fix that
// moved the readonly/readwrite/noaccess role check out of the cert-only
// branch of authenticate() into the shared checkRoleAccess() helper.
//
// TestAuthenticate (in server_test.go) covers certificate auth with a named
// config table. These tests exercise the same noaccess/readonly/readwrite
// matrix for password, JWT, and the empty-table certificate path.

import (
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
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
	mockPasswordAuth := gomonkey.ApplyFunc(BasicAuthenAndAuthor, func(ctx context.Context) (context.Context, error) {
		passwordCalls++
		rc, ctx := common_utils.GetContext(ctx)
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
	certStartedClean := false
	mockCertMapping := gomonkey.ApplyFunc(PopulateAuthStructByCommonName, func(certCommonName string, auth *common_utils.AuthInfo, serviceConfigTableName string) error {
		certCalls++
		certStartedClean = auth.User == "" && len(auth.Roles) == 0
		if !certStartedClean {
			return nil
		}
		return status.Error(codes.Unauthenticated, "unmapped certificate")
	})
	defer mockCertMapping.Reset()

	cfg := &Config{
		ConfigTableName: "GNMI_CLIENT_CERT",
		UserAuth:        AuthTypes{"password": true, "cert": true, "jwt": true},
	}

	if _, err := authenticate(cfg, newVerifiedClientCertCtx("unmapped-cert"), "gnmi", true); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("mixed auth with an unmapped certificate returned %v, want Unauthenticated", err)
	}
	if passwordCalls != 1 || jwtCalls != 1 || certCalls != 1 {
		t.Fatalf("authentication calls = password %d, JWT %d, cert %d; want 1 each", passwordCalls, jwtCalls, certCalls)
	}
	if !jwtStartedClean {
		t.Error("JWT authentication inherited identity state from the rejected password attempt")
	}
	if !certStartedClean {
		t.Error("certificate authentication inherited identity state from a rejected fallback attempt")
	}
}

func TestCheckRoleAccessTargets(t *testing.T) {
	tests := []struct {
		name        string
		roles       []string
		target      string
		writeAccess bool
		wantError   bool
	}{
		{
			name:        "gNOI readonly denies write",
			roles:       []string{"gnoi_readonly"},
			target:      "gnoi",
			writeAccess: true,
			wantError:   true,
		},
		{
			name:        "CONFIG_DB readwrite allows write",
			roles:       []string{"gnmi_config_db_readwrite"},
			target:      "gnmi_config_db",
			writeAccess: true,
		},
		{
			name:        "CONFIG_DB noaccess denies read",
			roles:       []string{"gnmi_config_db_noaccess"},
			target:      "gnmi_config_db",
			writeAccess: false,
			wantError:   true,
		},
		{
			name:        "missing target role denies write",
			roles:       []string{"sonic_linux"},
			target:      "gnmi_config_db",
			writeAccess: true,
			wantError:   true,
		},
		{
			name:        "missing target role preserves read",
			roles:       []string{"sonic_linux"},
			target:      "gnmi_config_db",
			writeAccess: false,
		},
		{
			name:        "noaccess after readwrite denies write",
			roles:       []string{"gnmi_readwrite", "gnmi_noaccess"},
			target:      "gnmi",
			writeAccess: true,
			wantError:   true,
		},
		{
			name:        "noaccess before readwrite denies write",
			roles:       []string{"gnmi_noaccess", "gnmi_readwrite"},
			target:      "gnmi",
			writeAccess: true,
			wantError:   true,
		},
		{
			name:        "noaccess after readwrite denies read",
			roles:       []string{"gnmi_readwrite", "gnmi_noaccess"},
			target:      "gnmi",
			writeAccess: false,
			wantError:   true,
		},
		{
			name:        "noaccess before readwrite denies read",
			roles:       []string{"gnmi_noaccess", "gnmi_readwrite"},
			target:      "gnmi",
			writeAccess: false,
			wantError:   true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			auth := &common_utils.AuthInfo{User: "testuser", Roles: test.roles}
			err := checkRoleAccess(auth, test.target, test.writeAccess)
			if (err != nil) != test.wantError {
				t.Fatalf("checkRoleAccess() error = %v, wantError %t", err, test.wantError)
			}
		})
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

func TestAuthenticateCertificateModeWithoutIdentity(t *testing.T) {
	cfg := &Config{
		ConfigTableName: "GNMI_CLIENT_CERT",
		UserAuth:        AuthTypes{"password": false, "cert": true, "jwt": false},
	}

	_, err := authenticate(cfg, context.Background(), "gnmi", false)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("certificate mode without a verified client identity returned %v, want Unauthenticated", err)
	}
}
