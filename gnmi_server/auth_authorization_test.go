package gnmi

import (
	"context"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/sonic-net/sonic-gnmi/common_utils"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestAuthenticateAppliesAuthorizationAfterJWT(t *testing.T) {
	config := &Config{UserAuth: AuthTypes{"jwt": true}}
	tests := []struct {
		name        string
		roles       []string
		writeAccess bool
		wantCode    codes.Code
	}{
		{name: "read compatibility", roles: []string{"sonic_linux"}, wantCode: codes.OK},
		{name: "explicit noaccess", roles: []string{"gnoi_noaccess"}, wantCode: codes.PermissionDenied},
		{name: "readonly write", roles: []string{"gnoi_readonly"}, writeAccess: true, wantCode: codes.PermissionDenied},
		{name: "readwrite write", roles: []string{"gnoi_readwrite"}, writeAccess: true, wantCode: codes.OK},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			token := generateJWT("jwt-user", test.roles, time.Now().Add(time.Minute))
			ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("access_token", token))
			_, err := authenticate(config, ctx, "gnoi", test.writeAccess)
			if got := status.Code(err); got != test.wantCode {
				t.Fatalf("authenticate() code = %v, want %v; err=%v", got, test.wantCode, err)
			}
		})
	}
}

func TestAuthenticateAppliesAuthorizationAfterPassword(t *testing.T) {
	roles := []string{"gnoi_readonly"}
	patches := gomonkey.NewPatches()
	defer patches.Reset()
	patches.ApplyFunc(PopulateAuthStruct, func(username string, auth *common_utils.AuthInfo, _ []string) error {
		auth.User = username
		auth.Roles = append([]string(nil), roles...)
		return nil
	})
	patches.ApplyFunc(UserPwAuth, func(string, string) (bool, error) {
		return true, nil
	})

	config := &Config{UserAuth: AuthTypes{"password": true}}
	newContext := func() context.Context {
		return metadata.NewIncomingContext(context.Background(), metadata.Pairs(
			"username", "password-user",
			"password", "password",
		))
	}

	if _, err := authenticate(config, newContext(), "gnoi", true); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("readonly password user write error = %v, want PermissionDenied", err)
	}
	roles = []string{"gnoi_readwrite"}
	if _, err := authenticate(config, newContext(), "gnoi", true); err != nil {
		t.Fatalf("readwrite password user write failed: %v", err)
	}
}
