package system

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	syspb "github.com/openconfig/gnoi/system"
	"github.com/openconfig/gnoi/types"
	"github.com/sonic-net/sonic-gnmi/pkg/exec"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// fakeHostRun replaces runHostCommandStream with one that prints lines and
// exits with exitCode, and records the command it was given.
type fakeHostRun struct {
	name     string
	args     []string
	lines    []string
	exitCode int
	err      error
}

func useFakeHostRun(t *testing.T, f *fakeHostRun) {
	t.Helper()
	orig := runHostCommandStream
	runHostCommandStream = func(ctx context.Context, name string, args []string, _ *exec.RunHostCommandOptions, onLine func(string) error) (int, error) {
		f.name, f.args = name, args
		for _, l := range f.lines {
			if err := onLine(l); err != nil {
				return -1, err
			}
		}
		return f.exitCode, f.err
	}
	t.Cleanup(func() { runHostCommandStream = orig })
}

type fakePingStream struct {
	grpc.ServerStream
	ctx     context.Context
	sent    []*syspb.PingResponse
	sendErr error
}

func (s *fakePingStream) Context() context.Context { return s.ctx }
func (s *fakePingStream) Send(r *syspb.PingResponse) error {
	if s.sendErr != nil {
		return s.sendErr
	}
	s.sent = append(s.sent, r)
	return nil
}

type fakeTracerouteStream struct {
	grpc.ServerStream
	ctx  context.Context
	sent []*syspb.TracerouteResponse
}

func (s *fakeTracerouteStream) Context() context.Context { return s.ctx }
func (s *fakeTracerouteStream) Send(r *syspb.TracerouteResponse) error {
	s.sent = append(s.sent, r)
	return nil
}

func wantCode(t *testing.T, err error, code codes.Code) {
	t.Helper()
	if status.Code(err) != code {
		t.Fatalf("error = %v, want code %v", err, code)
	}
}

func TestValidateHostArg(t *testing.T) {
	for _, ok := range []string{"10.0.0.1", "fe80::1%eth0", "2001:db8::1", "host-1.example.com", "Ethernet0"} {
		if err := validateHostArg("destination", ok); err != nil {
			t.Errorf("validateHostArg(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"-f", "--help", "1.1.1.1 -c 9", "a;b", "a/b", strings.Repeat("a", 256)} {
		if err := validateHostArg("destination", bad); err == nil {
			t.Errorf("validateHostArg(%q) = nil, want error", bad)
		}
	}
}

func TestHostVrf(t *testing.T) {
	for in, want := range map[string]string{"": "", "default": "", "DEFAULT": "", "mgmt": "mgmt", "Vrf_red": "Vrf_red"} {
		got, err := hostVrf(in)
		if err != nil || got != want {
			t.Errorf("hostVrf(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"-x", "Vrf red", "a-very-long-vrf-name"} {
		if _, err := hostVrf(bad); err == nil {
			t.Errorf("hostVrf(%q) = nil error, want error", bad)
		}
	}
}

func TestPingCommand(t *testing.T) {
	tests := []struct {
		name     string
		req      *syspb.PingRequest
		wantName string
		wantArgs []string
	}{
		{
			name:     "defaults",
			req:      &syspb.PingRequest{Destination: "10.0.0.1"},
			wantName: "ping",
			wantArgs: []string{"-n", "-c", "5", "10.0.0.1"},
		},
		{
			name: "all options in a VRF",
			req: &syspb.PingRequest{
				Destination:     "2001:db8::1",
				Source:          "2001:db8::2",
				Count:           3,
				Interval:        500_000_000,
				Wait:            2_000_000_000,
				Size:            1400,
				DoNotFragment:   true,
				L3Protocol:      types.L3Protocol_IPV6,
				NetworkInstance: "mgmt",
			},
			wantName: "ip",
			wantArgs: []string{"vrf", "exec", "mgmt", "ping", "-n", "-6", "-c", "3", "-i", "0.5", "-W", "2", "-s", "1400", "-M", "do", "-I", "2001:db8::2", "2001:db8::1"},
		},
		{
			name:     "flood ping",
			req:      &syspb.PingRequest{Destination: "10.0.0.1", Count: 100, Interval: -1},
			wantName: "ping",
			wantArgs: []string{"-n", "-c", "100", "-f", "10.0.0.1"},
		},
		{
			name:     "count -1 pings until cancelled",
			req:      &syspb.PingRequest{Destination: "10.0.0.1", Count: -1, L3Protocol: types.L3Protocol_IPV4},
			wantName: "ping",
			wantArgs: []string{"-n", "-4", "10.0.0.1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, args, err := pingCommand(tt.req)
			if err != nil {
				t.Fatalf("pingCommand() error = %v", err)
			}
			if name != tt.wantName || !reflect.DeepEqual(args, tt.wantArgs) {
				t.Errorf("pingCommand() = %q %q, want %q %q", name, args, tt.wantName, tt.wantArgs)
			}
		})
	}
}

func TestPingCommandRejects(t *testing.T) {
	for name, req := range map[string]*syspb.PingRequest{
		"no destination":     {},
		"option injection":   {Destination: "-f"},
		"bad source":         {Destination: "10.0.0.1", Source: "-I"},
		"negative count":     {Destination: "10.0.0.1", Count: -2},
		"short interval":     {Destination: "10.0.0.1", Interval: 1_000_000},
		"endless flood":      {Destination: "10.0.0.1", Interval: -1, Count: -1},
		"negative interval":  {Destination: "10.0.0.1", Interval: -2},
		"negative wait":      {Destination: "10.0.0.1", Wait: -1},
		"oversized packet":   {Destination: "10.0.0.1", Size: 70000},
		"bad network inst":   {Destination: "10.0.0.1", NetworkInstance: "a b"},
		"unknown l3protocol": {Destination: "10.0.0.1", L3Protocol: types.L3Protocol(9)},
	} {
		if _, _, err := pingCommand(req); err == nil {
			t.Errorf("%s: pingCommand() error = nil, want error", name)
		}
	}
}

// Output of iputils ping 20240905 on the H5.
var pingOutput = []string{
	"PING 198.51.100.1 (198.51.100.1) 56(84) bytes of data.",
	"64 bytes from 198.51.100.1: icmp_seq=1 ttl=64 time=0.269 ms",
	"64 bytes from 198.51.100.1: icmp_seq=2 ttl=64 time=0.270 ms",
	"64 bytes from 198.51.100.1: icmp_seq=3 ttl=64 time=0.295 ms",
	"",
	"--- 198.51.100.1 ping statistics ---",
	"3 packets transmitted, 3 received, 0% packet loss, time 411ms",
	"rtt min/avg/max/mdev = 0.269/0.278/0.295/0.012 ms",
}

func TestHandlePing(t *testing.T) {
	f := &fakeHostRun{lines: pingOutput}
	useFakeHostRun(t, f)
	stream := &fakePingStream{ctx: context.Background()}

	if err := HandlePing(&syspb.PingRequest{Destination: "198.51.100.1", Count: 3}, stream); err != nil {
		t.Fatalf("HandlePing() error = %v", err)
	}
	if f.name != "ping" {
		t.Errorf("command = %q, want ping", f.name)
	}
	want := []*syspb.PingResponse{
		{Source: "198.51.100.1", Bytes: 64, Sequence: 1, Ttl: 64, Time: 269000},
		{Source: "198.51.100.1", Bytes: 64, Sequence: 2, Ttl: 64, Time: 270000},
		{Source: "198.51.100.1", Bytes: 64, Sequence: 3, Ttl: 64, Time: 295000},
		{Sent: 3, Received: 3, Time: 411000000, MinTime: 269000, AvgTime: 278000, MaxTime: 295000, StdDev: 12000},
	}
	if len(stream.sent) != len(want) {
		t.Fatalf("sent %d responses, want %d: %v", len(stream.sent), len(want), stream.sent)
	}
	for i := range want {
		if !proto.Equal(stream.sent[i], want[i]) {
			t.Errorf("response %d = %v, want %v", i, stream.sent[i], want[i])
		}
	}
}

func TestHandlePingIPv6(t *testing.T) {
	useFakeHostRun(t, &fakeHostRun{lines: []string{
		"PING ::1 (::1) 56 data bytes",
		"64 bytes from ::1: icmp_seq=1 ttl=64 time=0.031 ms",
		"--- ::1 ping statistics ---",
		"1 packets transmitted, 1 received, 0% packet loss, time 0ms",
		"rtt min/avg/max/mdev = 0.031/0.031/0.031/0.000 ms",
	}})
	stream := &fakePingStream{ctx: context.Background()}
	if err := HandlePing(&syspb.PingRequest{Destination: "::1"}, stream); err != nil {
		t.Fatalf("HandlePing() error = %v", err)
	}
	if len(stream.sent) != 2 || stream.sent[0].Source != "::1" || stream.sent[0].Sequence != 1 {
		t.Errorf("responses = %v, want a reply from ::1 and a summary", stream.sent)
	}
}

func TestHandlePingNoReplies(t *testing.T) {
	useFakeHostRun(t, &fakeHostRun{exitCode: 1, lines: []string{
		"PING 192.0.2.1 (192.0.2.1) 56(84) bytes of data.",
		"",
		"--- 192.0.2.1 ping statistics ---",
		"2 packets transmitted, 0 received, 100% packet loss, time 201ms",
	}})
	stream := &fakePingStream{ctx: context.Background()}
	if err := HandlePing(&syspb.PingRequest{Destination: "192.0.2.1"}, stream); err != nil {
		t.Fatalf("HandlePing() error = %v", err)
	}
	if len(stream.sent) != 1 || stream.sent[0].Sent != 2 || stream.sent[0].Received != 0 {
		t.Errorf("responses = %v, want one summary with sent=2 received=0", stream.sent)
	}
}

func TestHandlePingFlood(t *testing.T) {
	useFakeHostRun(t, &fakeHostRun{lines: []string{
		"PING 198.51.100.1 (198.51.100.1) 56(84) bytes of data.",
		".\b \b.\b \b.\b \b",
		"--- 198.51.100.1 ping statistics ---",
		"3 packets transmitted, 3 received, 0% packet loss, time 5ms",
		"rtt min/avg/max/mdev = 0.227/0.245/0.290/0.014 ms, ipg/ewma 0.266/0.244 ms",
	}})
	stream := &fakePingStream{ctx: context.Background()}
	if err := HandlePing(&syspb.PingRequest{Destination: "198.51.100.1", Count: 3, Interval: -1}, stream); err != nil {
		t.Fatalf("HandlePing() error = %v", err)
	}
	want := &syspb.PingResponse{Sent: 3, Received: 3, Time: 5000000, MinTime: 227000, AvgTime: 245000, MaxTime: 290000, StdDev: 14000}
	if len(stream.sent) != 1 || !proto.Equal(stream.sent[0], want) {
		t.Errorf("responses = %v, want only the summary %v", stream.sent, want)
	}
}

func TestHandlePingUnknownHost(t *testing.T) {
	useFakeHostRun(t, &fakeHostRun{exitCode: 2, lines: []string{
		"ping: no.such.host.invalid: Name or service not known",
	}})
	err := HandlePing(&syspb.PingRequest{Destination: "no.such.host.invalid"}, &fakePingStream{ctx: context.Background()})
	wantCode(t, err, codes.InvalidArgument)
	if !strings.Contains(err.Error(), "Name or service not known") {
		t.Errorf("error = %v, want the ping message", err)
	}
}

func TestHandlePingInvalidRequest(t *testing.T) {
	err := HandlePing(&syspb.PingRequest{}, &fakePingStream{ctx: context.Background()})
	wantCode(t, err, codes.InvalidArgument)
}

func TestHandlePingSendError(t *testing.T) {
	useFakeHostRun(t, &fakeHostRun{lines: pingOutput})
	sendErr := status.Error(codes.Unavailable, "client gone")
	err := HandlePing(&syspb.PingRequest{Destination: "198.51.100.1"}, &fakePingStream{ctx: context.Background(), sendErr: sendErr})
	wantCode(t, err, codes.Unavailable)
}

func TestHandlePingCancelled(t *testing.T) {
	useFakeHostRun(t, &fakeHostRun{err: context.Canceled})
	err := HandlePing(&syspb.PingRequest{Destination: "10.0.0.1", Count: -1}, &fakePingStream{ctx: context.Background()})
	wantCode(t, err, codes.Canceled)
}

func TestHandlePingStartFailure(t *testing.T) {
	useFakeHostRun(t, &fakeHostRun{err: errors.New("nsenter: not found")})
	err := HandlePing(&syspb.PingRequest{Destination: "10.0.0.1"}, &fakePingStream{ctx: context.Background()})
	wantCode(t, err, codes.Internal)
}

func TestTracerouteCommand(t *testing.T) {
	tests := []struct {
		name     string
		req      *syspb.TracerouteRequest
		wantName string
		wantArgs []string
	}{
		{
			name:     "defaults",
			req:      &syspb.TracerouteRequest{Destination: "10.0.0.1"},
			wantName: "traceroute",
			wantArgs: []string{"-I", "-m", "30", "10.0.0.1"},
		},
		{
			name: "all options in a VRF",
			req: &syspb.TracerouteRequest{
				Destination:     "10.0.0.1",
				Source:          "10.0.0.2",
				InitialTtl:      2,
				MaxTtl:          10,
				Wait:            1_500_000_000,
				DoNotFragment:   true,
				DoNotResolve:    true,
				L3Protocol:      types.L3Protocol_IPV4,
				L4Protocol:      syspb.TracerouteRequest_TCP,
				NetworkInstance: "mgmt",
			},
			wantName: "ip",
			wantArgs: []string{"vrf", "exec", "mgmt", "traceroute", "-n", "-4", "-T", "-f", "2", "-m", "10", "-w", "1.5", "-F", "-s", "10.0.0.2", "10.0.0.1"},
		},
		{
			name:     "UDP with max_ttl -1",
			req:      &syspb.TracerouteRequest{Destination: "2001:db8::1", MaxTtl: -1, L3Protocol: types.L3Protocol_IPV6, L4Protocol: syspb.TracerouteRequest_UDP},
			wantName: "traceroute",
			wantArgs: []string{"-6", "-m", "255", "2001:db8::1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, args, err := tracerouteCommand(tt.req)
			if err != nil {
				t.Fatalf("tracerouteCommand() error = %v", err)
			}
			if name != tt.wantName || !reflect.DeepEqual(args, tt.wantArgs) {
				t.Errorf("tracerouteCommand() = %q %q, want %q %q", name, args, tt.wantName, tt.wantArgs)
			}
		})
	}
}

func TestTracerouteCommandRejects(t *testing.T) {
	for name, req := range map[string]*syspb.TracerouteRequest{
		"no destination":       {},
		"option injection":     {Destination: "-n"},
		"bad source":           {Destination: "10.0.0.1", Source: "a b"},
		"max_ttl too large":    {Destination: "10.0.0.1", MaxTtl: 256},
		"max_ttl negative":     {Destination: "10.0.0.1", MaxTtl: -2},
		"initial above max":    {Destination: "10.0.0.1", InitialTtl: 31},
		"negative wait":        {Destination: "10.0.0.1", Wait: -1},
		"unknown l4protocol":   {Destination: "10.0.0.1", L4Protocol: syspb.TracerouteRequest_L4Protocol(7)},
		"bad network instance": {Destination: "10.0.0.1", NetworkInstance: "-x"},
	} {
		if _, _, err := tracerouteCommand(req); err == nil {
			t.Errorf("%s: tracerouteCommand() error = nil, want error", name)
		}
	}
}

func TestTracerouteParseHops(t *testing.T) {
	var p tracerouteParser
	header := p.parse("traceroute to 203.0.113.168 (203.0.113.168), 30 hops max, 60 byte packets")
	if len(header) != 1 || header[0].DestinationAddress != "203.0.113.168" || header[0].Hops != 30 || header[0].PacketSize != 60 {
		t.Fatalf("header = %v", header)
	}

	tests := []struct {
		line string
		want []*syspb.TracerouteResponse
	}{
		{
			line: " 1  203.0.113.168  0.724 ms  0.699 ms  0.740 ms",
			want: []*syspb.TracerouteResponse{
				{Hop: 1, Address: "203.0.113.168", Rtt: 724000},
				{Hop: 1, Address: "203.0.113.168", Rtt: 699000},
				{Hop: 1, Address: "203.0.113.168", Rtt: 740000},
			},
		},
		{
			line: " 1  router.example.net (203.0.113.168)  0.461 ms  0.443 ms",
			want: []*syspb.TracerouteResponse{
				{Hop: 1, Address: "203.0.113.168", Name: "router.example.net", Rtt: 461000},
				{Hop: 1, Address: "203.0.113.168", Name: "router.example.net", Rtt: 443000},
			},
		},
		{
			line: " 2  * * *",
			want: []*syspb.TracerouteResponse{
				{Hop: 2, State: syspb.TracerouteResponse_NONE},
				{Hop: 2, State: syspb.TracerouteResponse_NONE},
				{Hop: 2, State: syspb.TracerouteResponse_NONE},
			},
		},
		{
			line: " 3  10.1.1.1  1.2 ms !H  10.1.1.2  1.3 ms !<13>  *",
			want: []*syspb.TracerouteResponse{
				{Hop: 3, Address: "10.1.1.1", Rtt: 1200000, State: syspb.TracerouteResponse_HOST_UNREACHABLE},
				{Hop: 3, Address: "10.1.1.2", Rtt: 1300000, State: syspb.TracerouteResponse_ICMP, IcmpCode: 13},
				{Hop: 3, State: syspb.TracerouteResponse_NONE},
			},
		},
		{
			line: " 4  2001:db8::1  5.0 ms !F-1400  2001:db8::1 (2001:db8::1)  5.1 ms !X",
			want: []*syspb.TracerouteResponse{
				{Hop: 4, Address: "2001:db8::1", Rtt: 5000000, State: syspb.TracerouteResponse_FRAGMENTATION_NEEDED},
				{Hop: 4, Address: "2001:db8::1", Rtt: 5100000, State: syspb.TracerouteResponse_PROHIBITED},
			},
		},
	}
	for _, tt := range tests {
		got := p.parse(tt.line)
		if len(got) != len(tt.want) {
			t.Errorf("parse(%q) = %v, want %v", tt.line, got, tt.want)
			continue
		}
		for i := range got {
			if !proto.Equal(got[i], tt.want[i]) {
				t.Errorf("parse(%q)[%d] = %v, want %v", tt.line, i, got[i], tt.want[i])
			}
		}
	}
}

func TestHandleTraceroute(t *testing.T) {
	f := &fakeHostRun{lines: []string{
		"traceroute to 203.0.113.168 (203.0.113.168), 4 hops max, 60 byte packets",
		" 1  203.0.113.168  0.724 ms  0.699 ms  0.740 ms",
	}}
	useFakeHostRun(t, f)
	stream := &fakeTracerouteStream{ctx: context.Background()}

	req := &syspb.TracerouteRequest{Destination: "203.0.113.168", MaxTtl: 4, DoNotResolve: true}
	if err := HandleTraceroute(req, stream); err != nil {
		t.Fatalf("HandleTraceroute() error = %v", err)
	}
	if f.name != "traceroute" {
		t.Errorf("command = %q, want traceroute", f.name)
	}
	if len(stream.sent) != 4 || stream.sent[0].Hops != 4 || stream.sent[1].Hop != 1 {
		t.Errorf("responses = %v, want the header and 3 probes", stream.sent)
	}
}

func TestHandleTracerouteUnknownHost(t *testing.T) {
	useFakeHostRun(t, &fakeHostRun{exitCode: 2, lines: []string{
		"no.such.host.invalid: Name or service not known",
		"Cannot handle \"host\" cmdline arg `no.such.host.invalid' on position 1 (argc 2)",
	}})
	err := HandleTraceroute(&syspb.TracerouteRequest{Destination: "no.such.host.invalid"}, &fakeTracerouteStream{ctx: context.Background()})
	wantCode(t, err, codes.InvalidArgument)
}

func TestHandleTracerouteInvalidRequest(t *testing.T) {
	err := HandleTraceroute(&syspb.TracerouteRequest{}, &fakeTracerouteStream{ctx: context.Background()})
	wantCode(t, err, codes.InvalidArgument)
}
