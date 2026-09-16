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
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/sonic-net/sonic-gnmi/common_utils"
	spb_jwt "github.com/sonic-net/sonic-gnmi/proto/gnoi/jwt"
	"golang.org/x/net/context"
	"google.golang.org/grpc/metadata"
)

func newPasswordAuthCtx() context.Context {
	md := metadata.New(map[string]string{
		"username": "testuser",
		"password": "testpass",
	})
	return metadata.NewIncomingContext(context.Background(), md)
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
		ConfigTableName: "GNMI_CLIENT_CERT",
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
		ConfigTableName: "GNMI_CLIENT_CERT",
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
