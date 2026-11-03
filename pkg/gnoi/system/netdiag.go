package system

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/sonic-net/sonic-gnmi/pkg/exec"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// runHostCommandStream is replaced in tests.
var runHostCommandStream = exec.RunHostCommandStream

// hostCommandOpts enters the host's cgroup namespace because "ip vrf exec"
// joins a VRF cgroup, which fails from the container's own cgroup namespace.
// The pid namespace is left out so that nsenter execs the command and
// cancelling the RPC kills it.
var hostCommandOpts = &exec.RunHostCommandOptions{
	Namespaces: []string{"mount", "uts", "ipc", "net", "cgroup"},
}

var (
	// Host names, IPv4 and IPv6 addresses (with an optional %zone).
	hostArgRe = regexp.MustCompile(`^[A-Za-z0-9_.:%-]+$`)
	// Linux interface names, which VRF devices are, are at most 15 bytes.
	vrfNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,15}$`)
)

// validateHostArg rejects anything that is not a plain host name or address,
// so that the value cannot be read as an option by ping or traceroute.
func validateHostArg(field, value string) error {
	if len(value) > 255 || !hostArgRe.MatchString(value) || strings.HasPrefix(value, "-") {
		return fmt.Errorf("invalid %s %q", field, value)
	}
	return nil
}

// hostVrf returns the Linux VRF for a gNOI network instance, or "" for the
// default instance.
func hostVrf(networkInstance string) (string, error) {
	switch networkInstance {
	case "", "default", "DEFAULT":
		return "", nil
	}
	if !vrfNameRe.MatchString(networkInstance) || strings.HasPrefix(networkInstance, "-") {
		return "", fmt.Errorf("invalid network_instance %q", networkInstance)
	}
	return networkInstance, nil
}

// inVrf returns the host command that runs name with args in vrf. Outside
// batch mode "ip vrf exec" execs the tool rather than forking it, so
// cancelling the RPC still kills the tool.
func inVrf(vrf, name string, args []string) (string, []string) {
	if vrf == "" {
		return name, args
	}
	return "ip", append([]string{"vrf", "exec", vrf, name}, args...)
}

// nsToSeconds formats a duration in nanoseconds as the seconds value that ping
// and traceroute take.
func nsToSeconds(ns int64) string {
	return strconv.FormatFloat(float64(ns)/1e9, 'f', -1, 64)
}

// msToNs converts a millisecond value printed by ping or traceroute.
func msToNs(ms string) (int64, bool) {
	v, err := strconv.ParseFloat(ms, 64)
	if err != nil {
		return 0, false
	}
	return int64(math.Round(v * 1e6)), true
}

// runError turns an error from running ping or traceroute (the client went
// away, a failed Send, or the command did not start) into a gRPC error.
func runError(tool string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return status.FromContextError(err).Err()
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	return status.Errorf(codes.Internal, "%s: %v", tool, err)
}

// commandError turns a failed ping or traceroute run into a gRPC error that
// carries the tool's own message.
func commandError(tool string, exitCode int, output []string) error {
	msg := strings.Join(output, "; ")
	if msg == "" {
		msg = fmt.Sprintf("exit code %d", exitCode)
	}
	// Both tools exit with 2 for bad arguments and unresolvable hosts.
	if exitCode == 2 {
		return status.Errorf(codes.InvalidArgument, "%s: %s", tool, msg)
	}
	return status.Errorf(codes.Internal, "%s: %s", tool, msg)
}
