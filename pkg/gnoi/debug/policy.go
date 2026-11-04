package debug

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/google/shlex"
	exec "github.com/sonic-net/sonic-gnmi/internal/exec"
	"gopkg.in/yaml.v3"
)

const (
	PolicyFilePath = "/etc/sonic/command_whitelist.yaml"
	policyVersion  = 1
)

var (
	ErrInvalidRequest    = errors.New("invalid debug request")
	ErrPolicyUnavailable = errors.New("debug command policy unavailable")
	ErrRejected          = errors.New("debug action rejected by policy")
)

type AccessLevel uint8

const (
	AccessReadOnly AccessLevel = iota + 1
	AccessReadWrite
)

type policyFile struct {
	Version        int      `yaml:"version"`
	EnabledActions []string `yaml:"enabled_actions"`
}

type actionSpec struct {
	request    []string
	executable string
	args       []string
	minAccess  AccessLevel
	user       string
	namespaces []string
	maxTimeout time.Duration
	maxOutput  int64
}

type Policy struct {
	actions map[string]actionSpec
	loadErr error
}

func actionRegistry() map[string]actionSpec {
	return map[string]actionSpec{
		"uptime": {
			request:    []string{"uptime"},
			executable: "/usr/bin/uptime",
			minAccess:  AccessReadOnly,
			user:       "admin",
			namespaces: []string{"mount"},
			maxTimeout: 10 * time.Second,
			maxOutput:  64 * 1024,
		},
		"process-list": {
			request:    []string{"ps"},
			executable: "/usr/bin/ps",
			args:       []string{"-ef"},
			minAccess:  AccessReadOnly,
			user:       "admin",
			namespaces: []string{"mount"},
			maxTimeout: 10 * time.Second,
			maxOutput:  256 * 1024,
		},
	}
}

func LoadPolicy(path string) (*Policy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: read policy: %v", ErrPolicyUnavailable, err)
	}

	var config policyFile
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("%w: decode policy: %v", ErrPolicyUnavailable, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("%w: multiple YAML documents are not allowed", ErrPolicyUnavailable)
		}
		return nil, fmt.Errorf("%w: decode trailing policy data: %v", ErrPolicyUnavailable, err)
	}
	if config.Version != policyVersion {
		return nil, fmt.Errorf("%w: unsupported version %d", ErrPolicyUnavailable, config.Version)
	}

	registry := actionRegistry()
	actions := make(map[string]actionSpec, len(config.EnabledActions))
	for _, id := range config.EnabledActions {
		spec, ok := registry[id]
		if !ok {
			return nil, fmt.Errorf("%w: unknown action %q", ErrPolicyUnavailable, id)
		}
		if _, exists := actions[id]; exists {
			return nil, fmt.Errorf("%w: duplicate action %q", ErrPolicyUnavailable, id)
		}
		actions[id] = spec
	}

	return &Policy{actions: actions}, nil
}

func NewUnavailablePolicy(err error) *Policy {
	if err == nil {
		err = ErrPolicyUnavailable
	}
	return &Policy{loadErr: err}
}

func (p *Policy) Resolve(command string, access AccessLevel, timeoutNanos, byteLimit int64) (exec.ExecutionPlan, error) {
	if p == nil || p.loadErr != nil {
		return exec.ExecutionPlan{}, fmt.Errorf("%w: %v", ErrPolicyUnavailable, policyError(p))
	}
	if access != AccessReadOnly && access != AccessReadWrite {
		return exec.ExecutionPlan{}, fmt.Errorf("%w: invalid access level", ErrRejected)
	}

	argv, err := shlex.Split(command)
	if err != nil {
		return exec.ExecutionPlan{}, fmt.Errorf("%w: parse command: %v", ErrInvalidRequest, err)
	}
	if len(argv) == 0 || strings.Join(argv, " ") != command {
		return exec.ExecutionPlan{}, fmt.Errorf("%w: command must use canonical action syntax", ErrInvalidRequest)
	}

	var matched actionSpec
	found := false
	for _, spec := range p.actions {
		if slices.Equal(argv, spec.request) {
			matched = spec
			found = true
			break
		}
	}
	if !found {
		return exec.ExecutionPlan{}, fmt.Errorf("%w: action is not enabled", ErrRejected)
	}
	if access < matched.minAccess {
		return exec.ExecutionPlan{}, fmt.Errorf("%w: action requires stronger authorization", ErrRejected)
	}

	timeout, err := boundedDuration(timeoutNanos, matched.maxTimeout)
	if err != nil {
		return exec.ExecutionPlan{}, err
	}
	outputLimit, err := boundedLimit(byteLimit, matched.maxOutput)
	if err != nil {
		return exec.ExecutionPlan{}, err
	}

	return exec.ExecutionPlan{
		Executable:  matched.executable,
		Args:        append([]string(nil), matched.args...),
		User:        matched.user,
		Namespaces:  append([]string(nil), matched.namespaces...),
		Timeout:     timeout,
		OutputLimit: outputLimit,
	}, nil
}

func policyError(p *Policy) error {
	if p == nil {
		return errors.New("nil policy")
	}
	return p.loadErr
}

func boundedDuration(requested int64, maximum time.Duration) (time.Duration, error) {
	if requested < 0 {
		return 0, fmt.Errorf("%w: timeout cannot be negative", ErrInvalidRequest)
	}
	if requested == 0 {
		return maximum, nil
	}
	value := time.Duration(requested)
	if value > maximum {
		return 0, fmt.Errorf("%w: timeout exceeds action limit", ErrInvalidRequest)
	}
	return value, nil
}

func boundedLimit(requested, maximum int64) (int64, error) {
	if requested < 0 {
		return 0, fmt.Errorf("%w: output limit cannot be negative", ErrInvalidRequest)
	}
	if requested == 0 {
		return maximum, nil
	}
	if requested > maximum {
		return 0, fmt.Errorf("%w: output limit exceeds action limit", ErrInvalidRequest)
	}
	return requested, nil
}
