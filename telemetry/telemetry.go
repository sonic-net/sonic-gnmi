package main

import (
	"crypto/tls"
	"crypto/x509"
	"flag"
	"io/ioutil"

	log "github.com/golang/glog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	gnmi "github.com/Azure/sonic-telemetry/gnmi_server"
	testcert "github.com/Azure/sonic-telemetry/testdata/tls"
)

var (
	port = flag.Int("port", -1, "port to listen on")
	// Certificate files.
	caCert            = flag.String("ca_crt", "", "CA certificate for client certificate validation. Optional.")
	serverCert        = flag.String("server_crt", "", "TLS server certificate")
	serverKey         = flag.String("server_key", "", "TLS server private key")
	insecure          = flag.Bool("insecure", false, "Skip providing TLS cert and key, for testing only!")
	noTLS             = flag.Bool("noTLS", false, "disable TLS, for testing only!")
	allowNoClientCert = flag.Bool("allow_no_client_auth", false, "When set, telemetry server will request but not require a client certificate.")
)

func main() {
	flag.Parse()

	switch {
	case *port <= 0:
		log.Errorf("port must be > 0.")
		return
	}

	cfg := &gnmi.Config{}
	cfg.Port = int64(*port)
	var opts []grpc.ServerOption

	if !*noTLS {
		var certificate tls.Certificate
		var err error
		if *insecure {
			certificate, err = testcert.NewCert()
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
		MinVersion:               tls.VersionTLS12,
		CurvePreferences:         []tls.CurveID{tls.CurveP521, tls.CurveP384, tls.CurveP256},
		PreferServerCipherSuites: true,
		CipherSuites: []uint16{
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
		},
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

	opts = []grpc.ServerOption{grpc.Creds(credentials.NewTLS(tlsCfg))}
}

	s, err := gnmi.NewServer(cfg, opts)
	if err != nil {
		log.Errorf("Failed to create gNMI server: %v", err)
		return
	}

	log.V(1).Infof("Starting RPC server on address: %s", s.Address())
	s.Serve() // blocks until close
	log.Flush()
}
