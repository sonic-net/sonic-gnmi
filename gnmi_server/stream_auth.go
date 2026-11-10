package gnmi

import (
	"time"

	log "github.com/golang/glog"
	"github.com/sonic-net/sonic-gnmi/common_utils"
	"golang.org/x/net/context"
)

func reauthenticate(config *Config, ctx context.Context, target string) error {
	if rpcAuthorizationCompleted(ctx) && !isUnixPeer(ctx) {
		// Initial interceptor-to-handler entry reuses the completed decision.
		// Periodic checks must refresh the credential source instead.
		ctx = common_utils.WithFreshAuth(ctx)
		rc, ctx := common_utils.GetContext(ctx)
		rc.Auth.AuthEnabled = true
		ctx, err := ClientCertAuthenAndAuthor(ctx, config.ConfigTableName, config.EnableCrl)
		if err != nil {
			return err
		}
		rc, _ = common_utils.GetContext(ctx)
		return checkCompletedRPCAuthorization(ctx, rc.Auth.User, rc.Auth.Roles, target)
	}
	_, err := authenticate(config, ctx, target, false)
	return err
}

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
