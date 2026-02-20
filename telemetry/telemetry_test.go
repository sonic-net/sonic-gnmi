package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"github.com/agiledragon/gomonkey/v2"
	"github.com/fsnotify/fsnotify"
	gnmi "github.com/sonic-net/sonic-gnmi/gnmi_server"
	"github.com/sonic-net/sonic-gnmi/test_utils"
	testdata "github.com/sonic-net/sonic-gnmi/testdata/tls"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"io"
	"io/ioutil"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestRunTelemetry(t *testing.T) {
	patches := gomonkey.ApplyFunc(startGNMIServer, func(_ *TelemetryConfig, _ *gnmi.Config, serverControlSignal chan ServerControlValue, stopSignalHandler chan<- bool, wg *sync.WaitGroup) {
		defer wg.Done()
	})
	patches.ApplyFunc(signalHandler, func(serverControlSignal chan<- ServerControlValue, sigchannel <-chan os.Signal, stopSignalHandler <-chan bool, wg *sync.WaitGroup) {
		defer wg.Done()
	})
	defer patches.Reset()

	args := []string{"telemetry", "-logtostderr", "-port", "50051", "-v=2", "-noTLS"}
	os.Args = args
	err := runTelemetry(os.Args)
	if err != nil {
		t.Errorf("Expected err to be nil, but got %v", err)
	}
	vflag := flag.Lookup("v")
	if vflag.Value.String() != "2" {
		t.Errorf("Expected v to be 2")
	}
	logtostderrflag := flag.Lookup("logtostderr")
	if logtostderrflag.Value.String() != "true" {
		t.Errorf("Expected logtostderr to be true")
	}
}

func TestFlags(t *testing.T) {
	originalArgs := os.Args
	defer func() {
		os.Args = originalArgs
	}()

	tests := []struct {
		args              []string
		expectedPort      int
		expectedThreshold int
		expectedIdleDur   int
		expectedLogLevel  int
	}{
		{
			[]string{"cmd", "-port", "9090", "-threshold", "200", "-idle_conn_duration", "10", "-v", "6", "-noTLS"},
			9090,
			200,
			10,
			6,
		},
		{
			[]string{"cmd", "-port", "2020", "-threshold", "500", "-idle_conn_duration", "4", "-v", "0", "-insecure"},
			2020,
			500,
			4,
			0,
		},
		{
			[]string{"cmd", "-port", "5050", "-threshold", "10", "-idle_conn_duration", "3", "-v", "-3", "-noTLS"},
			5050,
			10,
			3,
			2,
		},
		{
			[]string{"cmd", "-port", "8081", "-threshold", "1", "-idle_conn_duration", "1"},
			8081,
			1,
			1,
			2,
		},
		{
			[]string{"cmd", "-port", "8081", "-threshold", "1", "-idle_conn_duration", "1", "-server_crt", "../testdata/certs/testserver.cert"},
			8081,
			1,
			1,
			2,
		},
	}

	for index, test := range tests {
		fs := flag.NewFlagSet("testFlags", flag.ContinueOnError)
		os.Args = test.args

		config, _, err := setupFlags(fs)

		if index < len(tests)-2 {
			if err != nil {
				t.Errorf("Expected err to be nil, got err %v", err)
			}
		} else {
			if err == nil {
				t.Errorf("Expected missing certs err, but got no err")
			}
			continue // Expected error, no need to check rest of config
		}

		//Verify global var is expected value
		if *config.Port != test.expectedPort {
			t.Errorf("Expected port to be %d, got %d", test.expectedPort, *config.Port)
		}

		if *config.Threshold != test.expectedThreshold {
			t.Errorf("Expected threshold to be %d, got %d", test.expectedThreshold, *config.Threshold)
		}

		if *config.IdleConnDuration != test.expectedIdleDur {
			t.Errorf("Expected idle_conn_duration to be %d, got %d", test.expectedIdleDur, *config.IdleConnDuration)
		}

		if *config.LogLevel != test.expectedLogLevel {
			t.Errorf("Expected log_level to be %d, got %d", test.expectedLogLevel, *config.LogLevel)
		}
	}
}

func TestStartGNMIServer(t *testing.T) {
	testServerCert := "../testdata/certs/testserver.cert"
	testServerKey := "../testdata/certs/testserver.key"
	timeoutInterval := 3
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutInterval)*time.Second)
	defer cancel()

	originalArgs := os.Args
	defer func() {
		os.Args = originalArgs
	}()

	fs := flag.NewFlagSet("testStartGNMIServer", flag.ContinueOnError)
	os.Args = []string{"cmd", "-port", "8080", "-server_crt", testServerCert, "-server_key", testServerKey}
	telemetryCfg, cfg, err := setupFlags(fs)

	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	patches := gomonkey.ApplyFunc(tls.LoadX509KeyPair, func(certFile, keyFile string) (tls.Certificate, error) {
		return tls.Certificate{}, nil
	})
	patches.ApplyFunc(gnmi.NewServer, func(cfg *gnmi.Config, tlsOpts []grpc.ServerOption, commonOpts []grpc.ServerOption) (*gnmi.Server, error) {
		return &gnmi.Server{}, nil
	})
	patches.ApplyFunc(grpc.Creds, func(credentials.TransportCredentials) grpc.ServerOption {
		return grpc.EmptyServerOption{}
	})
	patches.ApplyMethod(reflect.TypeOf(&gnmi.Server{}), "Serve", func(_ *gnmi.Server) error {
		return nil
	})
	patches.ApplyMethod(reflect.TypeOf(&gnmi.Server{}), "Address", func(_ *gnmi.Server) string {
		return ""
	})
	patches.ApplyFunc(iNotifyCertMonitoring, func(_ *fsnotify.Watcher, _ *TelemetryConfig, serverControlSignal chan<- ServerControlValue, testReadySignal chan<- int, certLoaded *int32) {
	})

	serverControlSignal := make(chan ServerControlValue, 1)
	stopSignalHandler := make(chan bool, 1)
	wg := &sync.WaitGroup{}

	exitCalled := false
	patches.ApplyMethod(reflect.TypeOf(&gnmi.Server{}), "ForceStop", func(_ *gnmi.Server) {
		exitCalled = true
	})

	defer patches.Reset()

	wg.Add(1)

	go startGNMIServer(telemetryCfg, cfg, serverControlSignal, stopSignalHandler, wg)

	select {
	case <-tick.C: // Simulate shutdown
		sendSignal(serverControlSignal, ServerStop)
	case <-ctx.Done():
		t.Errorf("Failed to send shutdown signal")
		return
	}

	wg.Wait()

	if !exitCalled {
		t.Errorf("s.ForceStop should be called if gnmi server is called to shutdown")
	}
}

func TestStartGNMIServerGracefulStop(t *testing.T) {
	testServerCert := "../testdata/certs/testserver.cert"
	testServerKey := "../testdata/certs/testserver.key"
	timeoutInterval := 15
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutInterval)*time.Second)
	defer cancel()

	originalArgs := os.Args
	defer func() {
		os.Args = originalArgs
	}()

	fs := flag.NewFlagSet("testStartGNMIServer", flag.ContinueOnError)
	os.Args = []string{"cmd", "-port", "8080", "-server_crt", testServerCert, "-server_key", testServerKey}
	telemetryCfg, cfg, err := setupFlags(fs)

	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	patches := gomonkey.ApplyFunc(tls.LoadX509KeyPair, func(certFile, keyFile string) (tls.Certificate, error) {
		return tls.Certificate{}, nil
	})
	patches.ApplyFunc(gnmi.NewServer, func(cfg *gnmi.Config, tlsOpts []grpc.ServerOption, commonOpts []grpc.ServerOption) (*gnmi.Server, error) {
		return &gnmi.Server{}, nil
	})
	patches.ApplyFunc(grpc.Creds, func(credentials.TransportCredentials) grpc.ServerOption {
		return grpc.EmptyServerOption{}
	})
	patches.ApplyMethod(reflect.TypeOf(&gnmi.Server{}), "Serve", func(_ *gnmi.Server) error {
		return nil
	})
	patches.ApplyMethod(reflect.TypeOf(&gnmi.Server{}), "Address", func(_ *gnmi.Server) string {
		return ""
	})
	patches.ApplyFunc(iNotifyCertMonitoring, func(_ *fsnotify.Watcher, _ *TelemetryConfig, serverControlSignal chan<- ServerControlValue, testReadySignal chan<- int, certLoaded *int32) {
	})

	serverControlSignal := make(chan ServerControlValue, 1)
	stopSignalHandler := make(chan bool, 1)
	wg := &sync.WaitGroup{}

	counter := 0
	exitCalled := false
	patches.ApplyMethod(reflect.TypeOf(&gnmi.Server{}), "Stop", func(_ *gnmi.Server) {
		exitCalled = true
	})
	patches.ApplyMethod(reflect.TypeOf(&gnmi.Server{}), "ForceStop", func(_ *gnmi.Server) {
	})

	defer patches.Reset()

	wg.Add(1)

	go startGNMIServer(telemetryCfg, cfg, serverControlSignal, stopSignalHandler, wg)

	for {
		select {
		case <-tick.C:
			if counter == 0 { // simulate cert rotation first
				sendSignal(serverControlSignal, ServerRestart)
			} else { // simulate sigterm second
				sendSignal(serverControlSignal, ServerStop)
			}
			counter += 1
		case <-ctx.Done():
			t.Errorf("Failed to send shutdown signal")
			return
		}
		if counter > 1 { // both signals have been sent
			break
		}
	}

	wg.Wait()

	if !exitCalled {
		t.Errorf("s.Stop should be called if gnmi server is called to shutdown")
	}
}

// Generate a new TLS cert using NewCert and save key pair to specified file path
func saveCertKeyPair(certPath, keyPath string) error {
	cert, err := testdata.NewCert()
	if err != nil {
		return err
	}

	certBytes := cert.Certificate[0]
	keyBytes := x509.MarshalPKCS1PrivateKey(cert.PrivateKey.(*rsa.PrivateKey))

	// Save the certificate
	certFile, err := os.Create(certPath)
	if err != nil {
		return err
	}
	defer certFile.Close()

	if err := pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: certBytes}); err != nil {
		return err
	}

	// Save key
	keyFile, err := os.Create(keyPath)
	if err != nil {
		return err
	}
	defer keyFile.Close()

	if err := pem.Encode(keyFile, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: keyBytes}); err != nil {
		return err
	}

	return nil
}

func copyFile(srcPath string, destPath string) error {
	srcFile, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	destFile, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer destFile.Close()

	if _, err = io.Copy(destFile, srcFile); err != nil {
		return err
	}
	err = destFile.Sync()
	return err
}

func writeSlowKey(backupKeyPath string, keyPath string) error {
	// Copy existing key from keyPath to backupKeyPath
	err := copyFile(keyPath, backupKeyPath)
	if err != nil {
		return err
	}

	// Write from backupKeyPath to keyPath
	backupKey, err := os.Open(backupKeyPath)
	if err != nil {
		return err
	}
	defer backupKey.Close()

	key, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer key.Close()

	buffer := make([]byte, 256)
	for {
		n, err := backupKey.Read(buffer)
		if n > 0 {
			key.Write(buffer[:n])
			key.Sync()
			time.Sleep(100 * time.Millisecond)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func createCACert(certPath string) error {
	rsaPrivateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}

	serialNum, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}

	caCert := &x509.Certificate{
		SerialNumber: serialNum,
		Subject: pkix.Name{
			Organization: []string{"Mock CA"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	caBytes, err := x509.CreateCertificate(rand.Reader, caCert, caCert, &rsaPrivateKey.PublicKey, rsaPrivateKey)
	if err != nil {
		return err
	}

	// Save the certificate
	certFile, err := os.Create(certPath)
	if err != nil {
		return err
	}
	defer certFile.Close()

	if err := pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: caBytes}); err != nil {
		return err
	}

	return nil
}

func TestSHA512Checksum(t *testing.T) {
	testServerCert := "../testdata/certs/testserver.cert"
	testServerKey := "../testdata/certs/testserver.key"

	originalArgs := os.Args
	defer func() {
		os.Args = originalArgs
	}()

	fs := flag.NewFlagSet("testStartGNMIServer", flag.ContinueOnError)
	os.Args = []string{"cmd", "-port", "8080", "-server_crt", testServerCert, "-server_key", testServerKey}
	telemetryCfg, cfg, err := setupFlags(fs)
	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	err = saveCertKeyPair(testServerCert, testServerKey)

	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	patches := gomonkey.ApplyFunc(tls.LoadX509KeyPair, func(certFile, keyFile string) (tls.Certificate, error) {
		return tls.Certificate{}, fmt.Errorf("Mock LoadX509KeyPair error")
	})
	patches.ApplyFunc(gnmi.NewServer, func(cfg *gnmi.Config, tlsOpts []grpc.ServerOption, commonOpts []grpc.ServerOption) (*gnmi.Server, error) {
		return &gnmi.Server{}, nil
	})
	patches.ApplyFunc(grpc.Creds, func(credentials.TransportCredentials) grpc.ServerOption {
		return grpc.EmptyServerOption{}
	})
	patches.ApplyMethod(reflect.TypeOf(&gnmi.Server{}), "Serve", func(_ *gnmi.Server) error {
		return nil
	})
	patches.ApplyMethod(reflect.TypeOf(&gnmi.Server{}), "Address", func(_ *gnmi.Server) string {
		return ""
	})
	patches.ApplyFunc(iNotifyCertMonitoring, func(_ *fsnotify.Watcher, _ *TelemetryConfig, serverControlSignal chan<- ServerControlValue, testReadySignal chan<- int, certLoaded *int32) {
	})

	serverControlSignal := make(chan ServerControlValue, 1)
	stopSignalHandler := make(chan bool, 1)
	wg := &sync.WaitGroup{}

	patches.ApplyMethod(reflect.TypeOf(&gnmi.Server{}), "Stop", func(_ *gnmi.Server) {
	})

	defer patches.Reset()

	wg.Add(1)

	go startGNMIServer(telemetryCfg, cfg, serverControlSignal, stopSignalHandler, wg)

	sendSignal(serverControlSignal, ServerStop)

	wg.Wait()
}

func TestStartGNMIServerCACert(t *testing.T) {
	testServerCert := "../testdata/certs/testserver.cert"
	testServerKey := "../testdata/certs/testserver.key"
	testServerCACert := "../testdata/certs/testserver.pem"

	originalArgs := os.Args
	defer func() {
		os.Args = originalArgs
	}()

	fs := flag.NewFlagSet("testStartGNMIServerCACert", flag.ContinueOnError)
	os.Args = []string{"cmd", "-port", "8080", "-server_crt", testServerCert, "-server_key", testServerKey, "-ca_crt", testServerCACert, "-config_table_name", "GNMI_CLIENT_CERT"}
	telemetryCfg, cfg, err := setupFlags(fs)

	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	if cfg.ConfigTableName != "GNMI_CLIENT_CERT" {
		t.Errorf("Expected err to be GNMI_CLIENT_CERT, got %s", cfg.ConfigTableName)
	}

	err = createCACert(testServerCACert)

	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	patches := gomonkey.ApplyFunc(tls.LoadX509KeyPair, func(certFile, keyFile string) (tls.Certificate, error) {
		return tls.Certificate{}, nil
	})
	patches.ApplyFunc(gnmi.NewServer, func(cfg *gnmi.Config, tlsOpts []grpc.ServerOption, commonOpts []grpc.ServerOption) (*gnmi.Server, error) {
		return &gnmi.Server{}, nil
	})
	patches.ApplyFunc(grpc.Creds, func(credentials.TransportCredentials) grpc.ServerOption {
		return grpc.EmptyServerOption{}
	})
	patches.ApplyMethod(reflect.TypeOf(&gnmi.Server{}), "Serve", func(_ *gnmi.Server) error {
		return nil
	})
	patches.ApplyMethod(reflect.TypeOf(&gnmi.Server{}), "Address", func(_ *gnmi.Server) string {
		return ""
	})
	patches.ApplyFunc(iNotifyCertMonitoring, func(_ *fsnotify.Watcher, _ *TelemetryConfig, serverControlSignal chan<- ServerControlValue, testReadySignal chan<- int, certLoaded *int32) {
	})

	serverControlSignal := make(chan ServerControlValue, 1)
	stopSignalHandler := make(chan bool, 1)
	wg := &sync.WaitGroup{}

	patches.ApplyMethod(reflect.TypeOf(&gnmi.Server{}), "Stop", func(_ *gnmi.Server) {
	})
	defer patches.Reset()

	wg.Add(1)

	go startGNMIServer(telemetryCfg, cfg, serverControlSignal, stopSignalHandler, wg)

	sendSignal(serverControlSignal, ServerStop)

	wg.Wait()
}

func TestStartGNMIServerCreateWatcherError(t *testing.T) {
	testServerCert := "../testdata/certs/testserver.cert"
	testServerKey := "../testdata/certs/testserver.key"
	testServerCACert := "../testdata/certs/testserver.pem"

	originalArgs := os.Args
	defer func() {
		os.Args = originalArgs
	}()

	fs := flag.NewFlagSet("testStartGNMIServerCACert", flag.ContinueOnError)
	os.Args = []string{"cmd", "-port", "8080", "-server_crt", testServerCert, "-server_key", testServerKey, "-ca_crt", testServerCACert}
	telemetryCfg, cfg, err := setupFlags(fs)
	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	patches := gomonkey.ApplyFunc(tls.LoadX509KeyPair, func(certFile, keyFile string) (tls.Certificate, error) {
		return tls.Certificate{}, nil
	})
	patches.ApplyFunc(gnmi.NewServer, func(cfg *gnmi.Config, tlsOpts []grpc.ServerOption, commonOpts []grpc.ServerOption) (*gnmi.Server, error) {
		return &gnmi.Server{}, nil
	})
	patches.ApplyFunc(grpc.Creds, func(credentials.TransportCredentials) grpc.ServerOption {
		return grpc.EmptyServerOption{}
	})
	patches.ApplyFunc(fsnotify.NewWatcher, func() (*fsnotify.Watcher, error) {
		return nil, errors.New("mock newwatcher error")
	})
	patches.ApplyMethod(reflect.TypeOf(&gnmi.Server{}), "Serve", func(_ *gnmi.Server) error {
		return nil
	})
	patches.ApplyMethod(reflect.TypeOf(&gnmi.Server{}), "Address", func(_ *gnmi.Server) string {
		return ""
	})
	patches.ApplyFunc(iNotifyCertMonitoring, func(_ *fsnotify.Watcher, _ *TelemetryConfig, serverControlSignal chan<- ServerControlValue, testReadySignal chan<- int, certLoaded *int32) {
	})

	serverControlSignal := make(chan ServerControlValue, 1)
	stopSignalHandler := make(chan bool, 1)
	wg := &sync.WaitGroup{}

	patches.ApplyMethod(reflect.TypeOf(&gnmi.Server{}), "Stop", func(_ *gnmi.Server) {
	})
	defer patches.Reset()

	wg.Add(1)

	go startGNMIServer(telemetryCfg, cfg, serverControlSignal, stopSignalHandler, wg)

	sendSignal(serverControlSignal, ServerStop)

	wg.Wait()
}

func TestStartGNMIServerSlowCerts(t *testing.T) {
	testServerCert := "../testdata/certs/testserver.cert"
	testServerKey := "../testdata/certs/testserver.key"

	originalArgs := os.Args
	defer func() {
		os.Args = originalArgs
	}()

	fs := flag.NewFlagSet("testStartGNMIServer", flag.ContinueOnError)
	os.Args = []string{"cmd", "-port", "8080", "-server_crt", testServerCert, "-server_key", testServerKey}
	telemetryCfg, cfg, err := setupFlags(fs)
	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	var shouldFail int32
	atomic.StoreInt32(&shouldFail, 1)
	serveStarted := make(chan bool, 1)

	patches := gomonkey.ApplyFunc(tls.LoadX509KeyPair, func(certFile, keyFile string) (tls.Certificate, error) {
		if atomic.LoadInt32(&shouldFail) == 1 {
			atomic.StoreInt32(&shouldFail, 0)
			return tls.Certificate{}, fmt.Errorf("Mock LoadX509KeyPair error")
		}
		return tls.Certificate{}, nil
	})
	patches.ApplyFunc(computeSHA512Checksum, func(file string) {
	})
	patches.ApplyFunc(gnmi.NewServer, func(cfg *gnmi.Config, tlsOpts []grpc.ServerOption, commonOpts []grpc.ServerOption) (*gnmi.Server, error) {
		return &gnmi.Server{}, nil
	})
	patches.ApplyFunc(grpc.Creds, func(credentials.TransportCredentials) grpc.ServerOption {
		return grpc.EmptyServerOption{}
	})
	patches.ApplyMethod(reflect.TypeOf(&gnmi.Server{}), "Serve", func(_ *gnmi.Server) error {
		serveStarted <- true
		return nil
	})
	patches.ApplyMethod(reflect.TypeOf(&gnmi.Server{}), "Address", func(_ *gnmi.Server) string {
		return ""
	})
	patches.ApplyFunc(iNotifyCertMonitoring, func(_ *fsnotify.Watcher, _ *TelemetryConfig, serverControlSignal chan<- ServerControlValue, testReadySignal chan<- int, certLoaded *int32) {
	})

	serverControlSignal := make(chan ServerControlValue, 1)
	stopSignalHandler := make(chan bool, 1)
	wg := &sync.WaitGroup{}

	patches.ApplyMethod(reflect.TypeOf(&gnmi.Server{}), "Stop", func(_ *gnmi.Server) {
	})

	defer patches.Reset()

	wg.Add(1)

	go startGNMIServer(telemetryCfg, cfg, serverControlSignal, stopSignalHandler, wg)

	sendSignal(serverControlSignal, ServerRestart) // Should not stop cert monitoring or try reloading certs

	sendSignal(serverControlSignal, ServerStart) // Put certs for server to load new certs

	<-serveStarted

	sendSignal(serverControlSignal, ServerStop) // Once server starts serving, stop server

	wg.Wait()
}

func TestStartGNMIServerSlowCACerts(t *testing.T) {
	testServerCert := "../testdata/certs/testserver.cert"
	testServerKey := "../testdata/certs/testserver.key"
	testServerCACert := "../testdata/certs/testserver.pem"

	originalArgs := os.Args
	defer func() {
		os.Args = originalArgs
	}()

	fs := flag.NewFlagSet("testStartGNMIServerCACert", flag.ContinueOnError)
	os.Args = []string{"cmd", "-port", "8080", "-server_crt", testServerCert, "-server_key", testServerKey, "-ca_crt", testServerCACert}
	telemetryCfg, cfg, err := setupFlags(fs)
	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	var shouldFail int32
	atomic.StoreInt32(&shouldFail, 1)
	serveStarted := make(chan bool, 1)

	patches := gomonkey.ApplyFunc(tls.LoadX509KeyPair, func(certFile, keyFile string) (tls.Certificate, error) {
		return tls.Certificate{}, nil
	})
	patches.ApplyFunc(computeSHA512Checksum, func(file string) {
	})
	patches.ApplyFunc(ioutil.ReadFile, func(_ string) ([]byte, error) {
		if atomic.LoadInt32(&shouldFail) == 1 {
			return []byte("mock data"), fmt.Errorf("Mock ioutil ReadFile error")
		}
		return []byte("mock data"), nil
	})
	patches.ApplyMethod(reflect.TypeOf(&x509.CertPool{}), "AppendCertsFromPEM", func(_ *x509.CertPool, _ []byte) bool {
		if atomic.LoadInt32(&shouldFail) == 1 {
			atomic.StoreInt32(&shouldFail, 0)
			return false
		}
		return true
	})
	patches.ApplyFunc(gnmi.NewServer, func(cfg *gnmi.Config, tlsOpts []grpc.ServerOption, commonOpts []grpc.ServerOption) (*gnmi.Server, error) {
		return &gnmi.Server{}, nil
	})
	patches.ApplyFunc(grpc.Creds, func(credentials.TransportCredentials) grpc.ServerOption {
		return grpc.EmptyServerOption{}
	})
	patches.ApplyMethod(reflect.TypeOf(&gnmi.Server{}), "Serve", func(_ *gnmi.Server) error {
		serveStarted <- true
		return nil
	})
	patches.ApplyMethod(reflect.TypeOf(&gnmi.Server{}), "Address", func(_ *gnmi.Server) string {
		return ""
	})
	patches.ApplyFunc(iNotifyCertMonitoring, func(_ *fsnotify.Watcher, _ *TelemetryConfig, serverControlSignal chan<- ServerControlValue, testReadySignal chan<- int, certLoaded *int32) {
	})

	serverControlSignal := make(chan ServerControlValue, 1)
	stopSignalHandler := make(chan bool, 1)
	wg := &sync.WaitGroup{}

	patches.ApplyMethod(reflect.TypeOf(&gnmi.Server{}), "Stop", func(_ *gnmi.Server) {
	})
	defer patches.Reset()

	wg.Add(1)

	go startGNMIServer(telemetryCfg, cfg, serverControlSignal, stopSignalHandler, wg)

	sendSignal(serverControlSignal, ServerStart) // Put certs for server to load new certs

	<-serveStarted

	sendSignal(serverControlSignal, ServerStop) // Once server starts serving, stop server

	wg.Wait()
}

func TestINotifyCertMonitoringRotation(t *testing.T) {
	testServerCert := "../testdata/certs/testserver.cert"
	testServerKey := "../testdata/certs/testserver.key"
	timeoutInterval := 10
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutInterval)*time.Second)
	defer cancel()

	originalArgs := os.Args
	defer func() {
		os.Args = originalArgs
	}()

	fs := flag.NewFlagSet("testiNotifyCertMonitoring", flag.ContinueOnError)
	os.Args = []string{"cmd", "-v=2", "-port", "8080", "-server_crt", testServerCert, "-server_key", testServerKey}
	telemetryCfg, _, err := setupFlags(fs)
	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	serverControlSignal := make(chan ServerControlValue, 1)
	testReadySignal := make(chan int, 1)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	go iNotifyCertMonitoring(watcher, telemetryCfg, serverControlSignal, testReadySignal, nil)

	<-testReadySignal

	// Bring in new certs

	err = saveCertKeyPair(testServerCert, testServerKey)

	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	select {
	case val := <-serverControlSignal:
		if val != ServerStart {
			t.Errorf("Expected 2 from serverControlSignal, got %d", val)
		}
		t.Log("Received correct value from serverControlSignal")
	case <-ctx.Done():
		t.Errorf("Expected a value from serverControlSignal, but got none")
		return
	}
}

func TestINotifyCertMonitoringDeletion(t *testing.T) {
	testServerCert := "../testdata/certs/testserver.cert"
	testServerKey := "../testdata/certs/testserver.key"
	timeoutInterval := time.Duration(10 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), timeoutInterval)
	defer cancel()

	originalArgs := os.Args
	defer func() {
		os.Args = originalArgs
	}()

	fs := flag.NewFlagSet("testiNotifyCertMonitoring", flag.ContinueOnError)
	os.Args = []string{"cmd", "-v=2", "-port", "8080", "-server_crt", testServerCert, "-server_key", testServerKey}
	telemetryCfg, _, err := setupFlags(fs)
	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	serverControlSignal := make(chan ServerControlValue, 1)
	testReadySignal := make(chan int, 1)
	var certLoaded int32
	atomic.StoreInt32(&certLoaded, 1)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	go iNotifyCertMonitoring(watcher, telemetryCfg, serverControlSignal, testReadySignal, &certLoaded)

	<-testReadySignal

	// Delete certs

	err = os.Remove(testServerCert)

	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	select {
	case val := <-serverControlSignal:
		if val != ServerRestart {
			t.Errorf("Expected 2 from serverControlSignal, got %d", val)
		}
		t.Log("Received correct value from serverControlSignal")
	case <-ctx.Done():
		t.Errorf("Expected a value from serverControlSignal, but got none")
		return
	}
}

func TestINotifyCertMonitoringSlowWrites(t *testing.T) {
	testServerCert := "../testdata/certs/testserver.cert"
	testServerKey := "../testdata/certs/testserver.key"
	tempDir := t.TempDir()
	testServerKeyBackup := filepath.Join(tempDir, "testserver.key.backup")
	timeoutInterval := time.Duration(10 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), timeoutInterval)
	defer cancel()

	originalArgs := os.Args
	defer func() {
		os.Args = originalArgs
	}()

	fs := flag.NewFlagSet("testiNotifyCertMonitoring", flag.ContinueOnError)
	os.Args = []string{"cmd", "-v=2", "-port", "8080", "-server_crt", testServerCert, "-server_key", testServerKey}
	telemetryCfg, _, err := setupFlags(fs)
	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	serverControlSignal := make(chan ServerControlValue, 1)
	testReadySignal := make(chan int, 1)
	var certLoaded int32
	atomic.StoreInt32(&certLoaded, 1)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	err = saveCertKeyPair(testServerCert, testServerKey)
	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	go iNotifyCertMonitoring(watcher, telemetryCfg, serverControlSignal, testReadySignal, &certLoaded)

	<-testReadySignal

	// Write slowly to key file such that only get 1 reload after multiple writes

	err = writeSlowKey(testServerKeyBackup, testServerKey)
	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	select {
	case val := <-serverControlSignal:
		if val != ServerStart {
			t.Errorf("Expected 1 from serverControlSignal, got %d", val)
		}
		t.Log("Received correct value from serverControlSignal")
		_, err = tls.LoadX509KeyPair(testServerCert, testServerKey) // Cert should work after 1 reload
		if err != nil {
			t.Errorf("Expected err to be nil, got err %v", err)
		}
	case <-ctx.Done():
		t.Errorf("Expected a value from serverControlSignal, but got none")
		return
	}
}

func TestINotifyCertMonitoringMove(t *testing.T) {
	testServerCert := "../testdata/certs/testserver.cert"
	testServerKey := "../testdata/certs/testserver.key"
	testServerCertBackup := "../testdata/testserver.cert"
	testServerKeyBackup := "../testdata/testserver.key"
	timeoutInterval := time.Duration(10 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), timeoutInterval)
	defer cancel()

	originalArgs := os.Args
	defer func() {
		os.Args = originalArgs
	}()

	fs := flag.NewFlagSet("testiNotifyCertMonitoring", flag.ContinueOnError)
	os.Args = []string{"cmd", "-v=2", "-port", "8080", "-server_crt", testServerCert, "-server_key", testServerKey}
	telemetryCfg, _, err := setupFlags(fs)
	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	serverControlSignal := make(chan ServerControlValue, 1)
	testReadySignal := make(chan int, 1)
	var certLoaded int32
	atomic.StoreInt32(&certLoaded, 1)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	err = saveCertKeyPair(testServerCertBackup, testServerKeyBackup)
	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	err = os.Remove(testServerCert)
	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	err = os.Remove(testServerKey)
	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	err = os.Rename(testServerCertBackup, testServerCert)
	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	go iNotifyCertMonitoring(watcher, telemetryCfg, serverControlSignal, testReadySignal, &certLoaded)

	<-testReadySignal

	// Move key file from other directory to monitored directory and ensure after 1 reload, LoadKeyPair works

	err = os.Rename(testServerKeyBackup, testServerKey)
	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	select {
	case val := <-serverControlSignal:
		if val != ServerStart {
			t.Errorf("Expected 1 from serverControlSignal, got %d", val)
		}
		t.Log("Received correct value from serverControlSignal")
		_, err = tls.LoadX509KeyPair(testServerCert, testServerKey) // Cert should work after 1 reload
		if err != nil {
			t.Errorf("Expected err to be nil, got err %v", err)
		}
	case <-ctx.Done():
		t.Errorf("Expected a value from serverControlSignal, but got none")
		return
	}
}

func TestINotifyCertMonitoringCopy(t *testing.T) {
	testServerCert := "../testdata/certs/testserver.cert"
	testServerKey := "../testdata/certs/testserver.key"
	tempDir := t.TempDir()
	testServerCertBackup := filepath.Join(tempDir, "testserver.cert.backup")
	testServerKeyBackup := filepath.Join(tempDir, "testserver.key.backup")
	timeoutInterval := time.Duration(10 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), timeoutInterval)
	defer cancel()

	originalArgs := os.Args
	defer func() {
		os.Args = originalArgs
	}()

	fs := flag.NewFlagSet("testiNotifyCertMonitoring", flag.ContinueOnError)
	os.Args = []string{"cmd", "-v=2", "-port", "8080", "-server_crt", testServerCert, "-server_key", testServerKey}
	telemetryCfg, _, err := setupFlags(fs)
	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	serverControlSignal := make(chan ServerControlValue, 1)
	testReadySignal := make(chan int, 1)
	var certLoaded int32
	atomic.StoreInt32(&certLoaded, 1)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	err = saveCertKeyPair(testServerCertBackup, testServerKeyBackup)
	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	err = saveCertKeyPair(testServerCert, testServerKey)
	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	err = copyFile(testServerCertBackup, testServerCert)
	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	go iNotifyCertMonitoring(watcher, telemetryCfg, serverControlSignal, testReadySignal, &certLoaded)

	<-testReadySignal

	// Copy key file from other directory to monitored directory and ensure after 1 reload, LoadKeyPair works

	err = copyFile(testServerKeyBackup, testServerKey)
	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	select {
	case val := <-serverControlSignal:
		if val != ServerStart {
			t.Errorf("Expected 1 from serverControlSignal, got %d", val)
		}
		t.Log("Received correct value from serverControlSignal")
		_, err = tls.LoadX509KeyPair(testServerCert, testServerKey) // Cert should work after 1 reload
		if err != nil {
			t.Errorf("Expected err to be nil, got err %v", err)
		}
	case <-ctx.Done():
		t.Errorf("Expected a value from serverControlSignal, but got none")
		return
	}
}

func TestINotifyCertMonitoringErrors(t *testing.T) {
	testServerCert := "../testdata/certs/testserver.cert"
	testServerKey := "../testdata/certs/testserver.key"
	timeoutInterval := time.Duration(10 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), timeoutInterval)
	defer cancel()

	originalArgs := os.Args
	defer func() {
		os.Args = originalArgs
	}()

	fs := flag.NewFlagSet("testiNotifyCertMonitoring", flag.ContinueOnError)
	os.Args = []string{"cmd", "-v=2", "-port", "8080", "-server_crt", testServerCert, "-server_key", testServerKey}
	telemetryCfg, _, err := setupFlags(fs)
	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	serverControlSignal := make(chan ServerControlValue, 1)
	testReadySignal := make(chan int, 1)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	go iNotifyCertMonitoring(watcher, telemetryCfg, serverControlSignal, testReadySignal, nil)

	<-testReadySignal

	// Put error in error channel

	mockError := errors.New("mock error")
	watcher.Errors <- mockError

	select {
	case val := <-serverControlSignal:
		if val != ServerStop {
			t.Errorf("Expected 0 from serverControlSignal, got %d", val)
		}
		t.Log("Received correct value from serverControlSignal")
	case <-ctx.Done():
		t.Errorf("Expected a value from serverControlSignal, but got none")
		return
	}
}

// Temporarily disabling this function due to flakiness, Zain will later fix this function
func DisabledTestINotifyCertMonitoringAddWatcherError(t *testing.T) {
	testServerCert := "../testdata/certs/testserver.cert"
	testServerKey := "../testdata/certs/testserver.key"
	timeoutInterval := time.Duration(10 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), timeoutInterval)
	defer cancel()

	originalArgs := os.Args
	defer func() {
		os.Args = originalArgs
	}()

	fs := flag.NewFlagSet("testiNotifyCertMonitoring", flag.ContinueOnError)
	os.Args = []string{"cmd", "-v=2", "-port", "8080", "-server_crt", testServerCert, "-server_key", testServerKey}
	telemetryCfg, _, err := setupFlags(fs)
	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	serverControlSignal := make(chan ServerControlValue, 1)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	patches := gomonkey.ApplyMethod(reflect.TypeOf(watcher), "Add", func(_ *fsnotify.Watcher, _ string) error {
		return errors.New("mock error")
	})
	defer patches.Reset()

	go iNotifyCertMonitoring(watcher, telemetryCfg, serverControlSignal, nil, nil)

	select {
	case val := <-serverControlSignal:
		if val != ServerStop {
			t.Errorf("Expected 0 from serverControlSignal, got %d", val)
		}
		t.Log("Received correct value from serverControlSignal")
	case <-ctx.Done():
		t.Errorf("Expected a value from serverControlSignal, but got none")
		return
	}
}

func TestINotifyCertMonitoringSymlinkRotation(t *testing.T) {
	tmpDir := t.TempDir()
	testServerCert := filepath.Join(tmpDir, "server.crt")
	testServerKey := filepath.Join(tmpDir, "server.key")

	timeoutInterval := 10
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutInterval)*time.Second)
	defer cancel()

	originalArgs := os.Args
	defer func() {
		os.Args = originalArgs
	}()

	// Create initial cert/key files with numbered backup names
	certBackup1 := filepath.Join(tmpDir, "server.crt.1")
	keyBackup1 := filepath.Join(tmpDir, "server.key.1")

	err := saveCertKeyPair(certBackup1, keyBackup1)
	if err != nil {
		t.Fatalf("Failed to create initial cert/key pair: %v", err)
	}

	// Create symlinks pointing to initial backup
	err = os.Symlink(certBackup1, testServerCert)
	if err != nil {
		t.Fatalf("Failed to create cert symlink: %v", err)
	}
	err = os.Symlink(keyBackup1, testServerKey)
	if err != nil {
		t.Fatalf("Failed to create key symlink: %v", err)
	}

	fs := flag.NewFlagSet("testSymlinkRotation", flag.ContinueOnError)
	os.Args = []string{"cmd", "-v=2", "-port", "8080", "-server_crt", testServerCert, "-server_key", testServerKey}
	telemetryCfg, _, err := setupFlags(fs)
	if err != nil {
		t.Fatalf("Failed to setup flags: %v", err)
	}

	serverControlSignal := make(chan ServerControlValue, 1)
	testReadySignal := make(chan int, 1)
	var certLoaded int32
	atomic.StoreInt32(&certLoaded, 0)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("Failed to create watcher: %v", err)
	}

	go iNotifyCertMonitoring(watcher, telemetryCfg, serverControlSignal, testReadySignal, &certLoaded)

	<-testReadySignal

	// Simulate cert rotation: create new backup files and update symlinks (like ln -sf)
	certBackup2 := filepath.Join(tmpDir, "server.crt.2")
	keyBackup2 := filepath.Join(tmpDir, "server.key.2")

	err = saveCertKeyPair(certBackup2, keyBackup2)
	if err != nil {
		t.Fatalf("Failed to create new cert/key pair: %v", err)
	}

	os.Remove(testServerCert)
	err = os.Symlink(certBackup2, testServerCert)
	if err != nil {
		t.Fatalf("Failed to update cert symlink: %v", err)
	}

	os.Remove(testServerKey)
	err = os.Symlink(keyBackup2, testServerKey)
	if err != nil {
		t.Fatalf("Failed to update key symlink: %v", err)
	}

	for {
		select {
		case val := <-serverControlSignal:
			if val == ServerStart {
				t.Log("Received correct ServerStart signal after symlink rotation")
				return
			}
			// Ignore ServerRestart from REMOVE events
			t.Logf("Received ServerRestart (expected during symlink update)")
		case <-time.After(100 * time.Millisecond):
			// No more signals in buffer, wait for ServerStart
			select {
			case val := <-serverControlSignal:
				if val != ServerStart {
					t.Errorf("Expected ServerStart from serverControlSignal, got %d", val)
				} else {
					t.Log("Received correct ServerStart signal after symlink rotation")
				}
				return
			case <-ctx.Done():
				t.Errorf("Expected ServerStart from serverControlSignal, but got none")
				return
			}
		}
	}
}

func TestINotifyCertMonitoringCertValidationFails(t *testing.T) {
	tmpDir := t.TempDir()
	testServerCert := filepath.Join(tmpDir, "server.crt")
	testServerKey := filepath.Join(tmpDir, "server.key")

	timeoutInterval := 10
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutInterval)*time.Second)
	defer cancel()

	originalArgs := os.Args
	defer func() {
		os.Args = originalArgs
	}()

	fs := flag.NewFlagSet("testCertValidationFails", flag.ContinueOnError)
	os.Args = []string{"cmd", "-v=2", "-port", "8080", "-server_crt", testServerCert, "-server_key", testServerKey}
	telemetryCfg, _, err := setupFlags(fs)
	if err != nil {
		t.Fatalf("Failed to setup flags: %v", err)
	}

	serverControlSignal := make(chan ServerControlValue, 1)
	testReadySignal := make(chan int, 1)
	var certLoaded int32
	atomic.StoreInt32(&certLoaded, 0)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("Failed to create watcher: %v", err)
	}

	go iNotifyCertMonitoring(watcher, telemetryCfg, serverControlSignal, testReadySignal, &certLoaded)

	<-testReadySignal

	tempDir := t.TempDir()
	tempCert := filepath.Join(tempDir, "temp.crt")
	tempKey := filepath.Join(tempDir, "temp.key")

	err = saveCertKeyPair(tempCert, tempKey)
	if err != nil {
		t.Fatalf("Failed to create temp cert/key pair: %v", err)
	}

	err = copyFile(tempCert, testServerCert)
	if err != nil {
		t.Fatalf("Failed to copy cert file: %v", err)
	}

	select {
	case val := <-serverControlSignal:
		t.Errorf("Expected no signal due to cert validation failure, but got signal: %d", val)
	case <-time.After(500 * time.Millisecond):
		t.Log("Correctly received no signal after cert validation failure")
	}

	err = copyFile(tempKey, testServerKey)
	if err != nil {
		t.Fatalf("Failed to copy key file: %v", err)
	}

	select {
	case val := <-serverControlSignal:
		if val != ServerStart {
			t.Errorf("Expected ServerStart from serverControlSignal, got %d", val)
		} else {
			t.Log("Received correct ServerStart signal after valid cert/key pair written")
		}
	case <-ctx.Done():
		t.Errorf("Expected ServerStart from serverControlSignal after valid cert, but got none")
	}
}

func TestSignalHandler(t *testing.T) {
	testHandlerSyscall(t, syscall.SIGTERM)
	testHandlerSyscall(t, syscall.SIGQUIT)
	testHandlerSyscall(t, syscall.SIGINT)
	testHandlerSyscall(t, syscall.SIGHUP)
	testHandlerSyscall(t, nil) // Test that ServerStop should make signalHandler exit
}

func testHandlerSyscall(t *testing.T, signal os.Signal) {
	timeoutInterval := 1
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutInterval)*time.Second)
	defer cancel()

	serverControlSignal := make(chan ServerControlValue, 1)
	stopSignalHandler := make(chan bool, 1)
	testSigChan := make(chan os.Signal, 1)
	wg := &sync.WaitGroup{}

	wg.Add(1)

	go signalHandler(serverControlSignal, testSigChan, stopSignalHandler, wg)

	if signal == nil {
		stopSignalHandler <- true
		wg.Wait()
		return
	}

	testSigChan <- signal

	select {
	case val := <-serverControlSignal:
		if val != ServerStop {
			t.Errorf("Expected 0 from serverControlSignal, got %d", val)
		}
	case <-ctx.Done():
		t.Errorf("Expected a value from serverControlSignal, but got none")
		return
	}

	wg.Wait()
}

func sendSignal(serverControlSignal chan<- ServerControlValue, value ServerControlValue) {
	serverControlSignal <- value
}

func TestFlagsNoPortNoUnixSocket(t *testing.T) {
	// Test that validation fails when neither port nor unix_socket is specified
	originalArgs := os.Args
	defer func() {
		os.Args = originalArgs
	}()

	fs := flag.NewFlagSet("testNoPortNoUnixSocket", flag.ContinueOnError)
	// Explicitly disable unix_socket (since it now has a default) and set port to 0
	os.Args = []string{"cmd", "-port", "0", "-unix_socket", "", "-noTLS"}

	cfg, _, err := setupFlags(fs)
	if err == nil {
		t.Error("Expected error when port is 0 and no unix_socket, but got nil")
	}
	if cfg != nil {
		t.Error("Expected nil config when validation fails")
	}
	if err != nil && !strings.Contains(err.Error(), "port must be > 0") {
		t.Errorf("Expected 'port must be > 0' error, got: %v", err)
	}
}

func TestMain(m *testing.M) {
	defer test_utils.MemLeakCheck()
	m.Run()
}
