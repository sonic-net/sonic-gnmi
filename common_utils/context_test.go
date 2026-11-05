package common_utils

import (
	"context"
	"testing"
)

func TestWithFreshAuth(t *testing.T) {
	type key struct{}
	ctx := context.WithValue(context.Background(), key{}, "preserved")
	original, ctx := GetContext(ctx)
	version := "version"
	original.BundleVersion = &version
	original.Auth = AuthInfo{User: "initial", Roles: []string{"gnmi_readonly"}, AuthEnabled: true}
	freshCtx := WithFreshAuth(ctx)
	fresh, _ := GetContext(freshCtx)
	if fresh == original || fresh.ID != original.ID || fresh.BundleVersion != original.BundleVersion || freshCtx.Value(key{}) != "preserved" {
		t.Fatal("request metadata not preserved in independent context")
	}
	if fresh.Auth.User != "" || len(fresh.Auth.Roles) != 0 || fresh.Auth.AuthEnabled {
		t.Fatalf("fresh identity inherited state: %+v", fresh.Auth)
	}
	fresh.Auth.User, fresh.Auth.Roles = "refreshed", []string{"gnmi_noaccess"}
	if original.Auth.User != "initial" || original.Auth.Roles[0] != "gnmi_readonly" {
		t.Fatal("refresh changed original identity")
	}
}

func TestRetiredCounterSlotsRemainStable(t *testing.T) {
	tests := []struct {
		counter CounterType
		ordinal int
		name    string
	}{
		{DBUS_FILE_STAT, 23, "DBUS file stat"},
		{DBUS_FILE_DOWNLOAD, 24, "DBUS file download"},
		{DBUS_FILE_REMOVE, 25, "DBUS file remove"},
		{DBUS_IMAGE_DOWNLOAD, 26, "DBUS image download"},
		{DBUS_IMAGE_INSTALL, 27, "DBUS image install"},
		{DBUS_IMAGE_LIST, 28, "DBUS image list"},
		{DBUS_IMAGE_ACTIVATE, 29, "DBUS image activate"},
		{DBUS_DOCKER_LOAD, 30, "DBUS docker load"},
		{DBUS_CONFIG_REPLACE, 31, "DBUS config replace"},
	}

	for _, test := range tests {
		if got := int(test.counter); got != test.ordinal {
			t.Errorf("%s ordinal = %d, want %d", test.name, got, test.ordinal)
		}
		if got := test.counter.String(); got != test.name {
			t.Errorf("CounterType(%d).String() = %q, want %q", test.ordinal, got, test.name)
		}
	}

	if got, want := int(COUNTER_SIZE), 32; got != want {
		t.Errorf("COUNTER_SIZE = %d, want %d", got, want)
	}
}
