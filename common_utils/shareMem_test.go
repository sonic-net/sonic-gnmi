package common_utils

import "testing"

func TestSharedMemoryKey(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    int
		wantErr bool
	}{
		{name: "default", want: 7749},
		{name: "injected", value: "12345", want: 12345},
		{name: "malformed", value: "invalid", wantErr: true},
		{name: "zero", value: "0", wantErr: true},
		{name: "negative", value: "-1", wantErr: true},
		{name: "overflow", value: "2147483648", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("SONIC_GNMI_SHM_KEY", test.value)
			got, err := sharedMemoryKey()
			if (err != nil) != test.wantErr {
				t.Fatalf("sharedMemoryKey() error = %v, wantErr %v", err, test.wantErr)
			}
			if got != test.want {
				t.Fatalf("sharedMemoryKey() = %d, want %d", got, test.want)
			}
		})
	}
}
