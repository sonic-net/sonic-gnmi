package common_utils

import "testing"

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
