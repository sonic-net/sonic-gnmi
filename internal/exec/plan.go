package exec

import "time"

// ExecutionPlan is a fully validated, server-owned command invocation.
type ExecutionPlan struct {
	Executable  string
	Args        []string
	User        string
	Namespaces  []string
	Timeout     time.Duration
	OutputLimit int64
}
