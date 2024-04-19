package gnmi

import (
	"os"
	"github.com/sonic-net/sonic-gnmi/common_utils"
	"github.com/golang/glog"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

func ClientCertAuthenAndAuthor(ctx context.Context) (context.Context, error) {
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

	// [Hua] POC to verify CRL can work
	rawCRLs := make([][]byte, 6)
	rawCRL, err := os.ReadFile("/etc/sonic/crl/test.crl")
	if err == nil {
		rawCRLs = append(rawCRLs, rawCRL)
		cRLProvider := NewStaticCRLProvider(rawCRLs)
		err := checkRevocation(tlsAuth.State, RevocationConfig{
			AllowUndetermined: true,
			CRLProvider:       cRLProvider,
		})
		if err != nil {
			return ctx, status.Error(codes.Unauthenticated, "Peer certificate revoked")
		}
	} else {
		glog.Infof("[%s] [CRL] load /etc/sonic/crl/test.crl failed ; %v", rc.ID, err)
	}

	var username string

	username = tlsAuth.State.VerifiedChains[0][0].Subject.CommonName

	if len(username) == 0 {
		return ctx, status.Error(codes.Unauthenticated, "invalid username in certificate common name.")
	}

	if err := PopulateAuthStruct(username, &rc.Auth, nil); err != nil {
		glog.Infof("[%s] Failed to retrieve authentication information; %v", rc.ID, err)
		return ctx, status.Errorf(codes.Unauthenticated, "")
	}

	return ctx, nil
}
