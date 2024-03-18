package main

import (
	"crypto/tls"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"path/filepath"
	"errors"
	"github.com/fsnotify/fsnotify"
	"reflect"
	"sync"
	"testing"
	"flag"
	gnmi "github.com/sonic-net/sonic-gnmi/gnmi_server"
	"github.com/agiledragon/gomonkey/v2"
	"os"
	"syscall"
	"time"
	"context"
	"encoding/pem"
	"fmt"
	log "github.com/golang/glog"
	testdata "github.com/sonic-net/sonic-gnmi/testdata/tls"
)

func TestRunTelemetry(t *testing.T) {
	patches := gomonkey.ApplyFunc(startGNMIServer, func(_ *TelemetryConfig, _ *gnmi.Config, serverControlSignal chan int, wg *sync.WaitGroup) {
		defer wg.Done()
	})
	patches.ApplyFunc(signalHandler, func(serverControlSignal chan int, wg *sync.WaitGroup, sigchannel <-chan os.Signal) {
		defer wg.Done()
	})
	defer patches.Reset()

	args := []string{"telemetry", "-logtostderr",  "-port", "50051", "-v=2"}
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
			[]string{"cmd", "-port", "9090", "-threshold", "200", "-idle_conn_duration", "10", "-v", "6"},
			9090,
			200,
			10,
			6,
		},
		{
			[]string{"cmd", "-port", "2020", "-threshold", "500", "-idle_conn_duration", "4", "-v", "0"},
			2020,
			500,
			4,
			0,
		},
		{
			[]string{"cmd", "-port", "8081", "-threshold", "1", "-idle_conn_duration", "1"},
			8081,
			1,
			1,
			2,
		},
		{
			[]string{"cmd", "-port", "5050", "-threshold", "10", "-idle_conn_duration", "3", "-v", "-3"},
			5050,
			10,
			3,
			2,
		},
	}

	for _, test := range tests {
		fs := flag.NewFlagSet("testFlags", flag.ContinueOnError)
		os.Args = test.args

		config, _, err := setupFlags(fs)

		if err != nil {
			t.Errorf("Expected err to be nil, got err %v", err)
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
	testServerCert := "../testdata/certs/testserver.cer"
	testServerKey := "../testdata/certs/testserver.key"
	timeoutInterval := 3
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutInterval) * time.Second)
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
	patches.ApplyFunc(gnmi.NewServer, func(cfg *gnmi.Config, opts []grpc.ServerOption) (*gnmi.Server, error) {
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
	patches.ApplyFunc(iNotifyCertMonitoring, func(_ *fsnotify.Watcher, _ *TelemetryConfig, serverControlSignal chan<- int, testReadySignal chan<- int) {
	})

	serverControlSignal := make(chan int, 1)
	wg := &sync.WaitGroup{}

	exitCalled := false
	patches.ApplyMethod(reflect.TypeOf(&gnmi.Server{}), "Stop", func(_ *gnmi.Server) {
		exitCalled = true
	})

	defer patches.Reset()

	wg.Add(1)

	go startGNMIServer(telemetryCfg, cfg, serverControlSignal, wg)

	select {
	case <-tick.C: // Simulate shutdown
		sendShutdownSignal(serverControlSignal)
	case <-ctx.Done():
		t.Errorf("Failed to send shutdown signal")
		return
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

func createCACert(certPath string) error {
	rsaPrivateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}

	serialNum, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}

	caCert := &x509.Certificate {
		SerialNumber: serialNum,
		Subject: pkix.Name {
			Organization: []string{"Mock CA"},
		},
		NotBefore: time.Now(),
		NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA: true,
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
	testServerCert := "../testdata/certs/testserver.cer"
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
	patches.ApplyFunc(log.Exitf, func(format string, args ...interface{}) {
		t.Log("Mock of log.Exitf, so we do not exit")
	})
	patches.ApplyFunc(gnmi.NewServer, func(cfg *gnmi.Config, opts []grpc.ServerOption) (*gnmi.Server, error) {
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
	patches.ApplyFunc(iNotifyCertMonitoring, func(_ *fsnotify.Watcher, _ *TelemetryConfig, serverControlSignal chan<- int, testReadySignal chan<- int) {
	})

	serverControlSignal := make(chan int, 1)
	wg := &sync.WaitGroup{}

	patches.ApplyMethod(reflect.TypeOf(&gnmi.Server{}), "Stop", func(_ *gnmi.Server) {
	})

	defer patches.Reset()

	wg.Add(1)

	go startGNMIServer(telemetryCfg, cfg, serverControlSignal, wg)

	sendShutdownSignal(serverControlSignal)

	wg.Wait()
}

func TestStartGNMIServerCACert(t *testing.T) {
	testServerCert := "../testdata/certs/testserver.cer"
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

	err = createCACert(testServerCACert)

	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	patches := gomonkey.ApplyFunc(tls.LoadX509KeyPair, func(certFile, keyFile string) (tls.Certificate, error) {
		return tls.Certificate{}, nil
	})
	patches.ApplyFunc(gnmi.NewServer, func(cfg *gnmi.Config, opts []grpc.ServerOption) (*gnmi.Server, error) {
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
	patches.ApplyFunc(iNotifyCertMonitoring, func(_ *fsnotify.Watcher, _ *TelemetryConfig, serverControlSignal chan<- int, testReadySignal chan<- int) {
	})

	serverControlSignal := make(chan int, 1)
	wg := &sync.WaitGroup{}

	patches.ApplyMethod(reflect.TypeOf(&gnmi.Server{}), "Stop", func(_ *gnmi.Server) {
	})
	defer patches.Reset()

	wg.Add(1)

	go startGNMIServer(telemetryCfg, cfg, serverControlSignal, wg)

	sendShutdownSignal(serverControlSignal)

	wg.Wait()
}

func TestGNMIServerCreateWatcherError(t *testing.T) {
	testServerCert := "../testdata/certs/testserver.cer"
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

	patches := gomonkey.ApplyFunc(tls.LoadX509KeyPair, func(certFile, keyFile string) (tls.Certificate, error) {
		return tls.Certificate{}, fmt.Errorf("Mock LoadX509KeyPair error")
	})
	patches.ApplyFunc(log.Exitf, func(format string, args ...interface{}) {
		t.Log("Mock of log.Exitf, so we do not exit")
	})
	patches.ApplyFunc(gnmi.NewServer, func(cfg *gnmi.Config, opts []grpc.ServerOption) (*gnmi.Server, error) {
		return &gnmi.Server{}, nil
	})
	patches.ApplyFunc(grpc.Creds, func(credentials.TransportCredentials) grpc.ServerOption {
		return grpc.EmptyServerOption{}
	})
	patches.ApplyFunc(fsnotify.NewWatcher, func() (*fsnotify.Watcher, error) {
		return nil, errors.New("mock error")
	})
	patches.ApplyMethod(reflect.TypeOf(&gnmi.Server{}), "Serve", func(_ *gnmi.Server) error {
		return nil
	})
	patches.ApplyMethod(reflect.TypeOf(&gnmi.Server{}), "Address", func(_ *gnmi.Server) string {
		return ""
	})

	serverControlSignal := make(chan int, 1)
	wg := &sync.WaitGroup{}

	patches.ApplyMethod(reflect.TypeOf(&gnmi.Server{}), "Stop", func(_ *gnmi.Server) {
	})

	defer patches.Reset()

	wg.Add(1)

	go startGNMIServer(telemetryCfg, cfg, serverControlSignal, wg)

	sendShutdownSignal(serverControlSignal)

	wg.Wait()
}

func TestINotifyCertMonitoringRotation(t *testing.T) {
	testServerCert := "../testdata/certs/testserver.cer"
	testServerKey := "../testdata/certs/testserver.key"
	timeoutInterval := 10
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutInterval) * time.Second)
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

	serverControlSignal := make(chan int, 1)
	testReadySignal := make(chan int, 1)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	go iNotifyCertMonitoring(watcher, telemetryCfg, serverControlSignal, testReadySignal)

	<-testReadySignal

	// Bring in new certs

	err = saveCertKeyPair(testServerCert, testServerKey)

	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	select {
	case val := <-serverControlSignal:
		if val != ServerRestart {
			t.Errorf("Expected 1 from serverControlSignal, got %d", val)
		}
		t.Log("Received correct value from serverControlSignal")
	case <-ctx.Done():
		t.Errorf("Expected a value from serverControlSignal, but got none")
		return
	}
}

func TestINotifyCertMonitoringDeletion(t *testing.T) {
	testServerCert := "../testdata/certs/testserver.cer"
	testServerKey := "../testdata/certs/testserver.key"
	timeoutInterval := 10
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutInterval) * time.Second)
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

	serverControlSignal := make(chan int, 1)
	testReadySignal := make(chan int, 1)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	go iNotifyCertMonitoring(watcher, telemetryCfg, serverControlSignal, testReadySignal)

	<-testReadySignal

	// Delete certs

	err = os.Remove(testServerCert)

	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

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

func TestINotifyCertMonitoringErrors(t *testing.T) {
	testServerCert := "../testdata/certs/testserver.cer"
	testServerKey := "../testdata/certs/testserver.key"
	timeoutInterval := 10
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutInterval) * time.Second)
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

	serverControlSignal := make(chan int, 1)
	testReadySignal := make(chan int, 1)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	go iNotifyCertMonitoring(watcher, telemetryCfg, serverControlSignal, testReadySignal)

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

func TestINotifyCertMonitoringAddWatcherError(t *testing.T) {
	testServerCert := "../testdata/certs/testserver.cer"
	testServerKey := "../testdata/certs/testserver.key"
	timeoutInterval := 10
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutInterval) * time.Second)
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

	serverControlSignal := make(chan int, 1)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	patches := gomonkey.ApplyMethod(reflect.TypeOf(watcher), "Add", func(_ *fsnotify.Watcher, _ string) error {
		return errors.New("mock error")
	})
	defer patches.Reset()

	go iNotifyCertMonitoring(watcher, telemetryCfg, serverControlSignal, nil)

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

func TestSignalHandler(t *testing.T) {
	testHandlerSyscall(t, syscall.SIGTERM)
	testHandlerSyscall(t, syscall.SIGQUIT)
	testHandlerSyscall(t, nil) // Test that ServerStop should make signalHandler exit
}

func testHandlerSyscall(t *testing.T, signal os.Signal) {
	timeoutInterval := 1
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutInterval) * time.Second)
	defer cancel()

	serverControlSignal := make(chan int, 1)
	testSigChan := make(chan os.Signal, 1)
	wg := &sync.WaitGroup{}

	wg.Add(1)

	go signalHandler(serverControlSignal, wg, testSigChan)

	if signal == nil {
		serverControlSignal <- ServerStop
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

func sendShutdownSignal(serverControlSignal chan<- int) {
	serverControlSignal <- ServerStop
}
