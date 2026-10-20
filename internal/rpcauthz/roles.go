package rpcauthz

import (
	"fmt"
	"strings"
)

const (
	writeAccessMode = "readwrite"
	readOnlyMode    = "readonly"
	noAccessMode    = "noaccess"
)

type targetRoleAccess struct {
	noAccessRole string
	readOnlyRole string
	readWrite    bool
}

// CheckTargetAccess evaluates roles for the exact target, preserving legacy
// handler authorization semantics.
func CheckTargetAccess(
	user string,
	roles []string,
	target string,
	writeAccess bool,
	requireRole bool,
) error {
	return checkTargetAccess(
		user,
		target,
		writeAccess,
		requireRole,
		resolveTargetRoleAccess(roles, target),
	)
}

// CheckAuthorizationTargetAccess evaluates roles for central authorization,
// where a service-level noaccess role also denies scoped service targets.
func CheckAuthorizationTargetAccess(
	user string,
	roles []string,
	target string,
	writeAccess bool,
	requireRole bool,
) error {
	return checkTargetAccess(
		user,
		target,
		writeAccess,
		requireRole,
		resolveAuthorizationTargetRoleAccess(roles, target),
	)
}

func checkTargetAccess(
	user string,
	target string,
	writeAccess bool,
	requireRole bool,
	access targetRoleAccess,
) error {
	if access.noAccessRole != "" {
		return fmt.Errorf("%s does not have access, target %s, role %s", user, target, access.noAccessRole)
	}
	if writeAccess && !access.readWrite {
		if access.readOnlyRole != "" {
			return fmt.Errorf("%s does not have access, target %s, role %s", user, target, access.readOnlyRole)
		}
		return fmt.Errorf("%s does not have write access, target %s", user, target)
	}
	if requireRole && access.readOnlyRole == "" && !access.readWrite {
		return fmt.Errorf("%s does not have read access, target %s", user, target)
	}
	return nil
}

// CanAccessService reports whether any valid role can authorize a target in the
// selected service without allowing one scoped denial to suppress another
// target's independent allow.
func CanAccessService(roles []string, serviceTarget string, writeAccess bool) bool {
	serviceTarget = NormalizeTarget(serviceTarget)
	if resolveTargetRoleAccess(roles, serviceTarget).noAccessRole != "" {
		return false
	}

	accessByTarget := make(map[string]targetRoleAccess)
	for _, role := range roles {
		target, mode, ok := splitRole(role)
		if !ok ||
			!validRoleTarget(target, serviceTarget) ||
			serviceTargetFor(target) != serviceTarget {
			continue
		}
		access := accessByTarget[target]
		switch mode {
		case noAccessMode:
			access.noAccessRole = role
		case writeAccessMode:
			access.readWrite = true
		case readOnlyMode:
			access.readOnlyRole = role
		}
		accessByTarget[target] = access
	}

	for _, access := range accessByTarget {
		if access.noAccessRole != "" {
			continue
		}
		if access.readWrite || (!writeAccess && access.readOnlyRole != "") {
			return true
		}
	}
	return false
}

// NormalizeTarget returns the canonical target form used for authorization
// decisions carried through context.
func NormalizeTarget(target string) string {
	return strings.ToLower(strings.TrimSpace(target))
}

func resolveTargetRoleAccess(roles []string, target string) targetRoleAccess {
	var access targetRoleAccess
	prefix := strings.ToLower(target) + "_"
	for _, role := range roles {
		role = strings.TrimSpace(role)
		switch role {
		case prefix + noAccessMode:
			access.noAccessRole = role
		case prefix + readOnlyMode:
			access.readOnlyRole = role
		case prefix + writeAccessMode:
			access.readWrite = true
		}
	}
	return access
}

func serviceTargetFor(target string) string {
	target = NormalizeTarget(target)
	if separator := strings.IndexByte(target, '_'); separator >= 0 {
		return target[:separator]
	}
	return target
}

func resolveAuthorizationTargetRoleAccess(roles []string, target string) targetRoleAccess {
	access := resolveTargetRoleAccess(roles, target)
	serviceTarget := serviceTargetFor(target)
	if serviceTarget == NormalizeTarget(target) {
		return access
	}

	serviceAccess := resolveTargetRoleAccess(roles, serviceTarget)
	if serviceAccess.noAccessRole != "" {
		access.noAccessRole = serviceAccess.noAccessRole
	}
	return access
}

func validRoleTarget(target, serviceTarget string) bool {
	normalizedTarget := NormalizeTarget(target)
	if target != normalizedTarget {
		return false
	}
	target = normalizedTarget
	serviceTarget = NormalizeTarget(serviceTarget)
	if target == serviceTarget {
		return true
	}
	scope, ok := strings.CutPrefix(target, serviceTarget+"_")
	if !ok || scope == "" {
		return false
	}
	for _, segment := range strings.Split(scope, "_") {
		if segment == "" {
			return false
		}
	}
	return true
}

func splitRole(role string) (string, string, bool) {
	role = strings.TrimSpace(role)
	for _, mode := range []string{noAccessMode, readOnlyMode, writeAccessMode} {
		suffix := "_" + mode
		if strings.HasSuffix(role, suffix) && len(role) > len(suffix) {
			return strings.TrimSuffix(role, suffix), mode, true
		}
	}
	return "", "", false
}
