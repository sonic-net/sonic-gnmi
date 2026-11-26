package authentication

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestAuthenticateFallback(t *testing.T) {
	type contextKey struct{}
	auth := AuthInfo{User: "stale", Roles: []string{"gnmi_readwrite"}}
	var calls []string
	first := func(ctx context.Context) (context.Context, error) {
		calls = append(calls, "first")
		if auth.User != "" || len(auth.Roles) != 0 || !auth.AuthEnabled {
			t.Fatalf("initial identity not cleared: %+v", auth)
		}
		auth.User, auth.Roles = "failed", []string{"gnmi_readwrite"}
		return context.WithValue(ctx, contextKey{}, "preserved"), errors.New("rejected")
	}
	second := func(ctx context.Context) (context.Context, error) {
		calls = append(calls, "second")
		if auth.User != "" || len(auth.Roles) != 0 || ctx.Value(contextKey{}) != "preserved" {
			t.Fatalf("fallback identity/context mismatch: %+v", auth)
		}
		auth.User, auth.Roles = "accepted", []string{"gnmi_readwrite"}
		return ctx, nil
	}
	third := func(ctx context.Context) (context.Context, error) {
		t.Fatal("fallback called after success")
		return ctx, nil
	}
	ctx, err := Authenticate(context.Background(), &auth, "gnmi", true, first, second, third)
	if err != nil || ctx.Value(contextKey{}) != "preserved" || auth.User != "accepted" || !reflect.DeepEqual(calls, []string{"first", "second"}) {
		t.Fatalf("result: auth=%+v calls=%v err=%v", auth, calls, err)
	}
}

func TestAuthenticateRoleDenialStopsFallback(t *testing.T) {
	auth := AuthInfo{}
	first := func(ctx context.Context) (context.Context, error) {
		auth.User, auth.Roles = "reader", []string{"gnmi_readonly"}
		return ctx, nil
	}
	second := func(ctx context.Context) (context.Context, error) {
		t.Fatal("role denial must not select another identity")
		return ctx, nil
	}
	_, err := Authenticate(context.Background(), &auth, "gnmi", true, first, second)
	if status.Code(err) != codes.PermissionDenied || status.Convert(err).Message() != "reader does not have access, target gnmi, role gnmi_readonly" {
		t.Fatalf("denial = %v", err)
	}
}

func TestAuthenticateFailure(t *testing.T) {
	for _, withMethod := range []bool{false, true} {
		t.Run(map[bool]string{false: "no methods", true: "last method fails"}[withMethod], func(t *testing.T) {
			auth := AuthInfo{User: "existing"}
			var methods []Method
			if withMethod {
				methods = append(methods, func(ctx context.Context) (context.Context, error) {
					auth.User = "failed"
					return ctx, errors.New("private credential failure")
				})
			}
			_, err := Authenticate(context.Background(), &auth, "gnmi", false, methods...)
			if status.Code(err) != codes.Unauthenticated || status.Convert(err).Message() != "Unauthenticated" {
				t.Fatalf("failure = %v", err)
			}
			wantUser := "existing"
			if withMethod {
				wantUser = "failed"
			}
			if auth.User != wantUser || !auth.AuthEnabled {
				t.Fatalf("final-failure state = %+v", auth)
			}
		})
	}
}

func TestAuthenticateIndependentRequests(t *testing.T) {
	for _, user := range []string{"alice", "bob"} {
		t.Run(user, func(t *testing.T) {
			t.Parallel()
			auth := AuthInfo{}
			method := func(ctx context.Context) (context.Context, error) {
				auth.User, auth.Roles = user, []string{"gnmi_readwrite"}
				return ctx, nil
			}
			for i := 0; i < 10; i++ {
				if _, err := Authenticate(context.Background(), &auth, "gnmi", true, method); err != nil || auth.User != user {
					t.Fatalf("identity=%+v err=%v", auth, err)
				}
			}
		})
	}
}

func TestCheckRoleAccess(t *testing.T) {
	tests := []struct {
		name     string
		roles    []string
		write    bool
		required bool
		message  string
	}{
		{name: "legacy read without role"},
		{name: "write without role", write: true, message: "fixture does not have write access, target GNMI"},
		{name: "required read without role", required: true, message: "fixture does not have read access, target GNMI"},
		{name: "readonly read", roles: []string{"gnmi_readonly"}},
		{name: "readonly required read", roles: []string{"gnmi_readonly"}, required: true},
		{name: "readonly write", roles: []string{"gnmi_readonly"}, write: true, message: "fixture does not have access, target GNMI, role gnmi_readonly"},
		{name: "readwrite", roles: []string{"gnmi_readwrite"}, write: true},
		{name: "readwrite after readonly", roles: []string{"gnmi_readonly", "gnmi_readwrite"}, write: true},
		{name: "readwrite before readonly", roles: []string{"gnmi_readwrite", "gnmi_readonly"}, write: true},
		{name: "noaccess read", roles: []string{"gnmi_noaccess"}, message: "fixture does not have access, target GNMI, role gnmi_noaccess"},
		{name: "noaccess before readwrite", roles: []string{"gnmi_noaccess", "gnmi_readwrite"}, write: true, message: "fixture does not have access, target GNMI, role gnmi_noaccess"},
		{name: "noaccess after readwrite", roles: []string{"gnmi_readwrite", "gnmi_noaccess"}, message: "fixture does not have access, target GNMI, role gnmi_noaccess"},
		{name: "trim whitespace", roles: []string{"  gnmi_readwrite\t"}, write: true},
		{name: "exact target", roles: []string{"gnmi_config_db_readwrite"}, write: true, message: "fixture does not have write access, target GNMI"},
		{name: "role is case sensitive", roles: []string{"GNMI_readwrite"}, required: true, message: "fixture does not have read access, target GNMI"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckRoleAccess(&AuthInfo{User: "fixture", Roles: tt.roles}, "GNMI", tt.write, tt.required)
			if tt.message == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || err.Error() != tt.message {
				t.Fatalf("error=%v, want %q", err, tt.message)
			}
			if _, ok := status.FromError(err); ok {
				t.Fatal("role helper must return an ordinary error")
			}
		})
	}
}
