package main

import (
	"fmt"
	"sync"
	"testing"
	"github.com/agiledragon/gomonkey/v2"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func TestSignalHandler(t *testing.T) {
	reload := make(chan int, 1)
	wg := &sync.WaitGroup{}
	wg.Add(1)

	go signalHandler(reload, wg)

	syscall.Kill(syscall.Getpid(), syscall.SIGTERM)

	select {
	case val := <-reload:
		if val != 0 {
			t.Errorf("Expected 0 from reload channeel, got %d", val)
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

	patches := gomonkey.ApplyGlobalVar(&port, 8080)
	patches.ApplyGlobalVar(&threshold, 100)
	patches.ApplyGlobalVar(&idle_conn_duration, 5)
	defer patches.Reset()

	// Mock StartGNMIServer, MonitorCerts, SignalHandler

	patches.ApplyFunc(startGNMIServer, func(cfg *gnmi.Config, reload <-chan int, wg *sync.WaitGroup) {
		wg.Done()
	})

	patches.ApplyFunc(signalHandler, func(reload <-chan int, wg *sync.WaitGroup) {
		wg.Done()
	})

	patches.ApplyFunc(monitorCerts, func(reload chan<- int, wg *sync.WaitGroup) {
		wg.Done()
	})

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
			10
		},
		{
			[]string{"cmd", "-port", "2020", "-threshold", "500", "-idle_conn_duration", "4"},
			2020,
			500,
			4
		},
	}

	for _, test := range tests {
		os.Args = test.args
		main() // will parse cmd line args

		//Verify global var is expected value
		if *port != test.expectedPort {
			t.Errorf("Expected port to be %d, got %d", test.expectedPort, *port)
		}

		if *threshold != test.expectedThreshold {
			t.Errorf("Expected port to be %d, got %d", test.expectedThreshold, *threshold)
		}

		if *idle_conn_duration != test.expectedIdleDur {
			t.Errorf("Expected port to be %d, got %d", test.expectedIdleDur, *idle_conn_duration)
		}
	}
}

func TestMonitorCerts(t *testing.T) {
	originalArgs := os.Args
	defer func() {
		os.Args = originalArgs
	}()

	testServerCert = "../testdata/testserver.cert"
	testServerKey = "../testdata/testserver.key"
	pollingInterval = 1
	timeoutInterval = 5

	patches := gomonkey.ApplyGlobalVar(*serverCert, testServerCert)
	patches.ApplyGlobalVar(*serverKey, testServerKey)
	patches.ApplyGlobalVar(*certPollingInt, pollingInterval)
	defer patches.Reset()

	reload := make(chan int, 1)
	wg := &sync.WaitGroup()
	wg.Add(1)

	go monitorCerts(reload, wg)

	go func() {
		time.Sleep(pollingInterval * time.Second)
		modifyStr := []byte("\n MODIFIED")
		if f, err := os.OpenFile(testServerCert, os.O_APPEND, 0644); err != nil {
			t.Errorf("Unable to open test cert file: %s", err)
		}
		defer f.Close()
		if _, writeErr := f.Write(modifyStr); writeErr != nil {
			t.Errorf("Unable to write to cert file: %s", writeErr)
		}
	}()

	select {
	case val := <-reload:
		if val != 1 {
			t.Errorf("Reload value should be 1 to indicate cert rotation needed, got val %d", val)
		}
	case <-time.After(timeoutInterval * time.Second):
		t.Errorf("Timeout exceeded for monitor certs to detect modified cert")
	}

	wg.Wait()
}

func TestStartGNMIServer(t *testing.T) {
	originalArgs := os.Args
	defer func() {
		os.Args = originalArgs
	}()

	patches := gomonkey.ApplyGlobalVar(&port, 8080)
	defer patches.Reset()

	cfg := &gnmi.Config{
		Port:                int64(*port)
		EnableTranslibWrite: true,
		EnableNativeWrite:   true,
		LogLevel:            3,
		ZmqAddress:          "",
		Threshold:           int(*threshold)
		IdleConnDuration:    int(*idle_conn_duration),
		UserAuth:            gnmi.AuthTypes{"password":true, "cert": true, "jwt": true}
	}

	patches.ApplyFunc(tls.LoadX509KeyPair, func(certFile, keyFile string) (tls.Certificate, error) {
		return tls.Certificate{}, nil
	})

	patches.ApplyFunc(gnmi.NewServer, func(cfg *gnmi.Config, opts []grpc.ServerOption) (*gnmi.Server, error) {
		return *gnmi.Server{}, nil
	})

	patches.ApplyFunc(grpc.Creds, func(credentials.TransportCredentials) grpc.ServerOption {
		return grpc.EmptyServerOption{}
	})

	exitCalled := false
	patches.ApplyFunc(os.Exit, func(code int)) {
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

	go startGNMIServer(cfg, reload, wg)

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
