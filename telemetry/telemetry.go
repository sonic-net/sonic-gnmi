package main

import (
	"crypto/tls"
	"crypto/x509"
	"flag"
	"io/ioutil"

	log "github.com/golang/glog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	gnmi "github.com/jipanyang/sonic-telemetry/gnmi_server"
)

var (
	port = flag.Int("port", -1, "port to listen on")
	// Certificate files.
	caCert            = flag.String("ca_crt", "", "CA certificate for client certificate validation. Optional.")
	serverCert        = flag.String("server_crt", "", "TLS server certificate")
	serverKey         = flag.String("server_key", "", "TLS server private key")
	insecure          = flag.Bool("insecure", false, "Skip providing TLS cert and key, for testing only!")
	allowNoClientCert = flag.Bool("allow_no_client_auth", false, "When set, telemetry server will request but not require a client certificate.")
)

func main() {
	flag.Parse()

	switch {
	case *port <= 0:
		log.Errorf("port must be > 0.")
		return
	}
	var certificate tls.Certificate
	var err error

	if *insecure {
		certPEMBlock := []byte(`-----BEGIN CERTIFICATE-----
MIICWDCCAcGgAwIBAgIJAISaMNtAwNWSMA0GCSqGSIb3DQEBCwUAMEUxCzAJBgNV
BAYTAkFVMRMwEQYDVQQIDApTb21lLVN0YXRlMSEwHwYDVQQKDBhJbnRlcm5ldCBX
aWRnaXRzIFB0eSBMdGQwHhcNMTgwMTE1MjExOTUzWhcNMTkwMTE1MjExOTUzWjBF
MQswCQYDVQQGEwJBVTETMBEGA1UECAwKU29tZS1TdGF0ZTEhMB8GA1UECgwYSW50
ZXJuZXQgV2lkZ2l0cyBQdHkgTHRkMIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKB
gQCrFBy+xrT4gmeMqPDZjpfL2KqI7XiyvYEq/MKTgJM172FpmV3A/nI5O+O7pWub
ONOGOiU4HfUxWaFymyJNye4niBxrrxb/m8TdOc5eIqmtSyJymKir0IIu9vd6ZfK9
vQy7HqzezXSmJTt/s1ZfPF++tnTUPUI0B5RpKvIb8zKSVQIDAQABo1AwTjAdBgNV
HQ4EFgQUdRx4RR0QEcf+xYUlPJv8hrVuaBQwHwYDVR0jBBgwFoAUdRx4RR0QEcf+
xYUlPJv8hrVuaBQwDAYDVR0TBAUwAwEB/zANBgkqhkiG9w0BAQsFAAOBgQAHqRYo
jv3/iIsbtARagsh9i8GVYDPk/9M7Fy2Op/XjqOQmKut74FSe3gFXflmAAnfB3FOK
lxb4K8KkdohDGsxQ79UceBH6JwfDfTcZ4EWpI8aR9HzIQZcRNF/cTL92LWAogUYY
WVNSEMeoWhYbLM0YOYdGgz8FoXOWVaBcgj668g==
-----END CERTIFICATE-----`)

		keyPEMBlock := []byte(`-----BEGIN RSA PRIVATE KEY-----
MIICWwIBAAKBgQCrFBy+xrT4gmeMqPDZjpfL2KqI7XiyvYEq/MKTgJM172FpmV3A
/nI5O+O7pWubONOGOiU4HfUxWaFymyJNye4niBxrrxb/m8TdOc5eIqmtSyJymKir
0IIu9vd6ZfK9vQy7HqzezXSmJTt/s1ZfPF++tnTUPUI0B5RpKvIb8zKSVQIDAQAB
AoGAe8R4K1jskiEdsviCDpMHpLUiYx+SQ5Wv/h6Q0k+hsNJ3IgOPfVFX56o5TocV
e124QhKM3LVnrwVONPCg97AQN6CESk6HBC/y8XJi1f9Pz6RYHEjc1rXxgrppjdun
Wku5eWWhoJ51d2AlWtcT32gIWNB6TQqHw/fKE+kkudCW06ECQQDSgBjeAeOMy85l
E929wmdqwGjBfx4XKKhzuTPSwRQpZ5XLWZ4GsVHCDYAjI3W5p8C51b1x7t6LFBxn
CJafqYC5AkEA0A6iZh/udKXICyWtRHhww0a9w3shJj+OaC4dKogXHPqpBrPlFuDX
7GPGqZaWUVfGvG7lnMMwmax2fupq1dh4fQJASFX+taPeh06uEWv/QithEH0oQn4l
X/33zTSyi1UQUZ4oCqY0OMaMeuvawbh4xyDPiMzbeiCE1zRFAl8gK6O6+QJAFkRa
sR9dv/I2NKs1ngxd1ShvCsrUw2kt7oxw5qpl/t381RDPxeEOeug6zM+nCtGgHW6o
+FwTiX7ht7eS84wVaQJAOdKIf1gMjiEIBMKdmku4Pwj1jCeeyc3BTd9NsHIGgz9O
n6Mu9QKR+diUnqGtENDrD0NDJv8pyT1qXa0lXwF/Nw==
-----END RSA PRIVATE KEY-----`)
		certificate, err = tls.X509KeyPair(certPEMBlock, keyPEMBlock)
		if err != nil {
			log.Exitf("could not load server key pair: %s", err)
		}
	} else {
		switch {
		case *serverCert == "":
			log.Errorf("serverCert must be set.")
			return
		case *serverKey == "":
			log.Errorf("serverKey must be set.")
			return
		}
		certificate, err = tls.LoadX509KeyPair(*serverCert, *serverKey)
		if err != nil {
			log.Exitf("could not load server key pair: %s", err)
		}
	}

	tlsCfg := &tls.Config{
		ClientAuth:   tls.RequireAndVerifyClientCert,
		Certificates: []tls.Certificate{certificate},
	}
	if *allowNoClientCert {
		// RequestClientCert will ask client for a certificate but won't
		// require it to proceed. If certificate is provided, it will be
		// verified.
		tlsCfg.ClientAuth = tls.RequestClientCert
	}

	if *caCert != "" {
		ca, err := ioutil.ReadFile(*caCert)
		if err != nil {
			log.Exitf("could not read CA certificate: %s", err)
		}
		certPool := x509.NewCertPool()
		if ok := certPool.AppendCertsFromPEM(ca); !ok {
			log.Exit("failed to append CA certificate")
		}
		tlsCfg.ClientCAs = certPool
	}

	opts := []grpc.ServerOption{grpc.Creds(credentials.NewTLS(tlsCfg))}
	cfg := &gnmi.Config{}
	cfg.Port = int64(*port)
	s, err := gnmi.NewServer(cfg, opts)
	if err != nil {
		log.Errorf("Failed to create gNMI server: %v", err)
		return
	}

	log.Infof("Starting RPC server on address: %s", s.Address())
	s.Serve() // blocks until close
}
