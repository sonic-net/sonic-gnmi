package utils

import (
	"context"
	"flag"
	"google.golang.org/grpc/metadata"
)

var (
	jwtToken = flag.String("jwt_token", "", "JWT Token if required")
)

func SetUserCreds(ctx context.Context) context.Context {
	if len(*jwtToken) > 0 {
		ctx = metadata.AppendToOutgoingContext(ctx, "access_token", *jwtToken)
	}
	return ctx
}
