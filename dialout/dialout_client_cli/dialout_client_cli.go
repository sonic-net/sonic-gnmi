// The telemetry_client program implements the telemetry publish client.
package main

import (
	"context"
	"crypto/tls"
	"flag"
	log "github.com/golang/glog"
	dc "github.com/jipanyang/sonic-telemetry/dialout/dialout_client"
	gpb "github.com/openconfig/gnmi/proto/gnmi"
	"os"
	"os/signal"
	"time"
)

var (
	clientCfg = dc.ClientConfig{
		SrcIp:          "",
		RetryInterval:  30 * time.Second,
		Encoding:       gpb.Encoding_JSON_IETF,
		Unidirectional: true,
		TLS:            &tls.Config{},
	}
)

func init() {
	flag.StringVar(&clientCfg.TLS.ServerName, "server_name", "", "When set, use this hostname to verify server certificate during TLS handshake.")
	flag.BoolVar(&clientCfg.TLS.InsecureSkipVerify, "insecure", false, "When set, client will not verify the server certificate during TLS handshake.")
	flag.DurationVar(&clientCfg.RetryInterval, "retry_interval", 30*time.Second, "Interval at which client tries to reconnect to destination servers")
	flag.BoolVar(&clientCfg.Unidirectional, "unidirectional", true, "No repesponse from server is expected")
}

func main() {
	flag.Parse()
	ctx, cancel := context.WithCancel(context.Background())
	// Terminate on Ctrl+C
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt)
		<-c
		cancel()
	}()
	log.V(1).Infof("Starting telemetry publish client")
	dc.DialOutRun(ctx, &clientCfg)
	log.V(1).Infof("Exiting telemetry publish client")
	log.Flush()
}
