package utils

import (
	"context"
	"google.golang.org/grpc/metadata"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/config"
)

func SetUserCreds(ctx context.Context) context.Context {
	if len(*config.JwtToken) > 0 {
		ctx = metadata.AppendToOutgoingContext(ctx, "access_token", *config.JwtToken)
	}
	return ctx
}
