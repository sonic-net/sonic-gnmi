package client

import (
	"context"
	"time"
)

// Record is one orchagent/sairedis recorder line, ready for gNMI encoding.
// See the RECORDS target design: disk is the queue; each matched line becomes
// one Notification with this payload as JSON_IETF TypedValue.
type Record struct {
	Seq       string            `json:"seq"`
	TS        time.Time         `json:"ts"`
	Source    string            `json:"source"` // "swss" | "sairedis"
	DB        string            `json:"db"`     // "APPL_DB" | "ASIC_DB"
	Table     string            `json:"table"`
	Key       string            `json:"key"`
	Op        string            `json:"op"`
	Fields    map[string]string `json:"fields"`
	Status    string            `json:"status"`
	Raw       string            `json:"raw,omitempty"`
	MatchedBy string            `json:"matched_by,omitempty"`
}

// RawLine is one unread recorder line from a Tailer (WS2 → WS1/WS3).
type RawLine struct {
	Source string // "swss" | "sairedis"
	Seq    string
	Line   string
}

// Tailer replays from `from` (zero = live only) then tails until ctx is cancelled.
// WS2 owns the real implementation; WS1 consumes this interface.
type Tailer interface {
	Run(ctx context.Context, from time.Time, out chan<- RawLine) error
}

// Parser turns a RawLine into a Record. false means skip the line.
// WS3 owns the real implementation; WS1 consumes this interface.
type Parser interface {
	Parse(l RawLine) (*Record, bool)
}

// Matcher decides whether a subscriber wanted a Record; how becomes matched_by.
// WS3 owns the real implementation; WS1 consumes this interface.
type Matcher interface {
	Match(r *Record) (ok bool, how string)
}
