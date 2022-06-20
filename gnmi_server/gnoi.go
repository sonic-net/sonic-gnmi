package gnmi

import (
	"context"
	"errors"
	"os"
	"os/user"
	"time"
	gnoi_system_pb "github.com/openconfig/gnoi/system"
	log "github.com/golang/glog"
	io "io/ioutil"
	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"
	spb_jwt "github.com/sonic-net/sonic-gnmi/proto/gnoi/jwt"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/codes"
	jwt "github.com/dgrijalva/jwt-go"
)

func RebootSystem(fileName string) error {
	log.V(2).Infof("Rebooting with %s...", fileName)
	sc, err := ssc.NewDbusClient()
	if err != nil {
		return err
	}
	err = sc.ConfigReload(fileName)
	return err
}

func (srv *Server) Reboot(ctx context.Context, req *gnoi_system_pb.RebootRequest) (*gnoi_system_pb.RebootResponse, error) {
	fileName := "/etc/sonic/gnmi/config_db.json.tmp"
	_,err := authenticate(srv.config.UserAuth, ctx)
	if err != nil {
		return nil, err
	}
	log.V(1).Info("gNOI: Reboot")
	log.V(1).Info("Request:", req)
	log.V(1).Info("Reboot system now, delay is ignored...")
	_, err = io.ReadFile(fileName)
	if errors.Is(err, os.ErrNotExist) {
		fileName = ""
	}
	RebootSystem(fileName)
	var resp gnoi_system_pb.RebootResponse
	return &resp, nil
}

func (srv *Server) RebootStatus(ctx context.Context, req *gnoi_system_pb.RebootStatusRequest) (*gnoi_system_pb.RebootStatusResponse, error) {
	_, err := authenticate(srv.config.UserAuth, ctx)
	if err != nil {
		return nil, err
	}
	log.V(1).Info("gNOI: RebootStatus")
	return nil, status.Errorf(codes.Unimplemented, "")
}

func (srv *Server) CancelReboot(ctx context.Context, req *gnoi_system_pb.CancelRebootRequest) (*gnoi_system_pb.CancelRebootResponse, error) {
	_, err := authenticate(srv.config.UserAuth, ctx)
	if err != nil {
		return nil, err
	}
	log.V(1).Info("gNOI: CancelReboot")
	return nil, status.Errorf(codes.Unimplemented, "")
}

func (srv *Server) Ping(req *gnoi_system_pb.PingRequest, rs gnoi_system_pb.System_PingServer) error {
	ctx := rs.Context()
	_, err := authenticate(srv.config.UserAuth, ctx)
	if err != nil {
		return err
	}
	log.V(1).Info("gNOI: Ping")
	return status.Errorf(codes.Unimplemented, "")
}

func (srv *Server) Traceroute(req *gnoi_system_pb.TracerouteRequest, rs gnoi_system_pb.System_TracerouteServer) error {
	ctx := rs.Context()
	_, err := authenticate(srv.config.UserAuth, ctx)
	if err != nil {
		return err
	}
	log.V(1).Info("gNOI: Traceroute")
	return status.Errorf(codes.Unimplemented, "")
}

func (srv *Server) SetPackage(rs gnoi_system_pb.System_SetPackageServer) error {
	ctx := rs.Context()
	_, err := authenticate(srv.config.UserAuth, ctx)
	if err != nil {
		return err
	}
	log.V(1).Info("gNOI: SetPackage")
	return status.Errorf(codes.Unimplemented, "")
}

func (srv *Server) SwitchControlProcessor(ctx context.Context, req *gnoi_system_pb.SwitchControlProcessorRequest) (*gnoi_system_pb.SwitchControlProcessorResponse, error) {
	_, err := authenticate(srv.config.UserAuth, ctx)
	if err != nil {
		return nil, err
	}
	log.V(1).Info("gNOI: SwitchControlProcessor")
	return nil, status.Errorf(codes.Unimplemented, "")
}

func (srv *Server) Time(ctx context.Context, req *gnoi_system_pb.TimeRequest) (*gnoi_system_pb.TimeResponse, error) {
	_, err := authenticate(srv.config.UserAuth, ctx)
	if err != nil {
		return nil, err
	}
	log.V(1).Info("gNOI: Time")
	var tm gnoi_system_pb.TimeResponse
	tm.Time = uint64(time.Now().UnixNano())
	return &tm, nil
}

func (srv *Server) Authenticate(ctx context.Context, req *spb_jwt.AuthenticateRequest) (*spb_jwt.AuthenticateResponse, error) {
	// Can't enforce normal authentication here.. maybe only enforce client cert auth if enabled?
	// ctx,err := authenticate(srv.config.UserAuth, ctx)
	// if err != nil {
	// 	return nil, err
	// }
	log.V(1).Info("gNOI: Sonic Authenticate")


	if !srv.config.UserAuth.Enabled("jwt") {
		return nil, status.Errorf(codes.Unimplemented, "")
	}
	auth_success, _ := UserPwAuth(req.Username, req.Password)
	if  auth_success {
		usr, err := user.Lookup(req.Username)
		if err == nil {
			roles, err := GetUserRoles(usr)
			if err == nil {
				return &spb_jwt.AuthenticateResponse{Token: tokenResp(req.Username, roles)}, nil
			}
		}
		
	}
	return nil, status.Errorf(codes.PermissionDenied, "Invalid Username or Password")

}

func (srv *Server) Refresh(ctx context.Context, req *spb_jwt.RefreshRequest) (*spb_jwt.RefreshResponse, error) {
	_, err := authenticate(srv.config.UserAuth, ctx)
	if err != nil {
		return nil, err
	}
	log.V(1).Info("gNOI: Sonic Refresh")

	if !srv.config.UserAuth.Enabled("jwt") {
		return nil, status.Errorf(codes.Unimplemented, "")
	}

	token, _, err := JwtAuthenAndAuthor(ctx)
	if err != nil {
		return nil, err
	}

	claims := &Claims{}
	jwt.ParseWithClaims(token.AccessToken, claims, func(token *jwt.Token) (interface{}, error) {
		return hmacSampleSecret, nil
	})
	if time.Unix(claims.ExpiresAt, 0).Sub(time.Now()) > JwtRefreshInt {
		return nil, status.Errorf(codes.InvalidArgument, "Invalid JWT Token")
	}
	
	return &spb_jwt.RefreshResponse{Token: tokenResp(claims.Username, claims.Roles)}, nil
}

