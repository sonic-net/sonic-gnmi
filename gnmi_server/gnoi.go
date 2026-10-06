package gnmi

import (
	"context"
	"encoding/json"
	jwt "github.com/dgrijalva/jwt-go"
	log "github.com/golang/glog"
	spb "github.com/sonic-net/sonic-gnmi/proto/gnoi"
	spb_jwt "github.com/sonic-net/sonic-gnmi/proto/gnoi/jwt"
	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"
	transutil "github.com/sonic-net/sonic-gnmi/transl_utils"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"os/user"
	"strings"
	"time"
)

const (
	stateDB             string = "STATE_DB"
	defaultConfigDBPath string = "/etc/sonic/config_db.json"
)

func (srv *Server) Authenticate(ctx context.Context, req *spb_jwt.AuthenticateRequest) (*spb_jwt.AuthenticateResponse, error) {
	// Can't enforce normal authentication here.. maybe only enforce client cert auth if enabled?
	// ctx,err := authenticate(srv.config, ctx, false)
	// if err != nil {
	// 	return nil, err
	// }
	log.V(1).Info("gNOI: Sonic Authenticate")

	if !srv.config.UserAuth.Enabled("jwt") {
		return nil, status.Errorf(codes.Unimplemented, "")
	}
	auth_success, _ := UserPwAuth(req.Username, req.Password)
	if auth_success {
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
	ctx, err := authenticate(srv.config, ctx, "gnoi", true)
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

func (srv *Server) ClearNeighbors(ctx context.Context, req *spb.ClearNeighborsRequest) (*spb.ClearNeighborsResponse, error) {
	ctx, err := authenticate(srv.config, ctx, "gnoi", true)
	if err != nil {
		return nil, err
	}
	log.V(1).Info("gNOI: Sonic ClearNeighbors")
	log.V(1).Info("Request: ", req)

	resp := &spb.ClearNeighborsResponse{
		Output: &spb.ClearNeighborsResponse_Output{},
	}

	reqstr, err := json.Marshal(req)
	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}

	jsresp, err := transutil.TranslProcessAction("/sonic-neighbor:clear-neighbors", []byte(reqstr), ctx)

	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}

	err = json.Unmarshal(jsresp, resp)
	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}

	return resp, nil
}

func (srv *Server) CopyConfig(ctx context.Context, req *spb.CopyConfigRequest) (*spb.CopyConfigResponse, error) {
	ctx, err := authenticate(srv.config, ctx, "gnoi", true)
	if err != nil {
		return nil, err
	}
	log.V(1).Info("gNOI: Sonic CopyConfig")

	resp := &spb.CopyConfigResponse{
		Output: &spb.SonicOutput{},
	}

	reqstr, err := json.Marshal(req)
	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}
	jsresp, err := transutil.TranslProcessAction("/sonic-config-mgmt:copy", []byte(reqstr), ctx)

	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}

	err = json.Unmarshal(jsresp, resp)
	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}

	return resp, nil
}

func (srv *Server) ShowTechsupport(ctx context.Context, req *spb.TechsupportRequest) (*spb.TechsupportResponse, error) {
	ctx, err := authenticate(srv.config, ctx, "gnoi", false)
	if err != nil {
		return nil, err
	}
	log.V(1).Info("gNOI: Sonic ShowTechsupport")

	resp := &spb.TechsupportResponse{
		Output: &spb.TechsupportResponse_Output{},
	}

	reqstr, err := json.Marshal(req)
	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}
	jsresp, err := transutil.TranslProcessAction("/sonic-show-techsupport:sonic-show-techsupport-info", []byte(reqstr), ctx)

	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}

	err = json.Unmarshal(jsresp, resp)
	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}

	return resp, nil
}

func (srv *Server) ImageInstall(ctx context.Context, req *spb.ImageInstallRequest) (*spb.ImageInstallResponse, error) {
	ctx, err := authenticate(srv.config, ctx, "gnoi", true)
	if err != nil {
		return nil, err
	}
	log.V(1).Info("gNOI: Sonic ImageInstall")

	resp := &spb.ImageInstallResponse{
		Output: &spb.SonicOutput{},
	}

	reqstr, err := json.Marshal(req)
	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}
	jsresp, err := transutil.TranslProcessAction("/sonic-image-management:image-install", []byte(reqstr), ctx)

	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}

	err = json.Unmarshal(jsresp, resp)
	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}

	return resp, nil
}

func (srv *Server) ImageRemove(ctx context.Context, req *spb.ImageRemoveRequest) (*spb.ImageRemoveResponse, error) {
	ctx, err := authenticate(srv.config, ctx, "gnoi", true)
	if err != nil {
		return nil, err
	}
	log.V(1).Info("gNOI: Sonic ImageRemove")

	resp := &spb.ImageRemoveResponse{
		Output: &spb.SonicOutput{},
	}

	reqstr, err := json.Marshal(req)

	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}
	jsresp, err := transutil.TranslProcessAction("/sonic-image-management:image-remove", []byte(reqstr), ctx)
	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}

	err = json.Unmarshal(jsresp, resp)
	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}
	return resp, nil
}

func (srv *Server) ImageDefault(ctx context.Context, req *spb.ImageDefaultRequest) (*spb.ImageDefaultResponse, error) {
	ctx, err := authenticate(srv.config, ctx, "gnoi", true)
	if err != nil {
		return nil, err
	}
	log.V(1).Info("gNOI: Sonic ImageDefault")

	resp := &spb.ImageDefaultResponse{
		Output: &spb.SonicOutput{},
	}

	reqstr, err := json.Marshal(req)

	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}
	jsresp, err := transutil.TranslProcessAction("/sonic-image-management:image-default", []byte(reqstr), ctx)
	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}

	err = json.Unmarshal(jsresp, resp)
	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}

	return resp, nil
}

// ConfigSave persists the running configuration to the SONiC startup config
// file (defaultConfigDBPath) via the host service `config.save` D-Bus method.
func (srv *Server) ConfigSave(ctx context.Context, req *spb.ConfigSaveRequest) (*spb.ConfigSaveResponse, error) {
	ctx, err := authenticate(srv.config, ctx, "gnoi", true)
	if err != nil {
		return nil, err
	}
	log.V(1).Info("gNOI: Sonic ConfigSave")

	sc, err := ssc.NewDbusClient()
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if err := sc.ConfigSave(defaultConfigDBPath); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &spb.ConfigSaveResponse{Output: &spb.SonicOutput{}}, nil
}

// ConfigReload reapplies configuration via the host service `config.reload` D-Bus
// method. Empty config_json reloads from the startup file; otherwise the supplied
// JSON is validated and piped into `config reload -y /dev/stdin`. Payload is never
// logged (may contain credentials); only a boolean marker is.
func (srv *Server) ConfigReload(ctx context.Context, req *spb.ConfigReloadRequest) (*spb.ConfigReloadResponse, error) {
	ctx, err := authenticate(srv.config, ctx, "gnoi", true)
	if err != nil {
		return nil, err
	}

	cfg := req.GetInput().GetConfigJson()
	inline := strings.TrimSpace(cfg) != ""
	log.V(1).Infof("gNOI: Sonic ConfigReload inline=%t", inline)

	if inline {
		var probe interface{}
		if err := json.Unmarshal([]byte(cfg), &probe); err != nil {
			return nil, status.Error(codes.InvalidArgument, "config_json is not valid JSON")
		}
	}

	sc, err := ssc.NewDbusClient()
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if err := sc.ConfigReload(cfg); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &spb.ConfigReloadResponse{Output: &spb.SonicOutput{}}, nil
}
