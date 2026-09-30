// The dialout_client_cli program implements the telemetry publish client.
package main

import (
	"context"
	"crypto/tls"
	"flag"
	"os"
	"os/signal"
	"time"

	dc "github.com/sonic-net/sonic-gnmi/dialout/dialout_client"

	log "github.com/golang/glog"
	gpb "github.com/openconfig/gnmi/proto/gnmi"
)

var (
	clientCfg = dc.ClientConfig{
		SrcIp:          "",
		RetryInterval:  30 * time.Second,
		Encoding:       gpb.Encoding_JSON_IETF,
		Unidirectional: true,
		TLS:            &tls.Config{MinVersion: tls.VersionTLS13},
	}
)

func init() {
	flag.StringVar(&clientCfg.TLS.ServerName, "server_name", "", "When set, use this hostname to verify server certificate during TLS handshake.")
	flag.BoolVar(&clientCfg.TLS.InsecureSkipVerify, "insecure", false, "Development only: disable collector certificate verification. Do not use in production.")
	flag.DurationVar(&clientCfg.RetryInterval, "retry_interval", 30*time.Second, "Interval at which client tries to reconnect to destination servers")
	flag.BoolVar(&clientCfg.Unidirectional, "unidirectional", true, "No repesponse from server is expected")
}

func main() {
	flag.Parse()
	if clientCfg.TLS.InsecureSkipVerify {
		log.Warning("Collector certificate verification is disabled by -insecure; use only for development, not production.")
	}
	ctx, cancel := context.WithCancel(context.Background())
	// Terminate on Ctrl+C
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt)
		<-c
		cancel()
	}()
	log.V(1).Infof("Starting telemetry publish client")
	err := dc.DialOutRun(ctx, &clientCfg)
	log.V(1).Infof("Exiting telemetry publish client: %v", err)
	log.Flush()
}
