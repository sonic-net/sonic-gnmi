package main

import (
	"os"
	"testing"
	"context"
	"time"
	"sync"
	"syscall"
)

func TestMain(t *testing.T) {
}

func TestSignalHandler(t *testing.T) {
	timeoutInterval := 1
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutInterval) * time.Second)
	defer cancel()

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
	case <-ctx.Done():
		t.Errorf("Expected a value in reload channel, but none received")
		return
	}

	wg.Wait()
}
