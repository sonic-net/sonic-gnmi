package system

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	log "github.com/golang/glog"
	syspb "github.com/openconfig/gnoi/system"
	"github.com/openconfig/gnoi/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	defaultTracerouteMaxTTL = 30
	// traceroute does not go beyond 255 hops, so that is what -1 (forever) means.
	maxTracerouteTTL = 255
)

var (
	tracerouteHeaderRe = regexp.MustCompile(`^traceroute to (\S+) \(([^)]+)\), (\d+) hops max, (\d+) byte packets`)
	tracerouteHopRe    = regexp.MustCompile(`^\s*(\d+)\s+(.*)$`)
)

// tracerouteAnnotations maps the "!X" markers traceroute prints after a probe.
var tracerouteAnnotations = map[string]syspb.TracerouteResponse_State{
	"!H": syspb.TracerouteResponse_HOST_UNREACHABLE,
	"!N": syspb.TracerouteResponse_NETWORK_UNREACHABLE,
	"!P": syspb.TracerouteResponse_PROTOCOL_UNREACHABLE,
	"!S": syspb.TracerouteResponse_SOURCE_ROUTE_FAILED,
	"!X": syspb.TracerouteResponse_PROHIBITED,
	"!V": syspb.TracerouteResponse_PRECEDENCE_VIOLATION,
	"!C": syspb.TracerouteResponse_PRECEDENCE_CUTOFF,
}

// tracerouteCommand returns the host command for a Traceroute request.
func tracerouteCommand(req *syspb.TracerouteRequest) (string, []string, error) {
	if req.GetDestination() == "" {
		return "", nil, fmt.Errorf("destination is required")
	}
	if err := validateHostArg("destination", req.GetDestination()); err != nil {
		return "", nil, err
	}
	vrf, err := hostVrf(req.GetNetworkInstance())
	if err != nil {
		return "", nil, err
	}

	// AS path lookups (traceroute -A) query whois servers on the Internet, so
	// they are never done and as_path is left empty.
	var args []string
	if req.GetDoNotResolve() {
		args = append(args, "-n")
	}
	switch req.GetL3Protocol() {
	case types.L3Protocol_UNSPECIFIED:
	case types.L3Protocol_IPV4:
		args = append(args, "-4")
	case types.L3Protocol_IPV6:
		args = append(args, "-6")
	default:
		return "", nil, fmt.Errorf("unsupported l3protocol %v", req.GetL3Protocol())
	}
	switch req.GetL4Protocol() {
	case syspb.TracerouteRequest_ICMP:
		args = append(args, "-I")
	case syspb.TracerouteRequest_TCP:
		args = append(args, "-T")
	case syspb.TracerouteRequest_UDP:
		// UDP is what traceroute uses by default.
	default:
		return "", nil, fmt.Errorf("unsupported l4protocol %v", req.GetL4Protocol())
	}

	maxTTL := defaultTracerouteMaxTTL
	switch m := req.GetMaxTtl(); {
	case m == 0:
	case m == -1:
		maxTTL = maxTracerouteTTL
	case m < 0 || m > maxTracerouteTTL:
		return "", nil, fmt.Errorf("max_ttl %d is out of range 1-%d", m, maxTracerouteTTL)
	default:
		maxTTL = int(m)
	}
	if initial := req.GetInitialTtl(); initial != 0 {
		if initial > uint32(maxTTL) {
			return "", nil, fmt.Errorf("initial_ttl %d is above max_ttl %d", initial, maxTTL)
		}
		args = append(args, "-f", strconv.Itoa(int(initial)))
	}
	args = append(args, "-m", strconv.Itoa(maxTTL))

	if wait := req.GetWait(); wait != 0 {
		if wait < 0 {
			return "", nil, fmt.Errorf("invalid wait %d", wait)
		}
		args = append(args, "-w", nsToSeconds(wait))
	}
	if req.GetDoNotFragment() {
		args = append(args, "-F")
	}
	if src := req.GetSource(); src != "" {
		if err := validateHostArg("source", src); err != nil {
			return "", nil, err
		}
		args = append(args, "-s", src)
	}
	args = append(args, req.GetDestination())

	name, args := inVrf(vrf, "traceroute", args)
	return name, args, nil
}

// tracerouteParser turns traceroute output into TracerouteResponses.
type tracerouteParser struct {
	sawHeader bool
	// Lines that are neither the header nor hops, such as errors.
	other []string
}

// parse returns the responses on line: the request summary for the header, or
// one response per probe for a hop line.
func (p *tracerouteParser) parse(line string) []*syspb.TracerouteResponse {
	if m := tracerouteHeaderRe.FindStringSubmatch(line); m != nil {
		p.sawHeader = true
		resp := &syspb.TracerouteResponse{DestinationName: m[1], DestinationAddress: m[2]}
		if v, err := strconv.Atoi(m[3]); err == nil {
			resp.Hops = int32(v)
		}
		if v, err := strconv.Atoi(m[4]); err == nil {
			resp.PacketSize = int32(v)
		}
		return []*syspb.TracerouteResponse{resp}
	}
	m := tracerouteHopRe.FindStringSubmatch(line)
	if !p.sawHeader || m == nil {
		if line = strings.TrimSpace(line); line != "" {
			p.other = append(p.other, line)
		}
		return nil
	}
	hop, err := strconv.Atoi(m[1])
	if err != nil {
		return nil
	}

	// A hop line lists its probes, each "* " or "[name (addr) | addr] rtt ms
	// [!X]". The address is only printed when it differs from the last probe.
	var probes []*syspb.TracerouteResponse
	var address, name string
	tokens := strings.Fields(m[2])
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		switch {
		case tok == "*":
			probes = append(probes, &syspb.TracerouteResponse{Hop: int32(hop), State: syspb.TracerouteResponse_NONE})
		case strings.HasPrefix(tok, "!"):
			if len(probes) > 0 {
				setTracerouteAnnotation(probes[len(probes)-1], tok)
			}
		case i+1 < len(tokens) && tokens[i+1] == "ms":
			i++
			if rtt, ok := msToNs(tok); ok {
				probes = append(probes, &syspb.TracerouteResponse{Hop: int32(hop), Address: address, Name: name, Rtt: rtt})
			}
		case strings.HasPrefix(tok, "(") && strings.HasSuffix(tok, ")"):
			name = address
			address = strings.Trim(tok, "()")
			if name == address {
				name = ""
			}
		case strings.HasPrefix(tok, "<"):
			// Extra probe details, such as <syn,ack> for TCP.
		default:
			address, name = tok, ""
		}
	}
	return probes
}

// setTracerouteAnnotation records a "!X" marker on the probe it follows.
func setTracerouteAnnotation(resp *syspb.TracerouteResponse, tok string) {
	if strings.HasPrefix(tok, "!F") {
		// Printed as !F-<mtu> when the MTU is known.
		resp.State = syspb.TracerouteResponse_FRAGMENTATION_NEEDED
		return
	}
	if state, ok := tracerouteAnnotations[tok]; ok {
		resp.State = state
		return
	}
	// Other ICMP codes are printed as !<code>.
	if code, err := strconv.Atoi(strings.Trim(tok, "!<>")); err == nil {
		resp.State = syspb.TracerouteResponse_ICMP
		resp.IcmpCode = int32(code)
		return
	}
	resp.State = syspb.TracerouteResponse_UNKNOWN
}

// HandleTraceroute implements System.Traceroute. It runs traceroute on the
// host and streams the request summary, then one response per probe.
func HandleTraceroute(req *syspb.TracerouteRequest, stream syspb.System_TracerouteServer) error {
	name, args, err := tracerouteCommand(req)
	if err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	log.V(1).Infof("gNOI: Traceroute running %s %v", name, args)

	var p tracerouteParser
	exitCode, err := runHostCommandStream(stream.Context(), name, args, hostCommandOpts, func(line string) error {
		for _, resp := range p.parse(line) {
			if err := stream.Send(resp); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return runError("traceroute", err)
	}
	if exitCode != 0 && !p.sawHeader {
		return commandError("traceroute", exitCode, p.other)
	}
	return nil
}
