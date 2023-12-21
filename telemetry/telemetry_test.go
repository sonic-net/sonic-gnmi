package main

import (
	"crypto/tls"
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
	"strconv"
)

func TestSignalHandler(t *testing.T) {
	reload := make(chan int, 1)
	testSigChan := make(chan os.Signal, 1)
	wg := &sync.WaitGroup{}
	wg.Add(1)

	go signalHandler(reload, wg, testSigChan)

	testSigChan <- syscall.SIGTERM

	select {
	case val := <-reload:
		if val != 0 {
			t.Errorf("Expected 0 from reload channel, got %d", val)
		}
	case <-time.After(1 * time.Second):
		t.Errorf("Expected a value in reload channel, but none received")
	}

	wg.Wait()
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
	}{
		{
			[]string{"cmd", "-port", "9090", "-threshold", "200", "-idle_conn_duration", "10"},
			9090,
			200,
			10,
		},
		{
			[]string{"cmd", "-port", "2020", "-threshold", "500", "-idle_conn_duration", "4"},
			2020,
			500,
			4,
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
	}
}

func TestMonitorCerts(t *testing.T) {
	originalArgs := os.Args
	defer func() {
		os.Args = originalArgs
	}()

	testServerCert := "../testdata/testserver.cer"
	testServerKey := "../testdata/testserver.key"
	pollingInterval := 1
	timeoutInterval := 5

	fs := flag.NewFlagSet("testMonitorCerts", flag.ContinueOnError)
	os.Args = []string{"cmd", "-port", "8080", "-server_crt", testServerCert, "-server_key", testServerKey, "-cert_polling_int", strconv.Itoa(pollingInterval)}
	config, _, err := setupFlags(fs)
	if err != nil {
		t.Errorf("Expected err to be nil, got err %v", err)
	}

	reload := make(chan int, 1)
	wg := &sync.WaitGroup{}
	wg.Add(1)

	go monitorCerts(config, reload, wg)

	time.Sleep(time.Duration(pollingInterval) * time.Second)
	modifyStr := []byte("\n MODIFIED")
	f, err := os.OpenFile(testServerCert, os.O_APPEND|os.O_WRONLY, os.ModeAppend)
	if err != nil {
		t.Errorf("Unable to open test cert file: %s", err)
	}
	defer f.Close()
	if _, writeErr := f.Write(modifyStr); writeErr != nil {
		t.Errorf("Unable to write to cert file: %s", writeErr)
	}

	select {
	case val := <-reload:
		if val != 1 {
			t.Errorf("Reload value should be 1 to indicate cert rotation needed, got val %d", val)
		}
	case <-time.After(time.Duration(timeoutInterval) * time.Second):
		t.Errorf("Timeout exceeded for monitor certs to detect modified cert")
	}

	wg.Wait()
}

func TestStartGNMIServer(t *testing.T) {
	originalArgs := os.Args
	defer func() {
		os.Args = originalArgs
	}()

	fs := flag.NewFlagSet("testStartGNMIServer", flag.ContinueOnError)
	os.Args = []string{"cmd", "-port", "8080"}
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

	exitCalled := false
	patches.ApplyFunc(os.Exit, func(code int) {
		exitCalled = true
	})

	mockServe := patches.ApplyMethod(reflect.TypeOf(&gnmi.Server{}), "Serve", func(_ *gnmi.Server) error {
		return nil
	})

	mockStop := patches.ApplyMethod(reflect.TypeOf(&gnmi.Server{}), "Stop", func(_ *gnmi.Server) {})

	defer mockServe.Reset()
	defer mockStop.Reset()

	reload := make(chan int, 1)
	wg := &sync.WaitGroup{}
	wg.Add(1)

	go startGNMIServer(telemetryCfg, cfg, reload, wg)

	select {
	case reload<-0: // Simulate shutdown
	case <-time.After(1 * time.Second):
		t.Errorf("Failed to send shutdown signal")
	}

	time.Sleep(500 * time.Millisecond) // Wait for a brief moment to allow goroutine to exit after chan is updated

	if !exitCalled {
		t.Errorf("os.exit should be called if gnmi server is called to shutdown")
	}
}
