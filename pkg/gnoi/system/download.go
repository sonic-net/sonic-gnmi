package system

import (
	"net/url"

	"github.com/openconfig/gnoi/common"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func validateRemoteDownload(remote *common.RemoteDownload) error {
	u, err := url.Parse(remote.Path)
	if err != nil || u.Host == "" ||
		(u.Scheme != "http" && u.Scheme != "https") {
		return status.Error(codes.InvalidArgument, "remote package requires an HTTP(S) URL")
	}
	wantProtocol := common.RemoteDownload_HTTP
	if u.Scheme == "https" {
		wantProtocol = common.RemoteDownload_HTTPS
	}
	if remote.Protocol != common.RemoteDownload_UNKNOWN && remote.Protocol != wantProtocol {
		return status.Error(codes.InvalidArgument, "remote protocol does not match URL")
	}
	if remote.Credentials != nil || remote.SourceAddress != "" || remote.SourceVrf != "" {
		return status.Error(codes.InvalidArgument, "remote credentials and source overrides are not supported")
	}
	return nil
}
