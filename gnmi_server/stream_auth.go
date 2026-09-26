package gnmi

import (
	"time"

	log "github.com/golang/glog"
	"golang.org/x/net/context"
)

// monitorAuthentication periodically revalidates the credentials and current
// authorization of a long-lived Subscribe stream. Report failures before
// closing the client so Run can return the original gRPC status.
func monitorAuthentication(ctx context.Context, interval time.Duration, authenticate func() error,
	closeClient func(), stop <-chan struct{}, authErr chan<- error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
			if err := authenticate(); err != nil {
				select {
				case <-ctx.Done():
					return
				case <-stop:
					return
				case authErr <- err:
					log.Warningf("Periodic Subscribe authentication failed: %v", err)
					closeClient()
					return
				}
			}
		}
	}
}
