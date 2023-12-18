package main

import (
	"crypto/tls"
	"crypto/x509"
	"crypto/md5"
	"flag"
	"io/ioutil"
	"strconv"
	"time"
	"os"
	"os/signal"
	"syscall"
	"sync"
	log "github.com/golang/glog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"

	gnmi "github.com/sonic-net/sonic-gnmi/gnmi_server"
	testcert "github.com/sonic-net/sonic-gnmi/testdata/tls"
)

var (
	userAuth = gnmi.AuthTypes{"password": false, "cert": false, "jwt": false}
	port = flag.Int("port", -1, "port to listen on")
	// Certificate files.
	caCert            = flag.String("ca_crt", "", "CA certificate for client certificate validation. Optional.")
	serverCert        = flag.String("server_crt", "", "TLS server certificate")
	serverKey         = flag.String("server_key", "", "TLS server private key")
	zmqAddress        = flag.String("zmq_address", "", "Orchagent ZMQ address, when not set or empty string telemetry server will switch to Redis based communication channel.")
	insecure          = flag.Bool("insecure", false, "Skip providing TLS cert and key, for testing only!")
	noTLS             = flag.Bool("noTLS", false, "disable TLS, for testing only!")
	allowNoClientCert = flag.Bool("allow_no_client_auth", false, "When set, telemetry server will request but not require a client certificate.")
	certMonitorEnabled  = flag.Bool("cert_monitor_enabled", false, "When set, telemetry process will monitor certs at path and reload when needed.")
	certPollingInt    = flag.Int("cert_polling_int", 3600, "Rate in seconds in which cert files will be monitored")
	jwtRefInt         = flag.Uint64("jwt_refresh_int", 900, "Seconds before JWT expiry the token can be refreshed.")
	jwtValInt         = flag.Uint64("jwt_valid_int", 3600, "Seconds that JWT token is valid for.")
	gnmi_translib_write = flag.Bool("gnmi_translib_write", gnmi.ENABLE_TRANSLIB_WRITE, "Enable gNMI translib write for management framework")
	gnmi_native_write   = flag.Bool("gnmi_native_write", gnmi.ENABLE_NATIVE_WRITE, "Enable gNMI native write")
	threshold         = flag.Int("threshold", 100, "max number of client connections")
	withMasterArbitration = flag.Bool("with-master-arbitration", false, "Enables master arbitration policy.")
	idle_conn_duration = flag.Int("idle_conn_duration", 5, "Seconds before server closes idle connections")
)

func main() {
	flag.Var(userAuth, "client_auth", "Client auth mode(s) - none,cert,password")
	flag.Parse()

	var defUserAuth gnmi.AuthTypes
	if *gnmi_translib_write {
		//In read/write mode we want to enable auth by default.
		defUserAuth = gnmi.AuthTypes{"password": true, "cert": false, "jwt": true}
	}else {
		defUserAuth = gnmi.AuthTypes{"jwt": false, "password": false, "cert": false}
	}

	if isFlagPassed("client_auth") {
		log.V(1).Infof("client_auth provided")
	}else {
		log.V(1).Infof("client_auth not provided, using defaults.")
		userAuth = defUserAuth
	}

	switch {
	case *port <= 0:
		log.Errorf("port must be > 0.")
		return
	}

	switch {
	case *threshold < 0:
		log.Errorf("threshold must be >= 0.")
		return
	}

	switch {
	case *idle_conn_duration < 0:
		log.Errorf("idle_conn_duration must be >= 0, 0 meaning inf")
		return
	}

	gnmi.JwtRefreshInt = time.Duration(*jwtRefInt*uint64(time.Second))
	gnmi.JwtValidInt = time.Duration(*jwtValInt*uint64(time.Second))

	cfg := &gnmi.Config{}
	cfg.Port = int64(*port)
	cfg.EnableTranslibWrite = bool(*gnmi_translib_write)
	cfg.EnableNativeWrite = bool(*gnmi_native_write)
	cfg.LogLevel = 3
	cfg.ZmqAddress = *zmqAddress
	cfg.Threshold = int(*threshold)
	cfg.IdleConnDuration = int(*idle_conn_duration)

	if val, err := strconv.Atoi(getflag("v")); err == nil {
		cfg.LogLevel = val
		log.Errorf("flag: log level %v", cfg.LogLevel)
	}

	var reload = make(chan int, 1)
	var wg sync.WaitGroup

	wg.Add(1)
	go startGNMIServer(cfg, reload, &wg)

	if (*certMonitorEnabled) {
		wg.Add(1)
		go monitorCerts(reload, &wg)
	}

	wg.Add(1)
	go signalHandler(reload, &wg)

	wg.Wait()
}

func isFlagPassed(name string) bool {
	found := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

func getflag(name string) string {
	val := ""
	flag.VisitAll(func(f *flag.Flag) {
		if f.Name == name {
			val = f.Value.String()
		}
	})
	return val
}

func signalHandler(reload chan<- int, wg *sync.WaitGroup) {
	defer wg.Done()
	sigchannel := make(chan os.Signal, 1)
	signal.Notify(sigchannel,
		syscall.SIGTERM,
		syscall.SIGQUIT)
	<-sigchannel
	reload <- 0
	return
}

func monitorCerts(reload chan<- int, wg *sync.WaitGroup) {
	defer wg.Done()
	prevServerCertInfo, _ := os.Lstat(*serverCert)
	serverCertLastModTime := prevServerCertInfo.ModTime()
	log.V(1).Infof("Last modified time of %s is %d", prevServerCertInfo.Name(), serverCertLastModTime.Unix())

	prevServerKeyInfo, _ := os.Lstat(*serverKey)
	serverKeyLastModTime := prevServerKeyInfo.ModTime()
	log.V(1).Infof("Last modified time of %s is %d", prevServerKeyInfo.Name(), serverCertLastModTime.Unix())

	duration := time.Duration(*certPollingInt) * time.Second

	time.Sleep(duration)

	for {
		needsRotate := false
		currentServerCertInfo, _ := os.Lstat(*serverCert)
		serverCertCurrModTime := currentServerCertInfo.ModTime()
		log.V(1).Infof("Current modified time of %s is %d", currentServerCertInfo.Name(), serverCertCurrModTime.Unix())
		if serverCertLastModTime != serverCertCurrModTime {
			log.V(1).Infof("Modified time of %s has changed from %d to %d. %s needs to be rotated")
			needsRotate = true
		}

		serverCertLastModTime = serverCertCurrModTime

		currentServerKeyInfo, _ := os.Lstat(*serverKey)
		serverKeyCurrModTime := currentServerKeyInfo.ModTime()
		log.V(1).Infof("Current modified time of %s is %d", currentServerKeyInfo.Name(), serverKeyCurrModTime.Unix())
		if serverKeyLastModTime != serverKeyCurrModTime {
			log.V(1).Infof("Modified time of %s has changed from %d to %d. %s needs to be rotated")
			needsRotate = true
		}

		serverKeyLastModTime = serverKeyCurrModTime

		if needsRotate {
			log.V(1).Infof("Server Cert or Key needs to be rotated")
			reload <- 1
		}

		time.Sleep(duration)
	}
}

func startGNMIServer(cfg *gnmi.Config, reload <-chan int, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
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
					currentTime := time.Now().UTC()
					log.Infof("Server Cert md5 checksum: %x at time %s", md5.Sum([]byte(*serverCert)), currentTime.String())
					log.Infof("Server Key md5 checksum: %x at time %s", md5.Sum([]byte(*serverKey)), currentTime.String())
					log.Exitf("could not load server key pair: %s", err)
				}
			}

			tlsCfg := &tls.Config {
				ClientAuth:		  tls.RequireAndVerifyClientCert,
				Certificates:		  []tls.Certificate{certificate},
				MinVersion:		  tls.VersionTLS12,
				CurvePreferences:	  []tls.CurveID{tls.CurveP521, tls.CurveP384, tls.CurveP256},
				PreferServerCipherSuites: true,
				CipherSuites: []uint16 {
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
			} else {
				if userAuth.Enabled("cert") {
					userAuth.Unset("cert")
					log.Warning("client_auth mode cert requires ca_crt option. Disabling cert mode authentication.")
				}
			}

			keep_alive_params := keepalive.ServerParameters{
				MaxConnectionIdle: time.Duration(cfg.IdleConnDuration) * time.Second, // duration in which idle connection will be closed, default is inf
			}

			opts = []grpc.ServerOption{grpc.Creds(credentials.NewTLS(tlsCfg))}

			if cfg.IdleConnDuration > 0 { // non inf case
				opts = append(opts, grpc.KeepaliveParams(keep_alive_params))
			}

			cfg.UserAuth = userAuth

			gnmi.GenerateJwtSecretKey()

		}

		s, err := gnmi.NewServer(cfg, opts)
		if err != nil {
			log.Errorf("Failed to create gNMI server: %v", err)
			return
		}

		if *withMasterArbitration {
			s.ReqFromMaster = gnmi.ReqFromMasterEnabledMA
		}

		log.V(1).Infof("Auth Modes: ", userAuth)
		log.V(1).Infof("Starting RPC server on address: %s", s.Address())

		go func() {
			if err := s.Serve(); err != nil {
				log.Errorf("Serve returns with err: %v", err)
			}
		}()

		value := <-reload
		log.V(1).Infof("Received notification for gnmi server to shutdown and rotate certs")
		s.Stop()
		if value == 0 {
			os.Exit(0)
		}
		log.Flush()
	}
}
