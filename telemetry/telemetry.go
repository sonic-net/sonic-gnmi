package main

import (
	"crypto/tls"
	"crypto/x509"
	"crypto/sha512"
	"encoding/hex"
	"flag"
	"io"
	"io/ioutil"
	"path/filepath"
	"time"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"sync"
	log "github.com/golang/glog"
	"github.com/fsnotify/fsnotify"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"fmt"
	gnmi "github.com/sonic-net/sonic-gnmi/gnmi_server"
	testcert "github.com/sonic-net/sonic-gnmi/testdata/tls"
)

type ServerControlValue int

const (
	ServerStop       ServerControlValue = iota // 0
	ServerRestart    ServerControlValue = iota // 1
)

type TelemetryConfig struct {
	UserAuth              gnmi.AuthTypes
	Port                  *int
	LogLevel              *int
	CaCert                *string
	ServerCert            *string
	ServerKey             *string
	ZmqAddress            *string
	Insecure              *bool
	NoTLS                 *bool
	AllowNoClientCert     *bool
	JwtRefInt             *uint64
	JwtValInt             *uint64
	GnmiTranslibWrite     *bool
	GnmiNativeWrite       *bool
	Threshold             *int
	WithMasterArbitration *bool
	IdleConnDuration      *int
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
	// serverControlSignal channel is a channel that will be used to notify gnmi server of sigcall which should stop server
	var serverControlSignal = make(chan int, 1)
	sigchannel := make(chan os.Signal, 1)
	signal.Notify(sigchannel, syscall.SIGTERM, syscall.SIGQUIT)

	wg.Add(1)
	go startGNMIServer(telemetryCfg, cfg, serverControlSignal, &wg)

	wg.Add(1)
	go signalHandler(serverControlSignal, &wg, sigchannel)

	wg.Wait()
	return nil
}

func getGlogFlagsMap() map[string] bool {
	// glog flags: https://pkg.go.dev/github.com/golang/glog
	return map[string]bool {
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
	telemetryCfg := &TelemetryConfig {
		UserAuth:              gnmi.AuthTypes{"password": false, "cert": false, "jwt": false},
		Port:                  fs.Int("port", -1, "port to listen on"),
		LogLevel:              fs.Int("v", 2, "log level of process"),
		CaCert:                fs.String("ca_crt", "", "CA certificate for client certificate validation. Optional."),
		ServerCert:            fs.String("server_crt", "", "TLS server certificate"),
		ServerKey:             fs.String("server_key", "", "TLS server private key"),
		ZmqAddress:            fs.String("zmq_address", "", "Orchagent ZMQ address, when not set or empty string telemetry server will switch to Redis based communication channel."),
		Insecure:              fs.Bool("insecure", false, "Skip providing TLS cert and key, for testing only!"),
		NoTLS:                 fs.Bool("noTLS", false, "disable TLS, for testing only!"),
		AllowNoClientCert:     fs.Bool("allow_no_client_auth", false, "When set, telemetry server will request but not require a client certificate."),
		JwtRefInt:             fs.Uint64("jwt_refresh_int", 900, "Seconds before JWT expiry the token can be refreshed."),
		JwtValInt:             fs.Uint64("jwt_valid_int", 3600, "Seconds that JWT token is valid for."),
		GnmiTranslibWrite:     fs.Bool("gnmi_translib_write", gnmi.ENABLE_TRANSLIB_WRITE, "Enable gNMI translib write for management framework"),
		GnmiNativeWrite:       fs.Bool("gnmi_native_write", gnmi.ENABLE_NATIVE_WRITE, "Enable gNMI native write"),
		Threshold:             fs.Int("threshold", 100, "max number of client connections"),
		WithMasterArbitration: fs.Bool("with-master-arbitration", false, "Enables master arbitration policy."),
		IdleConnDuration:      fs.Int("idle_conn_duration", 5, "Seconds before server closes idle connections"),
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


	// Move to new function
	gnmi.JwtRefreshInt = time.Duration(*telemetryCfg.JwtRefInt * uint64(time.Second))
	gnmi.JwtValidInt = time.Duration(*telemetryCfg.JwtValInt * uint64(time.Second))

	cfg := &gnmi.Config{}
	cfg.Port = int64(*telemetryCfg.Port)
	cfg.EnableTranslibWrite = bool(*telemetryCfg.GnmiTranslibWrite)
	cfg.EnableNativeWrite = bool(*telemetryCfg.GnmiNativeWrite)
	cfg.LogLevel = int(*telemetryCfg.LogLevel)
	cfg.ZmqAddress = *telemetryCfg.ZmqAddress
	cfg.Threshold = int(*telemetryCfg.Threshold)
	cfg.IdleConnDuration = int(*telemetryCfg.IdleConnDuration)

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

func iNotifyCertMonitoring(watcher *fsnotify.Watcher, telemetryCfg *TelemetryConfig, serverControlSignal chan<- int, testReadySignal chan<- int) {
	defer watcher.Close()

	done := make(chan bool)
	/* Creating a timer for remove event edge case where cert file is deleted and new version of cert file is created immediately after.
	   In this case a remove event will be sent and shortly after a write event will be sent. Adding a timer will let us check for the write event
	   after a remove event. If there is no such write event after, we will proceed with handling the remove event by sending a StopServer value to channel
           and returning. If we do see a write event, we will treat as final state of write.
	*/
	removeEventTimer := time.NewTimer(time.Second)
	removeEventTimer.Stop()

	go func() {
		if testReadySignal != nil { // for testing only
			testReadySignal <- 0
		}
		for {
			select {
			case event := <-watcher.Events:
				if event.Name != "" {
					log.V(6).Infof("Inotify watcher has received event: %v", event)
					if event.Op & fsnotify.Write == fsnotify.Write {
						log.V(1).Infof("Cert File has been modified: %s", event.Name)
						removeEventTimer.Stop()
						serverControlSignal <- ServerRestart
						done <- true
						return
					}
					if event.Op & fsnotify.Remove == fsnotify.Remove {
						log.Errorf("Cert file has been deleted: %s", event.Name)
						// Start timer
						removeEventTimer.Reset(time.Second)
					}
				}
			case <-removeEventTimer.C:
				// No write event after a remove event, we will treat as remove final state
				serverControlSignal <- ServerStop
				done <- true
				return
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
}

func signalHandler(serverControlSignal chan int, wg *sync.WaitGroup, sigchannel <-chan os.Signal) {
	defer wg.Done()
	select {
	case <-sigchannel:
		serverControlSignal <- ServerStop
		return
	case serverControlValue := <-serverControlSignal:
		if serverControlValue == ServerStop { // Server has been stopped already, no longer need to watch for syscalls
			return
		}
	}
}

func startGNMIServer(telemetryCfg *TelemetryConfig, cfg *gnmi.Config, serverControlSignal chan int, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		var opts []grpc.ServerOption
		if !*telemetryCfg.NoTLS {
			var certificate tls.Certificate
			var err error
			if *telemetryCfg.Insecure {
				certificate, err = testcert.NewCert()
				if err != nil {
					log.Exitf("could not load server key pair: %s", err)
				}
			} else {
				switch {
				case *telemetryCfg.ServerCert == "":
					log.Errorf("serverCert must be set.")
					return
				case *telemetryCfg.ServerKey == "":
					log.Errorf("serverKey must be set.")
					return
				}
				certificate, err = tls.LoadX509KeyPair(*telemetryCfg.ServerCert, *telemetryCfg.ServerKey)
				if err != nil {
					computeSHA512Checksum(*telemetryCfg.ServerCert)
					computeSHA512Checksum(*telemetryCfg.ServerKey)
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

			if *telemetryCfg.AllowNoClientCert {
				// RequestClientCert will ask client for a certificate but won't
				// require it to proceed. If certificate is provided, it will be
				// verified.
				tlsCfg.ClientAuth = tls.RequestClientCert
			}

			if *telemetryCfg.CaCert != "" {
				ca, err := ioutil.ReadFile(*telemetryCfg.CaCert)
				if err != nil {
					log.Exitf("could not read CA certificate: %s", err)
				}
				certPool := x509.NewCertPool()
				if ok := certPool.AppendCertsFromPEM(ca); !ok {
					log.Exit("failed to append CA certificate")
				}
				tlsCfg.ClientCAs = certPool
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

		s, err := gnmi.NewServer(cfg, opts)
		if err != nil {
			log.Errorf("Failed to create gNMI server: %v", err)
			return
		}

		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			log.Errorf("Received error when creating fsnotify watcher %v", err)
		}

		if *telemetryCfg.WithMasterArbitration {
			s.ReqFromMaster = gnmi.ReqFromMasterEnabledMA
		}

		log.V(1).Infof("Auth Modes: %v", telemetryCfg.UserAuth)
		log.V(1).Infof("Starting RPC server on address: %s", s.Address())

		go func() {
			if err := s.Serve(); err != nil {
				log.Errorf("Serve returned with err: %v", err)
			}
		}()

		if watcher != nil {
			go iNotifyCertMonitoring(watcher, telemetryCfg, serverControlSignal, nil)
		}

		controlValue := <-serverControlSignal
		log.V(1).Infof("Received signal for gnmi server to close")
		s.Stop()
		if controlValue == 0 { // stop telemetry process, other values will restart gnmi server
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
