package main

import "testing"

func TestWorkerInvocationRequiresAllArguments(t *testing.T) {
	if result := run([]string{"worker"}, -1); result != 2 {
		t.Fatalf("invalid invocation returned %d", result)
	}
}

func TestWorkerCannotReadOutsideManagedImages(t *testing.T) {
	if result := run([]string{"worker", "/", "open", "/etc/shadow", ""}, -1); result != 1 {
		t.Fatalf("unmanaged read returned %d", result)
	}
}
