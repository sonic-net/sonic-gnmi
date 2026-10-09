package gnmi

import (
	"context"
	"time"

	log "github.com/golang/glog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	healthzArtifactReady   = "Artifact ready"
	healthzArtifactPending = "Artifact not ready"
)

var (
	artifactColTimeout = 5 * time.Minute
	artifactSleepTime  = 5 * time.Second
)

type healthzArtifactChecker interface {
	HealthzCheck(string) (string, error)
}

// waitForArtifact polls only while the host service reports its documented
// pending state. Request cancellation, collection timeout, D-Bus errors, and
// unknown responses terminate the wait immediately.
func waitForArtifact(ctx context.Context, checker healthzArtifactChecker, file string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, artifactColTimeout)
	defer cancel()
	for {
		if err := ctx.Err(); err != nil {
			return "", artifactWaitContextError(file, err)
		}

		result, err := checker.HealthzCheck(file)
		if err != nil {
			return "", status.Errorf(
				codes.Internal,
				"HealthzCheck failed for artifact %q: %v",
				file,
				err,
			)
		}
		log.V(2).Infof("HealthzCheck status=%q artifact=%q", result, file)
		switch result {
		case healthzArtifactReady:
			return result, nil
		case healthzArtifactPending:
			// The host service accepted collection but has not completed it yet.
		default:
			return "", status.Errorf(
				codes.Internal,
				"HealthzCheck returned unexpected status %q for artifact %q",
				result,
				file,
			)
		}

		timer := time.NewTimer(artifactSleepTime)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", artifactWaitContextError(file, ctx.Err())
		case <-timer.C:
		}
	}
}

func artifactWaitContextError(file string, err error) error {
	code := codes.Canceled
	if err == context.DeadlineExceeded {
		code = codes.DeadlineExceeded
	}
	return status.Errorf(
		code,
		"artifact collection wait for %q ended: %v",
		file,
		err,
	)
}
