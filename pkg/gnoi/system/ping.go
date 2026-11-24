package system

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	log "github.com/golang/glog"
	syspb "github.com/openconfig/gnoi/system"
	"github.com/openconfig/gnoi/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	defaultPingCount = 5
	// Smallest interval accepted, to keep a client from flooding the network.
	minPingInterval = int64(10 * time.Millisecond)
	maxPingSize     = 65507
)

var (
	pingReplyRe = regexp.MustCompile(`^(\d+) bytes from (\S+): icmp_seq=(\d+)(?: ttl=(\d+))?(?: time=([\d.]+) ms)?`)
	pingSentRe  = regexp.MustCompile(`^(\d+) packets transmitted, (\d+) received`)
	pingTimeRe  = regexp.MustCompile(`, time (\d+)ms`)
	pingRttRe   = regexp.MustCompile(`^(?:rtt|round-trip) min/avg/max/(?:mdev|stddev) = ([\d.]+)/([\d.]+)/([\d.]+)/([\d.]+) ms`)
)

// pingCommand returns the host command for a Ping request.
func pingCommand(req *syspb.PingRequest) (string, []string, error) {
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

	// Replies are always reported by address: PingResponse has no name field.
	args := []string{"-n"}
	switch req.GetL3Protocol() {
	case types.L3Protocol_UNSPECIFIED:
	case types.L3Protocol_IPV4:
		args = append(args, "-4")
	case types.L3Protocol_IPV6:
		args = append(args, "-6")
	default:
		return "", nil, fmt.Errorf("unsupported l3protocol %v", req.GetL3Protocol())
	}

	switch count := req.GetCount(); {
	case count == 0:
		args = append(args, "-c", strconv.Itoa(defaultPingCount))
	case count == -1:
		// Ping until the client cancels the RPC.
	case count < 0:
		return "", nil, fmt.Errorf("invalid count %d", count)
	default:
		args = append(args, "-c", strconv.Itoa(int(count)))
	}

	switch interval := req.GetInterval(); {
	case interval == 0:
	case interval == -1:
		// A flood ping prints no per-reply lines, so only the summary is sent.
		// An endless flood is refused.
		if req.GetCount() == -1 {
			return "", nil, fmt.Errorf("a flood ping (interval -1) needs a count")
		}
		args = append(args, "-f")
	default:
		if interval < minPingInterval {
			return "", nil, fmt.Errorf("interval %dns is below the minimum of %dns", interval, minPingInterval)
		}
		args = append(args, "-i", nsToSeconds(interval))
	}
	if wait := req.GetWait(); wait != 0 {
		if wait < 0 {
			return "", nil, fmt.Errorf("invalid wait %d", wait)
		}
		args = append(args, "-W", nsToSeconds(wait))
	}
	if size := req.GetSize(); size != 0 {
		if size < 0 || size > maxPingSize {
			return "", nil, fmt.Errorf("size %d is out of range 0-%d", size, maxPingSize)
		}
		args = append(args, "-s", strconv.Itoa(int(size)))
	}
	if req.GetDoNotFragment() {
		args = append(args, "-M", "do")
	}
	if src := req.GetSource(); src != "" {
		if err := validateHostArg("source", src); err != nil {
			return "", nil, err
		}
		args = append(args, "-I", src)
	}
	args = append(args, req.GetDestination())

	name, args := inVrf(vrf, "ping", args)
	return name, args, nil
}

// pingParser turns ping output into PingResponses.
type pingParser struct {
	summary *syspb.PingResponse
	// Lines that are neither replies nor statistics, such as errors.
	other []string
}

// parse returns the reply on line, or nil if line is not a reply.
func (p *pingParser) parse(line string) *syspb.PingResponse {
	if m := pingReplyRe.FindStringSubmatch(line); m != nil {
		resp := &syspb.PingResponse{Source: m[2]}
		if v, err := strconv.Atoi(m[1]); err == nil {
			resp.Bytes = int32(v)
		}
		if v, err := strconv.Atoi(m[3]); err == nil {
			resp.Sequence = int32(v)
		}
		if v, err := strconv.Atoi(m[4]); err == nil {
			resp.Ttl = int32(v)
		}
		if ns, ok := msToNs(m[5]); ok {
			resp.Time = ns
		}
		return resp
	}
	if m := pingSentRe.FindStringSubmatch(line); m != nil {
		p.summary = &syspb.PingResponse{}
		if v, err := strconv.Atoi(m[1]); err == nil {
			p.summary.Sent = int32(v)
		}
		if v, err := strconv.Atoi(m[2]); err == nil {
			p.summary.Received = int32(v)
		}
		if t := pingTimeRe.FindStringSubmatch(line); t != nil {
			if ns, ok := msToNs(t[1]); ok {
				p.summary.Time = ns
			}
		}
		return nil
	}
	if m := pingRttRe.FindStringSubmatch(line); m != nil {
		if p.summary != nil {
			p.summary.MinTime, _ = msToNs(m[1])
			p.summary.AvgTime, _ = msToNs(m[2])
			p.summary.MaxTime, _ = msToNs(m[3])
			p.summary.StdDev, _ = msToNs(m[4])
		}
		return nil
	}
	if line = strings.TrimSpace(line); line != "" &&
		!strings.HasPrefix(line, "PING ") && !strings.HasPrefix(line, "--- ") {
		p.other = append(p.other, line)
	}
	return nil
}

// HandlePing implements System.Ping. It runs ping on the host and streams one
// response per reply, then the summary statistics.
func HandlePing(req *syspb.PingRequest, stream syspb.System_PingServer) error {
	name, args, err := pingCommand(req)
	if err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	log.V(1).Infof("gNOI: Ping running %s %v", name, args)

	var p pingParser
	exitCode, err := runHostCommandStream(stream.Context(), name, args, hostCommandOpts, func(line string) error {
		if resp := p.parse(line); resp != nil {
			return stream.Send(resp)
		}
		return nil
	})
	if err != nil {
		return runError("ping", err)
	}
	// ping exits with 1 when nothing answered, which is still a valid result.
	if p.summary == nil {
		return commandError("ping", exitCode, p.other)
	}
	return stream.Send(p.summary)
}
