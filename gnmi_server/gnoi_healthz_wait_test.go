package gnmi

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type healthzCheckResult struct {
	status string
	err    error
}

type scriptedHealthzChecker struct {
	results []healthzCheckResult
	calls   int
}

func (checker *scriptedHealthzChecker) HealthzCheck(string) (string, error) {
	checker.calls++
	index := checker.calls - 1
	if index >= len(checker.results) {
		index = len(checker.results) - 1
	}
	return checker.results[index].status, checker.results[index].err
}

func TestWaitForArtifactLifecycle(t *testing.T) {
	previousTimeout := artifactColTimeout
	previousSleepTime := artifactSleepTime
	artifactColTimeout = 20 * time.Millisecond
	artifactSleepTime = 2 * time.Millisecond
	t.Cleanup(func() {
		artifactColTimeout = previousTimeout
		artifactSleepTime = previousSleepTime
	})

	tests := []struct {
		name         string
		results      []healthzCheckResult
		cancelBefore bool
		wantResult   string
		wantCode     codes.Code
		wantContains string
		wantCalls    int
		minimumCalls int
	}{
		{
			name: "pending then ready",
			results: []healthzCheckResult{
				{status: healthzArtifactPending},
				{status: healthzArtifactReady},
			},
			wantResult: healthzArtifactReady,
			wantCode:   codes.OK,
			wantCalls:  2,
		},
		{
			name:         "D-Bus error is immediate",
			results:      []healthzCheckResult{{err: fmt.Errorf("permanent D-Bus failure")}},
			wantCode:     codes.Internal,
			wantContains: "permanent D-Bus failure",
			wantCalls:    1,
		},
		{
			name:         "unexpected status is terminal",
			results:      []healthzCheckResult{{status: "unexpected status"}},
			wantCode:     codes.Internal,
			wantContains: "unexpected status",
			wantCalls:    1,
		},
		{
			name:         "request cancellation stops polling",
			results:      []healthzCheckResult{{status: healthzArtifactPending}},
			cancelBefore: true,
			wantCode:     codes.Canceled,
			wantCalls:    0,
		},
		{
			name:         "pending status times out",
			results:      []healthzCheckResult{{status: healthzArtifactPending}},
			wantCode:     codes.DeadlineExceeded,
			minimumCalls: 2,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if test.cancelBefore {
				cancel()
			}
			checker := &scriptedHealthzChecker{results: test.results}
			result, err := waitForArtifact(ctx, checker, "/tmp/dump/artifact")
			if result != test.wantResult || status.Code(err) != test.wantCode {
				t.Fatalf("waitForArtifact() = (%q, %v), want result %q and code %v", result, err, test.wantResult, test.wantCode)
			}
			if test.wantContains != "" && !strings.Contains(err.Error(), test.wantContains) {
				t.Fatalf("waitForArtifact() error = %v, want %q", err, test.wantContains)
			}
			if test.wantCalls != 0 && checker.calls != test.wantCalls {
				t.Fatalf("HealthzCheck calls = %d, want %d", checker.calls, test.wantCalls)
			}
			if checker.calls < test.minimumCalls {
				t.Fatalf("HealthzCheck calls = %d, want at least %d", checker.calls, test.minimumCalls)
			}
		})
	}
}
