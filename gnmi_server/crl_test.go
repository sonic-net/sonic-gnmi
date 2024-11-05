package gnmi

// server_test covers gNMI get, subscribe (stream and poll) test
// Prerequisite: redis-server should be running.
import (
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"os"
	"testing"
	"time"
	"github.com/agiledragon/gomonkey/v2"
	"github.com/sonic-net/sonic-gnmi/common_utils"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestCrlExpireDuration(t *testing.T) {
	duration := GetCrlExpireDuration()
	if duration != DEFAULT_CRL_EXPIRE_DURATION {
		t.Errorf("TestCrlExpireDuration test failed, default expire duration incorrect.")
	}

	newDuration := 60 * time.Second
	SetCrlExpireDuration(newDuration)
	duration = GetCrlExpireDuration()
	if duration != newDuration {
		t.Errorf("TestCrlExpireDuration test failed, change expire duration failed.")
	}
}

func TestCrlCache(t *testing.T) {
	InitCrlCache()
	defer ReleaseCrlCache()

	rawCRL, _ := os.ReadFile("/tmp/testdata/crl/test.crl")
	AppendCrlToCache("http://test.crl.com/test.crl", rawCRL)

	// test CRL expired
	exist, cacheItem := SearchCrlCache("http://test.crl.com/test.crl")
	if exist {
		t.Errorf("TestCrlCache test failed, crl should expired.")
	}

	if cacheItem != nil {
		t.Errorf("TestCrlCache test failed, crl content incorrect.")
	}
	
	// test CRL expired and remove
	AppendCrlToCache("http://test.crl.com/test.crl", rawCRL)
	RemoveExpiredCrl()
	_, exist = CrlCache["http://test.crl.com/test.crl"]
	if exist {
		t.Errorf("TestCrlCache test failed, expired crl should removed.")
	}

	// test CRL does not exist
	exist, cacheItem = SearchCrlCache("http://test.crl.com/notexist.crl")
	if exist {
		t.Errorf("TestCrlCache test failed, crl should not exist.")
	}

	if cacheItem != nil {
		t.Errorf("TestCrlCache test failed, crl content incorrect.")
	}

	// mock to make test CRL valied
	mockCrlExpired := gomonkey.ApplyFunc(CrlExpired, func(crl *Crl) bool {
		return false
	})
	defer mockCrlExpired.Reset()

	AppendCrlToCache("http://test.crl.com/test.crl", rawCRL)
	exist, cacheItem = SearchCrlCache("http://test.crl.com/test.crl")
	if !exist {
		t.Errorf("TestCrlCache test failed, crl should exist.")
	}

	if len(cacheItem.crl) != len(rawCRL) {
		t.Errorf("TestCrlCache test failed, crl content incorrect.")
	}
}

func TestGetCrlUrls(t *testing.T) {
	cert := x509.Certificate{
		Subject: pkix.Name{
			CommonName: "certname1",
		},
		CRLDistributionPoints: []string{
			"http://test.crl.com/test.crl",
		},
	}

	crlUrlArray := GetCrlUrls(cert)

	if len(crlUrlArray) != 1 {
		t.Errorf("TestGetCrlUrls get incorrect CRLDistributionPoints.")
	}

	if crlUrlArray[0] != "http://test.crl.com/test.crl" {
		t.Errorf("TestGetCrlUrls get incorrect CRL.")
	}
}

func makeChain(name string) []*x509.Certificate {
	certChain := make([]*x509.Certificate, 0)

	rest, err := os.ReadFile(name)
	if err != nil {
		fmt.Printf("makeChain ReadFile err: %s\n", err.Error())
	}

	for len(rest) > 0 {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			fmt.Printf("makeChain ParseCertificate err: %s\n", err.Error())
		}
		certChain = append(certChain, c)
	}
	return certChain
}

func CreateConnectionState(certPath string) tls.ConnectionState {
	certChain := makeChain(certPath)
	return tls.ConnectionState {
		VerifiedChains: [][]*x509.Certificate {
			certChain,
		},
	}
}

func TestVerifyCertCrl(t *testing.T) {
	InitCrlCache()
	defer ReleaseCrlCache()

	mockGetCrlUrls := gomonkey.ApplyFunc(GetCrlUrls, func(cert x509.Certificate) []string {
		return []string{ "http://test.crl.com/test.crl" }
	})
	defer mockGetCrlUrls.Reset()

	mockCrlExpired := gomonkey.ApplyFunc(CrlExpired, func(crl *Crl) bool {
		return false
	})
	defer mockCrlExpired.Reset()

	mockTryDownload := gomonkey.ApplyFunc(TryDownload, func(url string) bool {
		rawCRL, _ := os.ReadFile("/tmp/testdata/crl/test.crl")
		AppendCrlToCache("http://test.crl.com/test.crl", rawCRL)
		return true
	})
	defer mockTryDownload.Reset()

	// test revoked cert
	tlsConnState := CreateConnectionState("/tmp/testdata/crl/revokedInt.pem")
	err := VerifyCertCrl(tlsConnState)
	if err == nil {
		t.Errorf("TestVerifyCertCrl verify revoked cert failed.")
	}

	// test valid cert
	tlsConnState = CreateConnectionState("/tmp/testdata/crl/unrevoked.pem")
	err = VerifyCertCrl(tlsConnState)
	if err != nil {
		t.Errorf("TestVerifyCertCrl verify unrevoked cert failed.")
	}
}


func TestVerifyCertCrlWithDownloadFailed(t *testing.T) {
	InitCrlCache()
	defer ReleaseCrlCache()

	mockGetCrlUrls := gomonkey.ApplyFunc(GetCrlUrls, func(cert x509.Certificate) []string {
		return []string{ "http://test.crl.com/test.crl" }
	})
	defer mockGetCrlUrls.Reset()

	mockTryDownload := gomonkey.ApplyFunc(TryDownload, func(url string) bool {
		return false
	})
	defer mockTryDownload.Reset()

	// test valid cert,should failed because download CRL failed
	tlsConnState := CreateConnectionState("/tmp/testdata/crl/unrevoked.pem")
	err := VerifyCertCrl(tlsConnState)
	if err == nil {
		t.Errorf("TestVerifyCertCrl verify unrevoked cert should failed when CRL can't download.")
	}
}

func TestClientCertAuthenAndAuthorWithCrl(t *testing.T) {
	// initialize err variable
	err := status.Error(codes.Unauthenticated, "")

	// when config table is empty, will authorize with PopulateAuthStruct
	mockpopulate := gomonkey.ApplyFunc(PopulateAuthStruct, func(username string, auth *common_utils.AuthInfo, r []string) error {
		return nil
	})
	defer mockpopulate.Reset()

	// mock for revoked cert
	mockVerifyCertCrl := gomonkey.ApplyFunc(VerifyCertCrl, func(tlsConnState tls.ConnectionState) error {
		return status.Error(codes.Unauthenticated, "Peer certificate revoked")
	})

	// check auth with nil cert name
	ctx, cancel := CreateAuthorizationCtx()
	ctx, err = ClientCertAuthenAndAuthor(ctx, "", true)
	if err == nil {
		t.Errorf("Auth with revoked cert should failed.")
	}

	cancel()
	mockVerifyCertCrl.Reset()

	// mock for unrevoked cert
	mockVerifyCertCrl = gomonkey.ApplyFunc(VerifyCertCrl, func(tlsConnState tls.ConnectionState) error {
		return nil
	})

	// check auth with nil cert name
	ctx, cancel = CreateAuthorizationCtx()
	ctx, err = ClientCertAuthenAndAuthor(ctx, "", true)
	if err != nil {
		t.Errorf("Auth with revoked cert should failed: %v", err)
	}

	cancel()
	mockVerifyCertCrl.Reset()
}