package gnmi

import (
	"github.com/sonic-net/sonic-gnmi/common_utils"
	"github.com/sonic-net/sonic-gnmi/sonic_data_client"
	"github.com/sonic-net/sonic-gnmi/swsscommon"
	"github.com/golang/glog"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"strings"
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

	var username string

	username = tlsAuth.State.VerifiedChains[0][0].Subject.CommonName

	if len(username) == 0 {
		return ctx, status.Error(codes.Unauthenticated, "invalid username in certificate common name.")
	}

	err := CommonNameMatch(username)
	if err != nil {
		return ctx, err
	}

	if err = PopulateAuthStruct(username, &rc.Auth, nil); err != nil {
		glog.Infof("[%s] Failed to retrieve authentication information; %v", rc.ID, err)
		return ctx, status.Errorf(codes.Unauthenticated, "")
	}

	return ctx, nil
}

func CommonNameMatch(certCommonName string) error {
	var trustedCertCommonNames = getTrustedCertCommonNames()
	if len(trustedCertCommonNames) == 0 {
		// ignore further check because not config trusted cert common names
		return nil
	}

	for _, trustedCertCommonName := range trustedCertCommonNames {
		if certCommonName == trustedCertCommonName {
			return nil;
		}
	}

	return status.Errorf(codes.Unauthenticated, "Invalid cert cname:'%s', not a trusted cert common name.", certCommonName)
}

func getTrustedCertCommonNames() []string {
	var configDbConnector = swsscommon.NewConfigDBConnector()
	defer swsscommon.DeleteConfigDBConnector_Native(configDbConnector.ConfigDBConnector_Native)
	configDbConnector.Connect(false)

	clientCrtCommonNames, err := client.Hget(configDbConnector, "GNMI", "certs", "client_crt_cname");
	if err != nil {
		// config item does not exist, return empty array
		return []string{}
	}

	var clientCrtCommonNameArray = strings.Split(clientCrtCommonNames, ",")
	return clientCrtCommonNameArray
}
