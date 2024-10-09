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
	testcert "github.com/sonic-net/sonic-gnmi/testdata/tls"

	"github.com/fsnotify/fsnotify"
	log "github.com/golang/glog"
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
	EnableCrl             *bool
	CrlExpireDuration     *int
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
		EnableCrl:             fs.Bool("enable_crl", false, "Enable certificate revocation list"),
		CrlExpireDuration:     fs.Int("crl_expire_duration", 86400, "Certificate revocation list cache expire duration"),
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

func iNotifyCertMonitoring(watcher *fsnotify.Watcher, telemetryCfg *TelemetryConfig, serverControlSignal chan<- ServerControlValue, testReadySignal chan<- int, certLoaded *int32) {
	defer watcher.Close()

	done := make(chan bool)

	go func() {
		if testReadySignal != nil { // for testing only
			testReadySignal <- 0
		}
		for {
			select {
			case event := <-watcher.Events:
				if event.Name != "" && (filepath.Ext(event.Name) == ".cert" || filepath.Ext(event.Name) == ".crt" ||
					filepath.Ext(event.Name) == ".cer" || filepath.Ext(event.Name) == ".pem" ||
					filepath.Ext(event.Name) == ".key") {
					log.V(1).Infof("Inotify watcher has received event: %v", event)
					if event.Op&fsnotify.Write == fsnotify.Write || event.Op&fsnotify.Create == fsnotify.Create {
						log.V(1).Infof("Cert File has been modified: %s", event.Name)
						serverControlSignal <- ServerStart // let server know that a write/create event occurred
						done <- true
						return
					}
					if event.Op&fsnotify.Remove == fsnotify.Remove || event.Op&fsnotify.Rename == fsnotify.Rename {
						log.V(1).Infof("Cert file has been deleted: %s", event.Name)
						serverControlSignal <- ServerRestart   // let server know that a remove/rename event occurred
						if atomic.LoadInt32(certLoaded) == 1 { // Should continue monitoring if certs are not present
							done <- true
							return
						}
					}
				}
			case err := <-watcher.Errors:
				if err != nil {
					log.Errorf("Received error event when watching cert: %v", err)
					serverControlSignal <- ServerStop
					done <- true
					return // If watcher is unable to access cert file stop monitoring
				}
			}
		}
	}()

	telemetryCertDirectory := filepath.Dir(*telemetryCfg.ServerCert)

	log.V(1).Infof("Begin cert monitoring on %s", telemetryCertDirectory)

	err := watcher.Add(telemetryCertDirectory) // Adding watcher to cert directory
	if err != nil {
		log.Errorf("Received error when adding watcher to cert directory: %v", err)
		serverControlSignal <- ServerStop
		done <- true
	}

	<-done
	log.V(6).Infof("Closing cert rotation monitoring")
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

	for {
		var opts []grpc.ServerOption
		var certLoaded int32
		atomic.StoreInt32(&certLoaded, 0) // Not loaded

		if !*telemetryCfg.NoTLS {
			var certificate tls.Certificate
			var err error
			if *telemetryCfg.Insecure {
				certificate, err = testcert.NewCert()
				if err != nil {
					log.Errorf("could not load server key pair: %s", err)
					return
				}
			} else {
				watcher, err := fsnotify.NewWatcher()
				if err != nil {
					log.Errorf("Received error when creating fsnotify watcher %v", err)
				}
				if watcher != nil {
					go iNotifyCertMonitoring(watcher, telemetryCfg, serverControlSignal, nil, &certLoaded)
				}
				certificate, err = tls.LoadX509KeyPair(*telemetryCfg.ServerCert, *telemetryCfg.ServerKey)
				if err != nil {
					computeSHA512Checksum(*telemetryCfg.ServerCert)
					computeSHA512Checksum(*telemetryCfg.ServerKey)
					log.Errorf("could not load server key pair: %s", err)
					for {
						serverControlValue := <-serverControlSignal
						if serverControlValue == ServerStop {
							return // server called to shutdown
						}
						if serverControlValue == ServerStart {
							break // retry loading certs after cert has been written or created
						}
						// We don't care if file is deleted here as we will only want to check
						// if certs have been created or written to, else we will wait again
					}
					continue
				}
			}

			tlsCfg := &tls.Config{
				ClientAuth:               tls.RequireAndVerifyClientCert,
				Certificates:             []tls.Certificate{certificate},
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
				// RequestClientCert will ask client for a certificate but won't
				// require it to proceed. If certificate is provided, it will be
				// verified.
				tlsCfg.ClientAuth = tls.RequestClientCert
			}

			if *telemetryCfg.CaCert != "" {
				caCertLoaded := true
				ca, err := ioutil.ReadFile(*telemetryCfg.CaCert)
				if err != nil {
					log.Errorf("could not read CA certificate: %s", err)
					caCertLoaded = false
				}
				certPool := x509.NewCertPool()
				if ok := certPool.AppendCertsFromPEM(ca); !ok {
					log.Errorf("failed to append CA certificate")
					caCertLoaded = false
				}
				if !caCertLoaded {
					for {
						serverControlValue := <-serverControlSignal
						if serverControlValue == ServerStop {
							return // server called to shutdown
						}
						if serverControlValue == ServerStart {
							break // retry loading certs after cert has been written or created
						}
					}
					continue
				}
				tlsCfg.ClientCAs = certPool
			} else {
				if telemetryCfg.UserAuth.Enabled("cert") {
					telemetryCfg.UserAuth.Unset("cert")
					log.Warning("client_auth mode cert requires ca_crt option. Disabling cert mode authentication.")
				}
			}

			atomic.StoreInt32(&certLoaded, 1) // Certs have loaded

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

		serverControlValue := <-serverControlSignal
		log.V(1).Infof("Received signal for gnmi server to close")
		if serverControlValue == ServerStop {
			s.ForceStop() // No graceful stop
			stopSignalHandler <- true
			log.Flush()
			return
		}
		s.Stop() // Graceful stop
		// Both ServerStart and ServerRestart will loop and restart server
		// We use different value to distinguish between write/create and remove/rename
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
