package client

import (
	"encoding/json"
	"time"
)

// recordTSLayout is the local fractional timestamp used in Record JSON (no zone).
const recordTSLayout = "2006-01-02T15:04:05.000000"

// PassThroughParser is a Day-1 stub Parser. If Line is JSON for a Record it
// decodes it; otherwise it returns a minimal Record with Seq/Source/Raw filled
// from the RawLine. WS3 replaces this with swss/sairedis line parsers.
type PassThroughParser struct{}

// Parse implements Parser.
func (PassThroughParser) Parse(l RawLine) (*Record, bool) {
	if l.Line == "" {
		return nil, false
	}

	var aux struct {
		Seq       string            `json:"seq"`
		TS        string            `json:"ts"`
		Source    string            `json:"source"`
		DB        string            `json:"db"`
		Table     string            `json:"table"`
		Key       string            `json:"key"`
		Op        string            `json:"op"`
		Fields    map[string]string `json:"fields"`
		Status    string            `json:"status"`
		Raw       string            `json:"raw"`
		MatchedBy string            `json:"matched_by"`
	}
	if err := json.Unmarshal([]byte(l.Line), &aux); err == nil &&
		(aux.Seq != "" || aux.Source != "" || aux.Table != "" || aux.DB != "") {
		var ts time.Time
		if aux.TS != "" {
			ts, _ = time.ParseInLocation(recordTSLayout, aux.TS, time.Local)
		}
		src := aux.Source
		if src == "" {
			src = l.Source
		}
		seq := aux.Seq
		if seq == "" {
			seq = l.Seq
		}
		raw := aux.Raw
		if raw == "" {
			raw = l.Line
		}
		return &Record{
			Seq:       seq,
			TS:        ts,
			Source:    src,
			DB:        aux.DB,
			Table:     aux.Table,
			Key:       aux.Key,
			Op:        aux.Op,
			Fields:    aux.Fields,
			Status:    aux.Status,
			Raw:       raw,
			MatchedBy: aux.MatchedBy,
		}, true
	}

	return &Record{
		Seq:    l.Seq,
		Source: l.Source,
		Raw:    l.Line,
	}, true
}
