package main

import (
	"crypto/sha512"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	gnmi "github.com/sonic-net/sonic-gnmi/gnmi_server"
	"github.com/sonic-net/sonic-gnmi/pkg/interceptors"
	testcert "github.com/sonic-net/sonic-gnmi/testdata/tls"

	"github.com/fsnotify/fsnotify"
	log "github.com/golang/glog"
	"github.com/sonic-net/sonic-gnmi/swsscommon"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
)

type ServerControlValue int

const (
	ServerStop    ServerControlValue = iota // 0
	ServerStart   ServerControlValue = iota // 1
	ServerRestart ServerControlValue = iota // 2
)


type CertCache struct {
	cert       *tls.Certificate
	caPool     *x509.CertPool
	caPath     string
	mu         sync.RWMutex
	hasWatcher atomic.Bool
}

func (c *CertCache) Get() *tls.Certificate {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cert
}

func (c *CertCache) Set(cert *tls.Certificate) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cert = cert
}

func (c *CertCache) GetCAPool() *x509.CertPool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.caPool
}

func (c *CertCache) SetCAPool(pool *x509.CertPool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.caPool = pool
}

func loadAndValidateCert(certPath, keyPath string) (*tls.Certificate, error) {
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load cert/key pair: %v", err)
	}

	// Parse the leaf certificate to check expiration
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("failed to parse certificate: %v", err)
	}
	cert.Leaf = leaf

	if time.Now().After(leaf.NotAfter) {
		return nil, fmt.Errorf("certificate expired at %v", leaf.NotAfter)
	}

	if time.Now().Before(leaf.NotBefore) {
		return nil, fmt.Errorf("certificate not valid until %v", leaf.NotBefore)
	}

	return &cert, nil
}


func loadAndValidateCA(caPath string) (*x509.CertPool, error) {
	ca, err := ioutil.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA certificate: %v", err)
	}

	certPool := x509.NewCertPool()
	if ok := certPool.AppendCertsFromPEM(ca); !ok {
		return nil, fmt.Errorf("failed to parse CA certificate")
	}

	return certPool, nil
}

type TelemetryConfig struct {
	UserAuth              gnmi.AuthTypes
	Port                  *int
	LogLevel              *int
	CaCert                *string
	ServerCert            *string
	ServerKey             *string
	ConfigTableName       *string
	ZmqAddress            *string
	ZmqPort               *string
	Insecure              *bool
	NoTLS                 *bool
	AllowNoClientCert     *bool
	JwtRefInt             *uint64
	JwtValInt             *uint64
	GnmiTranslibWrite     *bool
	GnmiNativeWrite       *bool
	Threshold             *int
	WithMasterArbitration *bool
	WithSaveOnSet         *bool
	IdleConnDuration      *int
	Vrf                   *string
	EnableCrl             *bool
	CrlExpireDuration     *int
	ImgDirPath            *string
}

func main() {
	err := runTelemetry(os.Args)
	if err != nil {
		log.Errorf("Unable to setup telemetry config due to err: %v", err)
	}
}

func runTelemetry(args []string) error {
	/* Glog flags like -logtostderr have to be part of the global flagset.
	   Because we use a custom flagset to avoid the use of global var and improve
	   testability, we have to parse cmd line args two different times such that
	   in the first parse, cmd line args will contain global flags and flag.Parse() will be called.
	   The second parse, cmd line args will contain flags only relevant to telemetry, and our custom flagset will
	   call Parse().
	*/
	glogFlags, telemetryFlags := parseOSArgs()
	os.Args = glogFlags
	flag.Parse() // glog flags will be populated after global flag parse

	os.Args = telemetryFlags
	fs := flag.NewFlagSet(args[0], flag.ExitOnError)
	telemetryCfg, cfg, err := setupFlags(fs) // telemetry flags will be populated after second parse
	if err != nil {
		return err
	}

	// enable swss-common debug level
	swsscommon.LoggerLinkToDbNative("telemetry")

	var wg sync.WaitGroup
	// serverControlSignal channel is a channel that will be used to notify gnmi server to start, stop, restart, depending of syscall or cert updates
	var serverControlSignal = make(chan ServerControlValue, 1)
	var stopSignalHandler = make(chan bool, 1)
	sigchannel := make(chan os.Signal, 1)
	signal.Notify(sigchannel, syscall.SIGTERM, syscall.SIGQUIT, syscall.SIGINT, syscall.SIGHUP)

	wg.Add(1)

	go signalHandler(serverControlSignal, sigchannel, stopSignalHandler, &wg)

	wg.Add(1)

	go startGNMIServer(telemetryCfg, cfg, serverControlSignal, stopSignalHandler, &wg)

	wg.Wait()
	return nil
}

func getGlogFlagsMap() map[string]bool {
	// glog flags: https://pkg.go.dev/github.com/golang/glog
	return map[string]bool{
		"-alsologtostderr":  true,
		"-log_backtrace_at": true,
		"-log_dir":          true,
		"-log_link":         true,
		"-logbuflevel":      true,
		"-logtostderr":      true,
		"-stderrthreshold":  true,
		"-v":                true,
		"-vmodule":          true,
	}
}

func parseOSArgs() ([]string, []string) {
	glogFlags := []string{os.Args[0]}
	telemetryFlags := []string{os.Args[0]}
	glogFlagsMap := getGlogFlagsMap()

	for _, arg := range os.Args[1:] {
		if strings.HasPrefix(arg, "-v") { // only flag in both glog and telemetry
			glogFlags = append(glogFlags, arg)
			telemetryFlags = append(telemetryFlags, arg)
			continue
		}
		isGlogFlag := false
		if glogFlagsMap[arg] {
			glogFlags = append(glogFlags, arg)
			isGlogFlag = true
			continue
		}
		if !isGlogFlag {
			telemetryFlags = append(telemetryFlags, arg)
		}
	}
	return glogFlags, telemetryFlags
}

func setupFlags(fs *flag.FlagSet) (*TelemetryConfig, *gnmi.Config, error) {
	telemetryCfg := &TelemetryConfig{
		UserAuth:              gnmi.AuthTypes{"password": false, "cert": false, "jwt": false},
		Port:                  fs.Int("port", -1, "port to listen on"),
		LogLevel:              fs.Int("v", 2, "log level of process"),
		CaCert:                fs.String("ca_crt", "", "CA certificate for client certificate validation. Optional."),
		ServerCert:            fs.String("server_crt", "", "TLS server certificate"),
		ServerKey:             fs.String("server_key", "", "TLS server private key"),
		ConfigTableName:       fs.String("config_table_name", "", "Config table name"),
		ZmqAddress:            fs.String("zmq_address", "", "Orchagent ZMQ address, deprecated, please use zmq_port."),
		ZmqPort:               fs.String("zmq_port", "", "Orchagent ZMQ port, when not set or empty string telemetry server will switch to Redis based communication channel."),
		Insecure:              fs.Bool("insecure", false, "Skip providing TLS cert and key, for testing only!"),
		NoTLS:                 fs.Bool("noTLS", false, "disable TLS, for testing only!"),
		AllowNoClientCert:     fs.Bool("allow_no_client_auth", false, "When set, telemetry server will request but not require a client certificate."),
		JwtRefInt:             fs.Uint64("jwt_refresh_int", 900, "Seconds before JWT expiry the token can be refreshed."),
		JwtValInt:             fs.Uint64("jwt_valid_int", 3600, "Seconds that JWT token is valid for."),
		GnmiTranslibWrite:     fs.Bool("gnmi_translib_write", gnmi.ENABLE_TRANSLIB_WRITE, "Enable gNMI translib write for management framework"),
		GnmiNativeWrite:       fs.Bool("gnmi_native_write", gnmi.ENABLE_NATIVE_WRITE, "Enable gNMI native write"),
		Threshold:             fs.Int("threshold", 100, "max number of client connections"),
		WithMasterArbitration: fs.Bool("with-master-arbitration", false, "Enables master arbitration policy."),
		WithSaveOnSet:         fs.Bool("with-save-on-set", false, "Enables save-on-set."),
		IdleConnDuration:      fs.Int("idle_conn_duration", 5, "Seconds before server closes idle connections"),
		Vrf:                   fs.String("vrf", "", "VRF name, when zmq_address belong on a VRF, need VRF name to bind ZMQ."),
		EnableCrl:             fs.Bool("enable_crl", false, "Enable certificate revocation list"),
		CrlExpireDuration:     fs.Int("crl_expire_duration", 86400, "Certificate revocation list cache expire duration"),
		ImgDirPath:            fs.String("img_dir", "/tmp/host_tmp", "Directory path where image will be transferred."),
	}

	fs.Var(&telemetryCfg.UserAuth, "client_auth", "Client auth mode(s) - none,cert,password")
	fs.Parse(os.Args[1:])

	var defUserAuth gnmi.AuthTypes
	if *telemetryCfg.GnmiTranslibWrite {
		//In read/write mode we want to enable auth by default.
		defUserAuth = gnmi.AuthTypes{"password": true, "cert": false, "jwt": true}
	} else {
		defUserAuth = gnmi.AuthTypes{"jwt": false, "password": false, "cert": false}
	}

	if isFlagPassed(fs, "client_auth") {
		log.V(1).Infof("client_auth provided")
	} else {
		log.V(1).Infof("client_auth not provided, using defaults.")
		telemetryCfg.UserAuth = defUserAuth
	}

	switch {
	case *telemetryCfg.Port <= 0:
		return nil, nil, fmt.Errorf("port must be > 0.")
	}

	switch {
	case *telemetryCfg.Threshold < 0:
		return nil, nil, fmt.Errorf("threshold must be >= 0.")
	}

	switch {
	case *telemetryCfg.IdleConnDuration < 0:
		return nil, nil, fmt.Errorf("idle_conn_duration must be >= 0, 0 meaning inf")
	}

	switch {
	case *telemetryCfg.LogLevel < 0:
		*telemetryCfg.LogLevel = 2
		log.Infof("Log level must be greater than 0, setting to default value of 2")
	}

	if !*telemetryCfg.NoTLS && !*telemetryCfg.Insecure {
		switch {
		case *telemetryCfg.ServerCert == "":
			return nil, nil, fmt.Errorf("serverCert must be set.")
		case *telemetryCfg.ServerKey == "":
			return nil, nil, fmt.Errorf("serverKey must be set.")
		}
	}

	// Move to new function
	gnmi.JwtRefreshInt = time.Duration(*telemetryCfg.JwtRefInt * uint64(time.Second))
	gnmi.JwtValidInt = time.Duration(*telemetryCfg.JwtValInt * uint64(time.Second))

	cfg := &gnmi.Config{}
	cfg.Port = int64(*telemetryCfg.Port)
	cfg.EnableTranslibWrite = bool(*telemetryCfg.GnmiTranslibWrite)
	cfg.EnableNativeWrite = bool(*telemetryCfg.GnmiNativeWrite)
	cfg.LogLevel = int(*telemetryCfg.LogLevel)
	cfg.Threshold = int(*telemetryCfg.Threshold)
	cfg.IdleConnDuration = int(*telemetryCfg.IdleConnDuration)
	cfg.ConfigTableName = *telemetryCfg.ConfigTableName
	cfg.Vrf = *telemetryCfg.Vrf
	cfg.EnableCrl = *telemetryCfg.EnableCrl

	gnmi.SetCrlExpireDuration(time.Duration(*telemetryCfg.CrlExpireDuration) * time.Second)

	// TODO: After other dependent projects are migrated to ZmqPort, remove ZmqAddress
	zmqAddress := *telemetryCfg.ZmqAddress
	zmqPort := *telemetryCfg.ZmqPort
	if zmqPort == "" {
		if zmqAddress != "" {
			// ZMQ address format: "tcp://127.0.0.1:1234"
			zmqPort = strings.Split(zmqAddress, ":")[2]
		}
	}

	cfg.ZmqPort = zmqPort

	// Populate the OS-related fields directly on the gnmi.Config struct.
	cfg.ImgDir = *telemetryCfg.ImgDirPath

	return telemetryCfg, cfg, nil
}

func isFlagPassed(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

func iNotifyCertMonitoring(watcher *fsnotify.Watcher, telemetryCfg *TelemetryConfig, certCache *CertCache, stopChan <-chan struct{}, testReadySignal chan<- int) {
	defer watcher.Close()

	telemetryCertDirectory := filepath.Dir(*telemetryCfg.ServerCert)

	log.V(1).Infof("Begin cert monitoring on %s", telemetryCertDirectory)

	if testReadySignal != nil { // for testing only
		testReadySignal <- 0
	}

	for {
		select {
		case <-stopChan:
			log.V(1).Infof("Cert monitoring stopped")
			return
		case event := <-watcher.Events:
			if event.Name != "" && (filepath.Ext(event.Name) == ".cert" || filepath.Ext(event.Name) == ".crt" ||
				filepath.Ext(event.Name) == ".cer" || filepath.Ext(event.Name) == ".pem" ||
				filepath.Ext(event.Name) == ".key") {
				log.V(1).Infof("Inotify watcher has received event: %v", event)
				if event.Op&(fsnotify.CloseWrite|fsnotify.MovedTo|fsnotify.Create) != 0 {
					log.V(1).Infof("Cert File has been modified: %s", event.Name)

					// Validate and load cert/key pair before updating cache
					cert, err := loadAndValidateCert(*telemetryCfg.ServerCert, *telemetryCfg.ServerKey)
					if err != nil {
						log.V(1).Infof("Cert validation failed: %v", err)
						continue // Keep monitoring - wait for valid cert/key pair
					}

					// Update the cert cache with the new valid cert
					certCache.Set(cert)
					log.V(1).Infof("Cert cache updated with new certificate (expires: %v)", cert.Leaf.NotAfter)

					// Reload CA cert if configured
					if certCache.caPath != "" {
						caPool, err := loadAndValidateCA(certCache.caPath)
						if err != nil {
							log.V(1).Infof("CA cert validation failed (keeping cached CA): %v", err)
						} else {
							certCache.SetCAPool(caPool)
							log.V(1).Infof("CA cert cache updated")
						}
					}
				}
				if event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
					log.V(1).Infof("Cert file has been deleted/renamed: %s (keeping cached cert)", event.Name)
				}
			}
		case err := <-watcher.Errors:
			if err != nil {
				log.Errorf("Received error event when watching cert: %v", err)
			}
		}
	}
}

func signalHandler(serverControlSignal chan<- ServerControlValue, sigchannel <-chan os.Signal, stopSignalHandler <-chan bool, wg *sync.WaitGroup) {
	defer wg.Done()
	select {
	case <-sigchannel:
		log.V(6).Infof("Sending signal stop to server because of syscall received")
		serverControlSignal <- ServerStop
		return
	case <-stopSignalHandler:
		return
	}
}

func startGNMIServer(telemetryCfg *TelemetryConfig, cfg *gnmi.Config, serverControlSignal chan ServerControlValue, stopSignalHandler chan<- bool, wg *sync.WaitGroup) {
	defer wg.Done()

	var currentServerChain *interceptors.ServerChain
	defer func() {
		// Cleanup on function exit (ServerStop)
		if currentServerChain != nil {
			currentServerChain.Close()
		}
	}()

	var opts []grpc.ServerOption

	certCache := &CertCache{}

	// Channel to stop cert monitoring goroutine on shutdown
	stopCertMonitor := make(chan struct{})
	defer close(stopCertMonitor)

	if !*telemetryCfg.NoTLS {
		var err error
		if *telemetryCfg.Insecure {
			_, err = testcert.NewCert()
			if err != nil {
				log.Errorf("could not load server key pair: %s", err)
				return
			}
		} else {
			// Try to create fsnotify watcher for cert rotation monitoring
			watcher, err := fsnotify.NewWatcher()
			if err != nil {
				log.Errorf("Failed to create fsnotify watcher: %v", err)
				certCache.hasWatcher.Store(false)
			} else {
				// Try to add watcher to cert directory
				telemetryCertDirectory := filepath.Dir(*telemetryCfg.ServerCert)
				err = watcher.Add(telemetryCertDirectory)
				if err != nil {
					log.Errorf("Failed to add watcher to cert directory %s: %v", telemetryCertDirectory, err)
					watcher.Close()
					certCache.hasWatcher.Store(false)
				} else {
					certCache.hasWatcher.Store(true)
					go iNotifyCertMonitoring(watcher, telemetryCfg, certCache, stopCertMonitor, nil)
				}
			}

			// Try to load initial certificates
			cert, err := loadAndValidateCert(*telemetryCfg.ServerCert, *telemetryCfg.ServerKey)
			if err != nil {
				computeSHA512Checksum(*telemetryCfg.ServerCert)
				computeSHA512Checksum(*telemetryCfg.ServerKey)
				log.Errorf("could not load server key pair: %s", err)

				// No certs + No watcher, fatal exit
				if !certCache.hasWatcher.Load() {
					log.Fatalf("Certificate files not found and cert monitoring is disabled. Cannot start server. Exiting.")
					return
				}
				// No certs + Has watcher → Start server, wait for watcher to populate cache
				log.V(2).Infof("No valid certificates found at startup. Server will start but connections will fail until certificates are available.")
			} else {
				// Certs loaded successfully, populate cache
				certCache.Set(cert)
				log.V(2).Infof("Initial certificates loaded and cached (expires: %v)", cert.Leaf.NotAfter)
				if !certCache.hasWatcher.Load() {
					log.Warning("Certificate rotation monitoring is disabled")
				} else {
					log.V(2).Infof("Certificate rotation monitoring enabled")
				}
			}
		}

		tlsCfg := &tls.Config{
			ClientAuth: tls.RequireAndVerifyClientCert,
			// GetCertificate reads from cache
			GetCertificate: func(clientHello *tls.ClientHelloInfo) (*tls.Certificate, error) {
				cert := certCache.Get()
				if cert == nil {
					if !certCache.hasWatcher.Load() {
						log.Fatalf("No certificate available and cert monitoring is disabled. Cannot serve connections.")
					}
					return nil, fmt.Errorf("no certificate available in cache")
				}
				// Check if cert is expired
				if cert.Leaf != nil && time.Now().After(cert.Leaf.NotAfter) {
					if !certCache.hasWatcher.Load() {
						log.Fatalf("Cached certificate expired and cert monitoring is disabled. Cannot serve connections.")
					}
					return nil, fmt.Errorf("cached certificate expired at %v", cert.Leaf.NotAfter)
				}
				return cert, nil
			},
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


		if *telemetryCfg.AllowNoClientCert {
			tlsCfg.ClientAuth = tls.RequestClientCert
		}

		if *telemetryCfg.CaCert != "" {
			certCache.caPath = *telemetryCfg.CaCert

			caPool, err := loadAndValidateCA(*telemetryCfg.CaCert)
			if err != nil {
				log.Errorf("could not load CA certificate: %s", err)
				// No CA cert + No watcher, fatal exit
				if !certCache.hasWatcher.Load() {
					log.Fatalf("CA certificate file not found and cert monitoring is disabled. Cannot start server. Exiting.")
					return
				}
				log.V(2).Infof("CA certificate not found.")
			} else {
				certCache.SetCAPool(caPool)
				log.V(2).Infof("CA certificate loaded and cached")
			}

			tlsCfg.VerifyPeerCertificate = func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
				if len(rawCerts) == 0 {
					return nil
				}
				// Client provided a cert, verify it against CA pool
				caPool := certCache.GetCAPool()
				if caPool == nil {
					if !certCache.hasWatcher.Load() {
						log.Fatalf("No CA certificate available and cert monitoring is disabled. Cannot verify client certs.")
					}
					return fmt.Errorf("no CA certificate available for client verification")
				}

				// Parse the client certificate
				cert, err := x509.ParseCertificate(rawCerts[0])
				if err != nil {
					return fmt.Errorf("failed to parse client certificate: %v", err)
				}

				// Create intermediates pool
				intermediates := x509.NewCertPool()
				for _, rawCert := range rawCerts[1:] {
					intermediateCert, err := x509.ParseCertificate(rawCert)
					if err != nil {
						continue
					}
					intermediates.AddCert(intermediateCert)
				}

				// Verify the client certificate against CA pool
				opts := x509.VerifyOptions{
					Roots:         caPool,
					Intermediates: intermediates,
					KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
				}
				_, err = cert.Verify(opts)
				if err != nil {
					return fmt.Errorf("client certificate verification failed: %v", err)
				}

				return nil
			}
		} else {
			if telemetryCfg.UserAuth.Enabled("cert") {
				telemetryCfg.UserAuth.Unset("cert")
				log.Warning("client_auth mode cert requires ca_crt option. Disabling cert mode authentication.")
			}
		}

		keep_alive_params := keepalive.ServerParameters{
			MaxConnectionIdle: time.Duration(*telemetryCfg.IdleConnDuration) * time.Second, // duration in which idle connection will be closed, default is inf
		}

		opts = []grpc.ServerOption{grpc.Creds(credentials.NewTLS(tlsCfg))}

		if *telemetryCfg.IdleConnDuration > 0 { // non inf case
			opts = append(opts, grpc.KeepaliveParams(keep_alive_params))
		}

		cfg.UserAuth = telemetryCfg.UserAuth

		gnmi.GenerateJwtSecretKey()
	}

	// Setup interceptor chain (includes DPU proxy with Redis-based routing)
	var err error
	currentServerChain, err = interceptors.NewServerChain()
	if err != nil {
		log.Errorf("Failed to create interceptor chain: %v", err)
		return
	}

	opts = append(opts, currentServerChain.GetServerOptions()...)

	s, err := gnmi.NewServer(cfg, opts)
	if err != nil {
		log.Errorf("Failed to create gNMI server: %v", err)
		return
	}
	if *telemetryCfg.WithSaveOnSet {
		s.SaveStartupConfig = gnmi.SaveOnSetEnabled
	}

	if *telemetryCfg.WithMasterArbitration {
		s.ReqFromMaster = gnmi.ReqFromMasterEnabledMA
	}

	log.V(1).Infof("Auth Modes: %v", telemetryCfg.UserAuth)
	log.V(1).Infof("Starting RPC server on address: %s", s.Address())

	go func() {
		log.V(1).Infof("GNMI Server started serving")
		if err := s.Serve(); err != nil {
			log.Errorf("Serve returned with err: %v", err)
		}
	}()

	for {
		serverControlValue := <-serverControlSignal
		if serverControlValue == ServerStop {
			log.V(1).Infof("Received signal to stop gnmi server")
			s.ForceStop()
			stopSignalHandler <- true
			log.Flush()
			return
		}
	}
}

func computeSHA512Checksum(file string) {
	currentTime := time.Now().UTC()
	f, err := os.Open(file)
	if err != nil {
		log.Errorf("Unable to open %s, got err %s", file, err)
	}
	defer f.Close()

	hasher := sha512.New()
	if _, err := io.Copy(hasher, f); err != nil {
		log.Errorf("Unable to create hash for %s, got err %s", file, err)
	}
	hash := hasher.Sum(nil)
	log.V(1).Infof("SHA512 hash of %s: %s at time %s", file, hex.EncodeToString(hash), currentTime.String())
}
