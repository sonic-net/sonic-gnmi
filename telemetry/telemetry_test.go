package main

import (
	"crypto/tls"
	"crypto/rsa"
	"crypto/x509"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
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
	telemetryCfg := &TelemetryConfig{}
	cfg := &gnmi.Config{}

	patches := gomonkey.ApplyFunc(setupFlags, func(*flag.FlagSet) (*TelemetryConfig, *gnmi.Config, error) {
		return telemetryCfg, cfg, nil
	})
	patches.ApplyFunc(startGNMIServer, func(_ *TelemetryConfig, _ *gnmi.Config, serverControlSignal <-chan int, wg *sync.WaitGroup) {
		defer wg.Done()
	})
	patches.ApplyFunc(signalHandler, func(serverControlSignal chan<- int, wg *sync.WaitGroup, sigchannel <-chan os.Signal) {
		defer wg.Done()
	})
	defer patches.Reset()

	args := []string{"test"}
	err := runTelemetry(args)
	if err != nil {
		t.Errorf("Expected err to be nil, but got %v", err)
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
	testServerCert := "../testdata/testserver.cer"
	testServerKey := "../testdata/testserver.key"
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

func TestSHA512Checksum(t *testing.T) {
	testServerCert := "../testdata/testserver.cer"
	testServerKey := "../testdata/testserver.key"

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

func TestSignalHandler(t *testing.T) {
	timeoutInterval := 1
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutInterval) * time.Second)
	defer cancel()

	serverControlSignal := make(chan int, 1)
	testSigChan := make(chan os.Signal, 1)
	wg := &sync.WaitGroup{}

	wg.Add(1)

	go signalHandler(serverControlSignal, wg, testSigChan)

	testSigChan <- syscall.SIGTERM

	select {
	case val := <-serverControlSignal:
		if val != 0 {
			t.Errorf("Expected 0 from serverControlSignal, got %d", val)
		}
	case <-ctx.Done():
		t.Errorf("Expected a value from serverControlSignal, but got none")
		return
	}

	wg.Wait()
}

func sendShutdownSignal(serverControlSignal chan<- int) {
	serverControlSignal <- 0
}
