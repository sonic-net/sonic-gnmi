package client

import (
	"context"
	"time"
)

// FakeTailer is a Day-1 stub Tailer that emits a fixed list of RawLines then
// returns. WS2 replaces this with the real file tailer/replay implementation.
type FakeTailer struct {
	Lines []RawLine
}

// NewSampleFakeTailer returns a FakeTailer that emits the design-doc sample
// Record as one JSON RawLine (consumed by PassThroughParser).
func NewSampleFakeTailer() *FakeTailer {
	r := sampleRecord()
	line, err := marshalRecordJSON(r)
	if err != nil {
		// sampleRecord is fixed; marshal failure would be a programming error.
		panic(err)
	}
	return &FakeTailer{
		Lines: []RawLine{{
			Source: r.Source,
			Seq:    r.Seq,
			Line:   string(line),
		}},
	}
}

// Run sends each configured line (honouring backpressure on out), then returns.
// It ignores `from`; real replay is WS2's job.
func (t *FakeTailer) Run(ctx context.Context, from time.Time, out chan<- RawLine) error {
	_ = from
	for _, l := range t.Lines {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case out <- l:
		}
	}
	return nil
}
