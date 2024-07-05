package gnmi

import (
	"crypto/md5"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"github.com/sonic-net/sonic-gnmi/common_utils"
	"github.com/sonic-net/sonic-gnmi/swsscommon"
	"github.com/golang/glog"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

func ClientCertAuthenAndAuthor(ctx context.Context, serviceConfigTableName string) (context.Context, error) {
	rc, ctx := common_utils.GetContext(ctx)
	p, ok := peer.FromContext(ctx)
	if !ok {
		return ctx, status.Error(codes.Unauthenticated, "no peer found")
	}
	tlsAuth, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok {
		return ctx, status.Error(codes.Unauthenticated, "unexpected peer transport credentials")
	}
	if len(tlsAuth.State.VerifiedChains) == 0 || len(tlsAuth.State.VerifiedChains[0]) == 0 {
		return ctx, status.Error(codes.Unauthenticated, "could not verify peer certificate")
	}

	var username string

	username = tlsAuth.State.VerifiedChains[0][0].Subject.CommonName

	if len(username) == 0 {
		return ctx, status.Error(codes.Unauthenticated, "invalid username in certificate common name.")
	}

	if serviceConfigTableName != "" {
		if err := PopulateAuthStructByCommonName(username, &rc.Auth, serviceConfigTableName); err != nil {
			return ctx, err
		}
	} else {
		if err := PopulateAuthStruct(username, &rc.Auth, nil); err != nil {
			glog.Infof("[%s] Failed to retrieve authentication information; %v", rc.ID, err)
			return ctx, status.Errorf(codes.Unauthenticated, "")
		}
	}

	if err := VerifyCertCrl(tlsAuth.State); err != nil {
		glog.Infof("[%s] Failed to verify cert with CRL; %v", rc.ID, err)
		return ctx, status.Errorf(codes.Unauthenticated, "")
	}

	return ctx, nil
}

func GetLocalCrlPath(crlUrl string) string {
	crlHash := md5.Sum([]byte(crlUrl))
	localFileName := hex.EncodeToString(crlHash[:])
	return fmt.Sprintf("/etc/sonic/crl/%s.crl", localFileName)
}

func CheckCrlDownloaded(crlUrlArray []string) (bool, string) {
    for _,crlUrl := range crlUrlArray{
		localFilePath := GetLocalCrlPath(crlUrl)

		// check if CRL already downloaded
		_, err := os.Stat(localFilePath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}

		// TODO: check file expired
		return true, localFilePath
    }

	return false, ""
}

func TryDownload(url string) bool {
	destPath := GetLocalCrlPath(url)
	out, err := os.Create(destPath)
	defer out.Close()
	if err != nil {
		glog.Infof("Create local CRL: %s failed: %v", destPath, err)
		return false
	}

	resp, err := http.Get(url)
	defer resp.Body.Close()
	if err != nil {
		glog.Infof("Download CRL: %s failed: %v", url, err)
		return false
	}

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		glog.Infof("Download CRL: %s to local: %s failed: %v", url, destPath, err)
		return false
	}

	return true
}

func TryDownloadCrl(crlUrlArray []string) bool {
	// download file to local, and update appl_db
    for _,crlUrl := range crlUrlArray{
		downloaded := TryDownload(crlUrl)
		if downloaded {
			glog.Infof("Downloaded CRL: %s", crlUrl)
			return true
		}
    }

	return false
}

func VerifyCertCrl(tlsConnState tls.ConnectionState) error {
	// Check if any CRL already exist in local
	crlUriArray := tlsConnState.VerifiedChains[0][0].CRLDistributionPoints
	downloaded, localPath := CheckCrlDownloaded(crlUriArray)
	if !downloaded {
		downloaded = TryDownloadCrl(crlUriArray)
		if !downloaded {
			return status.Errorf(codes.Unauthenticated, "Can't download CRL and verify cert")
		}
	}

	// Verify cert with CRL
	rawCRLs := make([][]byte, 6)
	rawCRL, err := os.ReadFile(localPath)
	if err != nil {
		return status.Errorf(codes.Unauthenticated, "Can't load CRL and verify cert")
	}
	
	rawCRLs = append(rawCRLs, rawCRL)
	cRLProvider := NewStaticCRLProvider(rawCRLs)
	err = checkRevocation(tlsConnState, RevocationConfig{
		AllowUndetermined: true,
		CRLProvider:       cRLProvider,
	})
	if err != nil {
		return status.Error(codes.Unauthenticated, "Peer certificate revoked")
	}

	return nil
}

func PopulateAuthStructByCommonName(certCommonName string, auth *common_utils.AuthInfo, serviceConfigTableName string) error {
	if serviceConfigTableName == "" {
		return status.Errorf(codes.Unauthenticated, "Service config table name should not be empty")
	}

	var configDbConnector = swsscommon.NewConfigDBConnector()
	defer swsscommon.DeleteConfigDBConnector_Native(configDbConnector.ConfigDBConnector_Native)
	configDbConnector.Connect(false)

	var fieldValuePairs = configDbConnector.Get_entry(serviceConfigTableName, certCommonName)
	if fieldValuePairs.Size() > 0 {
		if fieldValuePairs.Has_key("role") {
			var role = fieldValuePairs.Get("role")
			auth.Roles = []string{role}
		}
	} else {
		glog.Warningf("Failed to retrieve cert common name mapping; %s", certCommonName)
	}

	swsscommon.DeleteFieldValueMap(fieldValuePairs)

	if len(auth.Roles) == 0 {
		return status.Errorf(codes.Unauthenticated, "Invalid cert cname:'%s', not a trusted cert common name.", certCommonName)
	} else {
		return nil
	}
}