package gnmi

import (
	"context"
	"strconv"
	"strings"
	gnoi_file_pb "github.com/openconfig/gnoi/file"
	log "github.com/golang/glog"
	"time"
	spb "github.com/sonic-net/sonic-gnmi/proto/gnoi"
	transutil "github.com/sonic-net/sonic-gnmi/transl_utils"
	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"
	spb_jwt "github.com/sonic-net/sonic-gnmi/proto/gnoi/jwt"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/codes"
	"os/user"
	"encoding/json"
	jwt "github.com/dgrijalva/jwt-go"
)

const (
	stateDB  string = "STATE_DB"
)

func ReadFileStat(path string) (*gnoi_file_pb.StatInfo, error) {
	sc, err := ssc.NewDbusClient()
	if err != nil {
		return nil, err
	}

	log.V(2).Infof("Reading file stat at path %s...", path)
	data, err := sc.GetFileStat(path)
	if err != nil {
		log.V(2).Infof("Failed to read file stat at path %s: %v. Error ", path, err)
		return nil, err
	}
	// Parse the data and populate StatInfo
	lastModified, err := strconv.ParseUint(data["last_modified"], 10, 64)
	if err != nil {
		return nil, err
	}

	permissions, err := strconv.ParseUint(data["permissions"], 8, 32)
	if err != nil {
		return nil, err
	}

	size, err := strconv.ParseUint(data["size"], 10, 64)
	if err != nil {
		return nil, err
	}

	umaskStr := data["umask"]
	if strings.HasPrefix(umaskStr, "o") {
		umaskStr = umaskStr[1:] // Remove leading "o"
	}
	umask, err := strconv.ParseUint(umaskStr, 8, 32)
	if err != nil {
		return nil, err
	}

	statInfo := &gnoi_file_pb.StatInfo{
		Path:         data["path"],
		LastModified: lastModified,
		Permissions:  uint32(permissions),
		Size:         size,
		Umask:        uint32(umask),
	}
	return statInfo, nil
}

func (srv *FileServer) Stat(ctx context.Context, req *gnoi_file_pb.StatRequest) (*gnoi_file_pb.StatResponse, error) {
	_, err := authenticate(srv.config, ctx)
	if err != nil {
		return nil, err
	}
	path := req.GetPath()
	log.V(1).Info("gNOI: Read File Stat")
	log.V(1).Info("Request: ", req)
	statInfo, err := ReadFileStat(path)
	if err != nil {
		return nil, err
	}
	resp := &gnoi_file_pb.StatResponse{
		Stats: []*gnoi_file_pb.StatInfo{statInfo},
	}
	return resp, nil
}

// TODO: Support GNOI File Get
func (srv *FileServer)  Get(req *gnoi_file_pb.GetRequest, stream gnoi_file_pb.File_GetServer) error {
	log.V(1).Info("gNOI: File Get")
	return status.Errorf(codes.Unimplemented, "")
}

func (srv *Server) Authenticate(ctx context.Context, req *spb_jwt.AuthenticateRequest) (*spb_jwt.AuthenticateResponse, error) {
	// Can't enforce normal authentication here.. maybe only enforce client cert auth if enabled?
	// ctx,err := authenticate(srv.config, ctx)
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
	ctx, err := authenticate(srv.config, ctx)
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
    ctx, err := authenticate(srv.config, ctx)
    if err != nil {
        return nil, err
    }
    log.V(1).Info("gNOI: Sonic ClearNeighbors")
    log.V(1).Info("Request: ", req)

    resp := &spb.ClearNeighborsResponse{
		Output: &spb.ClearNeighborsResponse_Output {
        },
    }

    reqstr, err := json.Marshal(req)
    if err != nil {
        return nil, status.Error(codes.Unknown, err.Error())
    }

    jsresp, err:= transutil.TranslProcessAction("/sonic-neighbor:clear-neighbors", []byte(reqstr), ctx)

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
	ctx, err := authenticate(srv.config, ctx)
	if err != nil {
		return nil, err
	}
	log.V(1).Info("gNOI: Sonic CopyConfig")
	
	resp := &spb.CopyConfigResponse{
		Output: &spb.SonicOutput {

		},
	}
	
	reqstr, err := json.Marshal(req)
	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}
	jsresp, err:= transutil.TranslProcessAction("/sonic-config-mgmt:copy", []byte(reqstr), ctx)

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
	ctx, err := authenticate(srv.config, ctx)
	if err != nil {
		return nil, err
	}
	log.V(1).Info("gNOI: Sonic ShowTechsupport")
	
	resp := &spb.TechsupportResponse{
		Output: &spb.TechsupportResponse_Output {

		},
	}

	reqstr, err := json.Marshal(req)
	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}
	jsresp, err:= transutil.TranslProcessAction("/sonic-show-techsupport:sonic-show-techsupport-info", []byte(reqstr), ctx)

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
	ctx, err := authenticate(srv.config, ctx)
	if err != nil {
		return nil, err
	}
	log.V(1).Info("gNOI: Sonic ImageInstall")
	
	resp := &spb.ImageInstallResponse{
		Output: &spb.SonicOutput {

		},
	}

	reqstr, err := json.Marshal(req)
	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}
	jsresp, err:= transutil.TranslProcessAction("/sonic-image-management:image-install", []byte(reqstr), ctx)

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
	ctx, err := authenticate(srv.config, ctx)
	if err != nil {
		return nil, err
	}
	log.V(1).Info("gNOI: Sonic ImageRemove")
	
	resp := &spb.ImageRemoveResponse{
		Output: &spb.SonicOutput {

		},
	}

	reqstr, err := json.Marshal(req)

	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}
	jsresp, err:= transutil.TranslProcessAction("/sonic-image-management:image-remove", []byte(reqstr), ctx)
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
	ctx, err := authenticate(srv.config, ctx)
	if err != nil {
		return nil, err
	}
	log.V(1).Info("gNOI: Sonic ImageDefault")
	
	resp := &spb.ImageDefaultResponse{
		Output: &spb.SonicOutput {

		},
	}

	reqstr, err := json.Marshal(req)

	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}
	jsresp, err:= transutil.TranslProcessAction("/sonic-image-management:image-default", []byte(reqstr), ctx)
	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}

	err = json.Unmarshal(jsresp, resp)
	if err != nil {
		return nil, status.Error(codes.Unknown, err.Error())
	}

	
	return resp, nil
}
