package rpcauthz

import "testing"

func TestCheckTargetAccess(t *testing.T) {
	tests := []struct {
		name        string
		roles       []string
		target      string
		write       bool
		requireRole bool
		wantError   bool
	}{
		{name: "legacy unknown read remains allowed", roles: []string{"sonic_linux"}},
		{name: "central unknown read denies", roles: []string{"sonic_linux"}, requireRole: true, wantError: true},
		{name: "unknown write denies", roles: []string{"sonic_linux"}, write: true, wantError: true},
		{name: "readonly read allows", roles: []string{"gnmi_readonly"}, requireRole: true},
		{name: "readonly write denies", roles: []string{"gnmi_readonly"}, write: true, requireRole: true, wantError: true},
		{name: "readwrite write allows", roles: []string{"gnmi_readwrite"}, write: true, requireRole: true},
		{name: "target noaccess denies", roles: []string{"gnmi_config_db_readwrite", "gnmi_config_db_noaccess"}, target: "gnmi_CONFIG_DB", write: true, requireRole: true, wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target := test.target
			if target == "" {
				target = "gnmi"
			}
			err := CheckTargetAccess("test-user", test.roles, target, test.write, test.requireRole)
			if got := err != nil; got != test.wantError {
				t.Fatalf("error = %v, wantError %t", err, test.wantError)
			}
		})
	}
}

func TestCheckTargetAccessUsesExactTargetRoles(t *testing.T) {
	err := CheckTargetAccess(
		"test-user",
		[]string{"gnmi_noaccess", "gnmi_config_db_readwrite"},
		"gnmi_CONFIG_DB",
		true,
		false,
	)
	if err != nil {
		t.Fatalf("CheckTargetAccess() error = %v, want nil", err)
	}
}

func TestCheckAuthorizationTargetAccessUsesServiceLevelDenial(t *testing.T) {
	err := CheckAuthorizationTargetAccess(
		"test-user",
		[]string{"gnmi_noaccess", "gnmi_config_db_readwrite"},
		"gnmi_CONFIG_DB",
		true,
		true,
	)
	if err == nil {
		t.Fatal("CheckAuthorizationTargetAccess() error = nil, want denial")
	}
}

func TestCanAccessService(t *testing.T) {
	tests := []struct {
		name      string
		roles     []string
		write     bool
		wantAllow bool
	}{
		{name: "generic readonly", roles: []string{"gnmi_readonly"}, wantAllow: true},
		{name: "scoped readwrite", roles: []string{"gnmi_config_db_readwrite"}, write: true, wantAllow: true},
		{name: "service noaccess wins", roles: []string{"gnmi_noaccess", "gnmi_config_db_readwrite"}, wantAllow: false},
		{name: "same target noaccess wins", roles: []string{"gnmi_config_db_noaccess", "gnmi_config_db_readwrite"}, wantAllow: false},
		{name: "one denied target does not suppress another", roles: []string{"gnmi_config_db_noaccess", "gnmi_state_db_readonly"}, wantAllow: true},
		{name: "malformed empty scope denies", roles: []string{"gnmi__readonly"}, wantAllow: false},
		{name: "cross service denies", roles: []string{"gnoi_readwrite"}, wantAllow: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := CanAccessService(test.roles, "gnmi", test.write); got != test.wantAllow {
				t.Fatalf("CanAccessService() = %t, want %t", got, test.wantAllow)
			}
		})
	}
}

func TestNormalizeTarget(t *testing.T) {
	if got := NormalizeTarget("  gnmi_CONFIG_DB "); got != "gnmi_config_db" {
		t.Fatalf("NormalizeTarget() = %q, want %q", got, "gnmi_config_db")
	}
}
