package gnmi

import (
	"github.com/Azure/sonic-telemetry/common_utils"
	"github.com/golang/glog"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func BasicAuthenAndAuthor(ctx context.Context) (context.Context, error) {
	rc, ctx := common_utils.GetContext(ctx)
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ctx, status.Errorf(codes.Unknown, "Invalid context")
	}

	var username string
	var passwd string
	if username_a, ok := md["username"]; ok {
		username = username_a[0]
	} else {
		return ctx, status.Errorf(codes.Unauthenticated, "No Username Provided")
	}

	if passwd_a, ok := md["password"]; ok {
		passwd = passwd_a[0]
	} else {
		return ctx, status.Errorf(codes.Unauthenticated, "No Password Provided")
	}
	if err := PopulateAuthStruct(username, &rc.Auth, nil); err != nil {
		glog.Infof("[%s] Failed to retrieve authentication information; %v", rc.ID, err)
		return ctx, status.Errorf(codes.Unauthenticated, "")
	}
	auth_success, _ := UserPwAuth(username, passwd)
	if auth_success == false {
		return ctx, status.Errorf(codes.PermissionDenied, "Invalid Password")
	}

	return ctx, nil
}
