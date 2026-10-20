package gnmi

import (
	"context"

	"github.com/sonic-net/sonic-gnmi/common_utils"
)

func configuredRPCAuthorizationInterceptor(config *Config) (*rpcAuthorizationInterceptor, string) {
	if config == nil {
		return nil, ""
	}
	enabled, unsupportedMode := rpcAuthorizationMode(map[string]bool(config.UserAuth), config.ConfigTableName)
	if !enabled {
		return nil, unsupportedMode
	}

	return newRPCAuthorizationInterceptor(
		func(ctx context.Context) (context.Context, *rpcIdentity, error) {
			rc, ctx := common_utils.GetContext(ctx)
			rc.Auth.AuthEnabled = true
			rc.Auth.User = ""
			rc.Auth.Roles = nil

			authenticatedCtx, err := ClientCertAuthenAndAuthor(ctx, config.ConfigTableName, config.EnableCrl)
			rc, authenticatedCtx = common_utils.GetContext(authenticatedCtx)
			return authenticatedCtx, &rpcIdentity{
				user:  rc.Auth.User,
				roles: rc.Auth.Roles,
			}, err
		},
		isUnixPeer,
	), ""
}
