package gnmi

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"time"
	"github.com/sonic-net/sonic-gnmi/common_utils"
	"github.com/sonic-net/sonic-gnmi/swsscommon"
	"github.com/golang/glog"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/security/advancedtls"
)

const DEFAULT_CRL_EXPIRE_DURATION time.Duration = 24 * 60* 60 * time.Second

type Crl struct {
	thisUpdate   time.Time
	nextUpdate   time.Time
	crl         []byte
}

// CRL content cache
var CrlCache map[string]*Crl = nil

// CRL content cache
var CrlDxpireDuration time.Duration = DEFAULT_CRL_EXPIRE_DURATION

func InitCrlCache() {
	if CrlCache == nil {
		CrlCache = make(map[string]*Crl)
	}
}

func ReleaseCrlCache() {
	for mapkey, _ := range(CrlCache) {
		delete(CrlCache, mapkey)
	}
}

func AppendCrlToCache(url string, rawCRL []byte) {
	crl := new(Crl)
	crl.thisUpdate = time.Now()
	crl.nextUpdate = time.Now()
	crl.crl = rawCRL

	CrlCache[url] = crl
}

func GetCrlExpireDuration() time.Duration {
	return CrlDxpireDuration
}

func SetCrlExpireDuration(duration time.Duration) {
	CrlDxpireDuration = duration
}

func CrlExpired(crl *Crl) bool {
	now := time.Now()
	expireTime := crl.thisUpdate.Add(GetCrlExpireDuration())
	glog.Infof("CrlExpired expireTime: %s, now: %s", expireTime.Format(time.ANSIC), now.Format(time.ANSIC))
	// CRL expiresion policy follow the policy of Get-CRLFreshness command in following doc:
	// 		https://learn.microsoft.com/en-us/archive/blogs/russellt/get-crlfreshness
	// The policy are:
	//      1. CRL expired when current time is after CRL expiresion time, which defined in "Next CRL Publish" extension.
	// Because CRL cached in memory, GNMI support OnDemand CRL referesh by restart GNMI service.
	return now.After(expireTime)
}

func CrlNeedUpdate(crl *Crl) bool {
	now := time.Now()
	glog.Infof("CrlNeedUpdate nextUpdate: %s, now: %s", crl.nextUpdate.Format(time.ANSIC), now.Format(time.ANSIC))
	return now.After(crl.nextUpdate)
}

func RemoveExpiredCrl() {
	for mapkey, crl := range(CrlCache) {
		if CrlExpired(crl) {
			glog.Infof("RemoveExpiredCrl key: %s", mapkey)
			delete(CrlCache, mapkey)
		}
	}
}

func SearchCrlCache(url string) (bool, *Crl) {
	crl, exist := CrlCache[url]
	if !exist {
		glog.Infof("SearchCrlCache not found cache for url: %s", url)
		return false, nil
	}

	if CrlExpired(crl) {
		glog.Infof("SearchCrlCache crl expired: %s", url)
		delete(CrlCache, url)
		return false, nil
	}

	if CrlNeedUpdate(crl) {
		glog.Infof("SearchCrlCache crl need update: %s", url)
		delete(CrlCache, url)
		return false, nil
	}

	glog.Infof("SearchCrlCache found cache for url: %s", url)
	return true, crl
}

func ClientCertAuthenAndAuthor(ctx context.Context, serviceConfigTableName string, enableCrl bool) (context.Context, error) {
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

	if enableCrl {
		err := VerifyCertCrl(tlsAuth.State)
		if err != nil {
			glog.Infof("[%s] Failed to verify cert with CRL; %v", rc.ID, err)
			return ctx, err
		}
	}

	return ctx, nil
}

func TryDownload(url string) bool {
	glog.Infof("Download CRL start: %s", url)
	resp, err := http.Get(url)

	if resp != nil {
		defer resp.Body.Close()
	}

	if err != nil || resp.StatusCode != http.StatusOK {
		glog.Infof("Download CRL: %s failed: %v", url, err)
		return false
	}

	crlContent, err := io.ReadAll(resp.Body) 
	if err != nil {
		glog.Infof("Download CRL: %s failed: %v", url, err)
		return false
	}

	glog.Infof("Download CRL: %s successed", url)
	AppendCrlToCache(url, crlContent)

	return true
}

func GetCrlUrls(cert x509.Certificate) []string {
	glog.Infof("Get Crl Urls for cert: %v", cert.CRLDistributionPoints)
	return cert.CRLDistributionPoints
}

func DownloadNotCachedCrl(crlUrlArray []string) bool {
	crlAvaliable := false
    for _, crlUrl := range crlUrlArray{
		exist, _ := SearchCrlCache(crlUrl)
		if exist {
			crlAvaliable = true
		} else {
			downloaded := TryDownload(crlUrl)
			if downloaded {
				crlAvaliable = true
			}
		}
    }

	return crlAvaliable
}

func CreateStaticCRLProvider() *advancedtls.StaticCRLProvider {
	crlArray := make([][]byte, 1)
	for mapkey, item := range(CrlCache) {
		if CrlExpired(item) {
			glog.Infof("CreateStaticCRLProvider remove expired crl: %s", mapkey)
			delete(CrlCache, mapkey)
		} else {
			glog.Infof("CreateStaticCRLProvider add crl: %s content: %v", mapkey, item.crl)
			crlArray = append(crlArray, item.crl)
		}
	}
	
	return advancedtls.NewStaticCRLProvider(crlArray)
}

func VerifyCertCrl(tlsConnState tls.ConnectionState) error {
	InitCrlCache()
	// Check if any CRL already exist in local
	crlUriArray := GetCrlUrls(*tlsConnState.VerifiedChains[0][0])
	if len(crlUriArray) == 0 {
		glog.Infof("Cert does not contains and CRL distribution points")
		return nil
	}

	crlAvaliable := DownloadNotCachedCrl(crlUriArray)
	if !crlAvaliable {
		// Every certificate will contain multiple CRL distribution points.
		// If all CRLs are not available, the certificate validation should be blocked.
		glog.Infof("VerifyCertCrl can't download CRL and verify cert: %v", crlUriArray)
		return status.Errorf(codes.Unauthenticated, "Can't download CRL and verify cert")
	}

	// Build CRL provider from cache and verify cert
	crlProvider := CreateStaticCRLProvider()
	err := advancedtls.CheckChainRevocation(tlsConnState.VerifiedChains, advancedtls.RevocationOptions{
		DenyUndetermined:  false,
		CRLProvider:       crlProvider,
	})

	if err != nil {
		glog.Infof("VerifyCertCrl peer certificate revoked: %v", err.Error())
		return status.Error(codes.Unauthenticated, "Peer certificate revoked")
	}

	glog.Infof("VerifyCertCrl verify cert passed: %v", crlUriArray)
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