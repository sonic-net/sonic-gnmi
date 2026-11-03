// Package authentication provides request-local authentication orchestration
// and role checks without credential backend or SONiC dependencies.
package authentication

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AuthInfo holds data about the authenticated user.
type AuthInfo struct {
	User        string
	AuthEnabled bool
	Roles       []string
}

// Method validates credentials and populates the caller-owned identity. It may
// return a derived context, which is passed to the next method and the caller.
type Method func(context.Context) (context.Context, error)

// Authenticate tries the supplied methods in order, then checks the successful
// identity's roles. The caller supplies enabled methods and owns transport bypasses.
func Authenticate(ctx context.Context, auth *AuthInfo, target string, writeAccess bool, methods ...Method) (context.Context, error) {
	auth.AuthEnabled = true
	for _, method := range methods {
		auth.User = ""
		auth.Roles = nil
		var err error
		ctx, err = method(ctx)
		if err != nil {
			continue
		}
		if err := CheckRoleAccess(auth, target, writeAccess, false); err != nil {
			return ctx, status.Error(codes.PermissionDenied, err.Error())
		}
		return ctx, nil
	}
	return ctx, status.Error(codes.Unauthenticated, "Unauthenticated")
}

// CheckRoleAccess enforces target-prefixed roles. Denials are ordinary errors;
// the RPC caller decides how to map them to a transport status. requireRole
// distinguishes strict read access from the legacy authenticated-read fallback.
func CheckRoleAccess(auth *AuthInfo, target string, writeAccess, requireRole bool) error {
	access := resolveTargetRoleAccess(auth, target)
	if access.noAccessRole != "" {
		return fmt.Errorf("%s does not have access, target %s, role %s", auth.User, target, access.noAccessRole)
	}
	if writeAccess && !access.readWrite {
		if access.readOnlyRole != "" {
			return fmt.Errorf("%s does not have access, target %s, role %s", auth.User, target, access.readOnlyRole)
		}
		return fmt.Errorf("%s does not have write access, target %s", auth.User, target)
	}
	if requireRole && access.readOnlyRole == "" && !access.readWrite {
		return fmt.Errorf("%s does not have read access, target %s", auth.User, target)
	}
	return nil
}

type targetRoleAccess struct {
	noAccessRole string
	readOnlyRole string
	readWrite    bool
}

func resolveTargetRoleAccess(auth *AuthInfo, target string) targetRoleAccess {
	var access targetRoleAccess
	if auth == nil {
		return access
	}
	prefix := strings.ToLower(target) + "_"
	for _, role := range auth.Roles {
		role = strings.TrimSpace(role)
		switch role {
		case prefix + "noaccess":
			access.noAccessRole = role
		case prefix + "readonly":
			access.readOnlyRole = role
		case prefix + "readwrite":
			access.readWrite = true
		}
	}
	return access
}
