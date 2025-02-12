package utils

import (
	"context"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/config"
	"google.golang.org/grpc/metadata"
)

func SetUserCreds(ctx context.Context) context.Context {
	if len(*config.JwtToken) > 0 {
		ctx = metadata.AppendToOutgoingContext(ctx, "access_token", *config.JwtToken)
	}
	return ctx
}
