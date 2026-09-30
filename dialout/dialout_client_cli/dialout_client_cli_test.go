package main

import (
	"crypto/tls"
	"flag"
	"io"
	"strings"
	"testing"
)

func TestTLSFlags(t *testing.T) {
	initial := *clientCfg.TLS
	t.Cleanup(func() { *clientCfg.TLS = initial })
	if initial.InsecureSkipVerify || initial.MinVersion != tls.VersionTLS13 {
		t.Fatalf("unexpected default TLS config: %+v", initial)
	}
	insecure := flag.Lookup("insecure")
	if insecure.DefValue != "false" || !strings.Contains(insecure.Usage, "Development only") {
		t.Fatalf("unexpected insecure flag: %+v", insecure)
	}

	for _, tt := range []struct {
		name       string
		args       []string
		insecure   bool
		serverName string
		wantErr    bool
	}{
		{name: "secure default"},
		{name: "explicit verification", args: []string{"--insecure=false"}},
		{name: "development override", args: []string{"--insecure"}, insecure: true},
		{name: "explicit override", args: []string{"--insecure=true"}, insecure: true},
		{name: "server identity", args: []string{"--server_name=collector.example"}, serverName: "collector.example"},
		{name: "invalid boolean", args: []string{"--insecure=invalid"}, wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			*clientCfg.TLS = initial
			flags := flag.NewFlagSet(t.Name(), flag.ContinueOnError)
			flags.SetOutput(io.Discard)
			for _, name := range []string{"insecure", "server_name"} {
				option := flag.Lookup(name)
				flags.Var(option.Value, name, option.Usage)
			}
			if err := flags.Parse(tt.args); (err != nil) != tt.wantErr {
				t.Fatalf("Parse(%v) = %v, want error %v", tt.args, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if clientCfg.TLS.InsecureSkipVerify != tt.insecure || clientCfg.TLS.ServerName != tt.serverName {
				t.Fatalf("unexpected TLS config: %+v", clientCfg.TLS)
			}
			if clientCfg.TLS.MinVersion != tls.VersionTLS13 {
				t.Fatal("flags changed the minimum TLS version")
			}
		})
	}
}
