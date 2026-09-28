package client

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	log "github.com/golang/glog"
)

// ---------------------------------------------------------------------------
// FakeTailer (Day-1 stub, kept for WS1/WS4 tests)
// ---------------------------------------------------------------------------

// FakeTailer is a Day-1 stub Tailer that emits a fixed list of RawLines then
// returns. FileTailer is the real implementation; FakeTailer stays as a test
// seam for RecordsClient.
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
// It ignores `from`; replay is FileTailer's job.
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

// ---------------------------------------------------------------------------
// Package configuration (wired from telemetry flags -records_dir/-records_tz)
// ---------------------------------------------------------------------------

const (
	// RecordsSourceSwss is the swss recorder (APPL_DB operations).
	RecordsSourceSwss = "swss"
	// RecordsSourceSairedis is the sairedis recorder (SAI calls).
	RecordsSourceSairedis = "sairedis"
	// RecordsSourceControl marks a RawLine that is not from a file but a
	// tailer control message. Line is JSON: {"event":"live"|"gap",...}.
	// Parsers must skip it; RecordsClient passes it through as a Record.
	RecordsSourceControl = "control"

	// RecordsDefaultDir is where the gnmi container sees the host's record
	// files through the read-only /mnt/host mount.
	RecordsDefaultDir = "/mnt/host/var/log/swss"

	// RecordsDefaultNamespace is what the operator types on a single ASIC box.
	RecordsDefaultNamespace = "localhost"

	// recordsFileTSLayout is the recorder's localtime stamp: 2026-09-26.10:15:32.123456
	recordsFileTSLayout = "2006-01-02.15:04:05.000000"

	// recordsDefaultMaxLine bounds one record line. sairedis bulk records can
	// be hundreds of KB; anything past this is dropped, never buffered further.
	recordsDefaultMaxLine = 4 << 20

	recordsDefaultPollInterval = 200 * time.Millisecond
	recordsDefaultRetryMissing = time.Second
	recordsReaderBufSize       = 64 << 10

	// maxRelativeDays keeps "-Nd" inside time.Duration's range (~292 years).
	maxRelativeDays = 100000
)

var (
	recordsCfgMu sync.RWMutex
	recordsDir   = RecordsDefaultDir
	recordsLoc   = time.Local

	asicNamespaceRe = regexp.MustCompile(`^asic[0-9]+$`)
)

// SetRecordsDir sets the directory holding swss.rec / sairedis.rec files.
// Empty keeps the current value.
func SetRecordsDir(dir string) {
	if dir == "" {
		return
	}
	recordsCfgMu.Lock()
	defer recordsCfgMu.Unlock()
	recordsDir = dir
}

// RecordsDir returns the configured record file directory.
func RecordsDir() string {
	recordsCfgMu.RLock()
	defer recordsCfgMu.RUnlock()
	return recordsDir
}

// SetRecordsTZ sets the zone used to interpret the zone-less localtime stamps
// in the record files. Empty means the process local zone (the container is
// expected to inherit the host TZ).
func SetRecordsTZ(name string) error {
	loc := time.Local
	if name != "" {
		l, err := time.LoadLocation(name)
		if err != nil {
			return fmt.Errorf("records: bad timezone %q: %w", name, err)
		}
		loc = l
	}
	recordsCfgMu.Lock()
	defer recordsCfgMu.Unlock()
	recordsLoc = loc
	return nil
}

// RecordsLocation returns the zone used for record timestamps.
func RecordsLocation() *time.Location {
	recordsCfgMu.RLock()
	defer recordsCfgMu.RUnlock()
	return recordsLoc
}

// ---------------------------------------------------------------------------
// File discovery
// ---------------------------------------------------------------------------

// recordsLiveFileName maps (namespace, source) to the live recorder file name:
// localhost -> "<source>.rec", asicN -> "<source>.asicN.rec".
// It is the only place that knows the multi-ASIC naming convention.
func recordsLiveFileName(namespace, source string) (string, error) {
	switch source {
	case RecordsSourceSwss, RecordsSourceSairedis:
	default:
		return "", fmt.Errorf("records: unknown source %q (want %s or %s)",
			source, RecordsSourceSwss, RecordsSourceSairedis)
	}
	switch {
	case namespace == RecordsDefaultNamespace:
		return source + ".rec", nil
	case asicNamespaceRe.MatchString(namespace):
		return source + "." + namespace + ".rec", nil
	case namespace == "":
		return "", errors.New("records: namespace is required (localhost or asicN)")
	}
	return "", fmt.Errorf("records: unknown namespace %q (want localhost or asicN)", namespace)
}

// rotatedFile is one logrotate output for a live file: base.N or base.N.gz.
type rotatedFile struct {
	path  string
	index int
	gz    bool
	inode uint64
	mtime time.Time
}

// discoverRotated lists base.N and base.N.gz in dir, oldest first (highest N
// first), so a replay can walk them in chronological order. If both base.N
// and base.N.gz exist (logrotate mid-compress), the uncompressed one wins.
func discoverRotated(dir, base string) ([]rotatedFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	prefix := base + "."
	byIndex := map[int]rotatedFile{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		rest := strings.TrimPrefix(name, prefix)
		gz := strings.HasSuffix(rest, ".gz")
		if gz {
			rest = strings.TrimSuffix(rest, ".gz")
		}
		idx, err := strconv.Atoi(rest)
		if err != nil || idx < 1 {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		rf := rotatedFile{
			path:  filepath.Join(dir, name),
			index: idx,
			gz:    gz,
			inode: fileInode(info),
			mtime: info.ModTime(),
		}
		if prev, ok := byIndex[idx]; ok && !prev.gz {
			continue // keep the uncompressed copy
		}
		byIndex[idx] = rf
	}
	out := make([]rotatedFile, 0, len(byIndex))
	for _, rf := range byIndex {
		out = append(out, rf)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].index > out[j].index })
	return out, nil
}

// fileInode returns the inode of a stat result, or 0 if unavailable.
func fileInode(info os.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Ino)
	}
	return 0
}

// ---------------------------------------------------------------------------
// Timestamps
// ---------------------------------------------------------------------------

// parseRecordTS reads the leading "YYYY-MM-DD.HH:MM:SS.uuuuuu" stamp of a
// recorder line in the configured zone. false means the line has no stamp
// (rotation banner, comment, garbage).
func parseRecordTS(line string) (time.Time, bool) {
	if len(line) < len(recordsFileTSLayout) {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation(recordsFileTSLayout, line[:len(recordsFileTSLayout)], RecordsLocation())
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// parseRecordsFrom parses the [from=...] path parameter: RFC3339 (with or
// without zone), epoch seconds, or a relative offset (-30m, -2h, -1d).
// Empty returns the zero time, meaning live only.
func parseRecordsFrom(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	if strings.HasPrefix(s, "-") {
		d, err := parseRelativeDuration(s[1:])
		if err != nil {
			return time.Time{}, fmt.Errorf("records: bad relative from %q: %w", s, err)
		}
		return now.Add(-d), nil
	}
	if secs, err := strconv.ParseInt(s, 10, 64); err == nil {
		if secs < 0 {
			return time.Time{}, fmt.Errorf("records: bad epoch from %q", s)
		}
		return time.Unix(secs, 0), nil
	}
	// Zoned forms first; then zone-less forms in the records zone.
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	for _, layout := range []string{
		"2006-01-02T15:04:05.999999999",
		"2006-01-02T15:04:05",
		"2006-01-02T15:04",
		recordsFileTSLayout,
		"2006-01-02.15:04:05",
		"2006-01-02",
	} {
		if t, err := time.ParseInLocation(layout, s, RecordsLocation()); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("records: cannot parse from=%q (want RFC3339, epoch seconds, or -30m/-2h/-1d)", s)
}

// parseRelativeDuration accepts Go durations plus a trailing "d" for days.
func parseRelativeDuration(s string) (time.Duration, error) {
	if s == "" {
		return 0, errors.New("empty duration")
	}
	if strings.HasSuffix(s, "d") {
		n, err := strconv.ParseFloat(strings.TrimSuffix(s, "d"), 64)
		if err != nil || n < 0 || n > maxRelativeDays {
			return 0, fmt.Errorf("bad day count %q", s)
		}
		return time.Duration(n * float64(24*time.Hour)), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, err
	}
	if d < 0 {
		return 0, fmt.Errorf("negative duration %q", s)
	}
	return d, nil
}

// ---------------------------------------------------------------------------
// Line reader: newline-delimited, bounded, offset-tracking, tail-safe
// ---------------------------------------------------------------------------

// lineReader yields complete lines from r and never returns a line without
// its trailing newline; an incomplete tail is held until the next call.
// It tracks the byte offset of each line's start in the underlying stream
// (uncompressed offset for gzip), which becomes the seq token.
type lineReader struct {
	r            *bufio.Reader
	offset       int64  // next unread byte in the stream
	partial      []byte // bytes of the in-progress line
	partialStart int64  // stream offset where partial begins
	maxLine      int
	dropping     bool // current line exceeded maxLine; skip to newline
	dropped      uint64
}

func newLineReader(r io.Reader, startOffset int64, maxLine int) *lineReader {
	if maxLine <= 0 {
		maxLine = recordsDefaultMaxLine
	}
	return &lineReader{
		r:       bufio.NewReaderSize(r, recordsReaderBufSize),
		offset:  startOffset,
		maxLine: maxLine,
	}
}

// next returns the next complete line (without "\n"/"\r\n") and the stream
// offset it started at. It returns io.EOF when no complete line is available;
// any partial data is retained for the next call.
func (lr *lineReader) next() (string, int64, error) {
	for {
		chunk, err := lr.r.ReadSlice('\n')
		if len(chunk) > 0 {
			if len(lr.partial) == 0 && !lr.dropping {
				lr.partialStart = lr.offset
			}
			lr.offset += int64(len(chunk))
			if !lr.dropping {
				if len(lr.partial)+len(chunk) > lr.maxLine {
					lr.dropping = true
					lr.partial = lr.partial[:0]
				} else {
					lr.partial = append(lr.partial, chunk...)
				}
			}
		}
		switch {
		case err == nil:
			// Newline seen: a complete line is in partial (or being dropped).
			if lr.dropping {
				lr.dropping = false
				lr.dropped++
				continue
			}
			line := lr.partial[:len(lr.partial)-1]
			if n := len(line); n > 0 && line[n-1] == '\r' {
				line = line[:n-1]
			}
			s := string(line)
			lr.partial = lr.partial[:0]
			return s, lr.partialStart, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		default:
			return "", 0, err
		}
	}
}

// hasPartial reports whether an incomplete line is being held.
func (lr *lineReader) hasPartial() bool {
	return len(lr.partial) > 0 || lr.dropping
}

// ---------------------------------------------------------------------------
// FileTailer
// ---------------------------------------------------------------------------

// FileTailerStats are counters for V(2) logging by the RecordsClient.
type FileTailerStats struct {
	FilesOpened   uint64
	LinesEmitted  uint64
	LinesSkipped  uint64 // before `from` during replay
	LinesDropped  uint64 // longer than maxLine
	Rotations     uint64
	Gaps          uint64
	ReplayedFiles uint64
}

// FileTailer implements Tailer over one recorder file family
// (dir/<source>[.<asicN>].rec plus its logrotate outputs). It replays history
// bounded by `from`, then tails the live file until ctx is cancelled,
// surviving rename-based rotation and truncation. It buffers nothing beyond
// the current line: Run blocks on `out` when the consumer is slow.
type FileTailer struct {
	namespace string
	source    string
	dir       string
	liveName  string
	livePath  string

	pollInterval time.Duration
	retryMissing time.Duration
	maxLine      int

	stats struct {
		filesOpened   uint64
		linesEmitted  uint64
		linesSkipped  uint64
		linesDropped  uint64
		rotations     uint64
		gaps          uint64
		replayedFiles uint64
	}
}

// NewFileTailer validates namespace and source and resolves the file names
// under dir. It opens nothing; a missing file is handled by Run.
func NewFileTailer(dir, namespace, source string) (*FileTailer, error) {
	name, err := recordsLiveFileName(namespace, source)
	if err != nil {
		return nil, err
	}
	if dir == "" {
		dir = RecordsDir()
	}
	return &FileTailer{
		namespace:    namespace,
		source:       source,
		dir:          dir,
		liveName:     name,
		livePath:     filepath.Join(dir, name),
		pollInterval: recordsDefaultPollInterval,
		retryMissing: recordsDefaultRetryMissing,
		maxLine:      recordsDefaultMaxLine,
	}, nil
}

// LivePath returns the path of the live file being tailed.
func (t *FileTailer) LivePath() string { return t.livePath }

// Source returns the recorder source ("swss" or "sairedis").
func (t *FileTailer) Source() string { return t.source }

// Stats returns a snapshot of the tailer counters.
func (t *FileTailer) Stats() FileTailerStats {
	return FileTailerStats{
		FilesOpened:   atomic.LoadUint64(&t.stats.filesOpened),
		LinesEmitted:  atomic.LoadUint64(&t.stats.linesEmitted),
		LinesSkipped:  atomic.LoadUint64(&t.stats.linesSkipped),
		LinesDropped:  atomic.LoadUint64(&t.stats.linesDropped),
		Rotations:     atomic.LoadUint64(&t.stats.rotations),
		Gaps:          atomic.LoadUint64(&t.stats.gaps),
		ReplayedFiles: atomic.LoadUint64(&t.stats.replayedFiles),
	}
}

type emitFunc func(RawLine) error

// tailState is the open live file and its reader.
type tailState struct {
	f             *os.File
	inode         uint64
	lr            *lineReader
	missingLogged bool
	lastStat      time.Time
}

func (st *tailState) close() {
	if st.f != nil {
		st.f.Close()
	}
	st.f = nil
	st.lr = nil
	st.inode = 0
}

// Run implements Tailer. It returns ctx.Err() once cancelled, or an error
// only for unrecoverable setup failures. A missing file or directory is not
// an error: Run waits for it.
func (t *FileTailer) Run(ctx context.Context, from time.Time, out chan<- RawLine) error {
	emit := func(l RawLine) error {
		select {
		case out <- l:
			atomic.AddUint64(&t.stats.linesEmitted, 1)
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	st := &tailState{}
	defer st.close()

	if !from.IsZero() {
		log.V(2).Infof("records[%s/%s]: replay from %s under %s", t.namespace, t.source, from.Format(time.RFC3339), t.dir)
		if err := t.replay(ctx, from, st, emit); err != nil {
			return err
		}
	} else {
		// Live only: open at EOF if the file exists; otherwise wait in tail.
		if err := t.openLive(st, true); err != nil && !os.IsNotExist(err) {
			log.V(2).Infof("records[%s/%s]: open %s: %v", t.namespace, t.source, t.livePath, err)
		}
	}

	if err := emit(t.controlLine("live", nil)); err != nil {
		return err
	}
	return t.tail(ctx, st, emit)
}

// controlLine builds a RawLine with Source "control" and a JSON body.
func (t *FileTailer) controlLine(event string, extra map[string]string) RawLine {
	m := map[string]string{
		"event":         event,
		"tailer_source": t.source,
		"namespace":     t.namespace,
	}
	for k, v := range extra {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	return RawLine{Source: RecordsSourceControl, Line: string(b)}
}

func (t *FileTailer) seq(inode uint64, offset int64) string {
	return t.source + ":" + strconv.FormatUint(inode, 10) + ":" + strconv.FormatInt(offset, 10)
}

// openLive opens the live file and installs it in st. If atEnd, the reader
// starts at the current end of file (live only, no replay).
func (t *FileTailer) openLive(st *tailState, atEnd bool) error {
	f, err := os.Open(t.livePath)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	var off int64
	if atEnd {
		off, err = f.Seek(0, io.SeekEnd)
		if err != nil {
			f.Close()
			return err
		}
	}
	st.close()
	st.f = f
	st.inode = fileInode(info)
	st.lr = newLineReader(f, off, t.maxLine)
	st.missingLogged = false
	atomic.AddUint64(&t.stats.filesOpened, 1)
	log.V(2).Infof("records[%s/%s]: opened %s inode=%d offset=%d", t.namespace, t.source, t.livePath, st.inode, off)
	return nil
}

// ---------------------------------------------------------------------------
// Replay
// ---------------------------------------------------------------------------

// replay streams rotated files (oldest first) whose mtime is at or after
// `from`, then the live file from offset 0, dropping lines stamped before
// `from`. It leaves the live file open in st for tailing. Inodes are tracked
// so a rotation that happens during replay neither duplicates nor loses a file.
func (t *FileTailer) replay(ctx context.Context, from time.Time, st *tailState, emit emitFunc) error {
	seen := map[uint64]bool{}

	replayRotated := func() error {
		rotated, err := discoverRotated(t.dir, t.liveName)
		if err != nil {
			if os.IsNotExist(err) {
				log.V(2).Infof("records[%s/%s]: directory %s does not exist yet", t.namespace, t.source, t.dir)
				return nil
			}
			return err
		}
		for _, rf := range rotated {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if rf.inode != 0 && seen[rf.inode] {
				continue
			}
			if rf.mtime.Before(from) {
				log.V(2).Infof("records[%s/%s]: skip %s (mtime %s < from)", t.namespace, t.source, rf.path, rf.mtime.Format(time.RFC3339))
				seen[rf.inode] = true
				continue
			}
			seen[rf.inode] = true
			if err := t.replayFile(ctx, rf, from, emit); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				log.V(2).Infof("records[%s/%s]: replay %s: %v", t.namespace, t.source, rf.path, err)
			}
		}
		return nil
	}

	if err := replayRotated(); err != nil {
		return err
	}

	// Open the live file now, before the second discovery pass, so nothing can
	// rotate between "what is .1" and "what is live" without us noticing.
	if err := t.openLive(st, false); err != nil {
		if os.IsNotExist(err) {
			log.V(2).Infof("records[%s/%s]: %s missing during replay; will wait for it", t.namespace, t.source, t.livePath)
			return nil
		}
		return err
	}

	// Second discovery pass: if the live file rotated while we were replaying,
	// the previous live file is now .1 with an inode we have not seen. Replay
	// it before the file we hold open, so order is preserved and nothing is
	// duplicated.
	if err := replayRotated(); err != nil {
		return err
	}
	if seen[st.inode] {
		// Rename-based rotation cannot produce this; guard anyway.
		log.Warningf("records[%s/%s]: live inode %d already replayed; tailing from end", t.namespace, t.source, st.inode)
		if err := t.openLive(st, true); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	seen[st.inode] = true

	// Replay the live file from 0 with the same from-filter.
	atomic.AddUint64(&t.stats.replayedFiles, 1)
	_, err := t.streamLines(ctx, st.lr, st.inode, from, emit)
	return err
}

// replayFile streams one rotated file, transparently gunzipping .gz.
func (t *FileTailer) replayFile(ctx context.Context, rf rotatedFile, from time.Time, emit emitFunc) error {
	f, err := os.Open(rf.path)
	if err != nil {
		return err
	}
	defer f.Close()
	var r io.Reader = f
	if rf.gz {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer gz.Close()
		r = gz
	}
	atomic.AddUint64(&t.stats.filesOpened, 1)
	atomic.AddUint64(&t.stats.replayedFiles, 1)
	log.V(2).Infof("records[%s/%s]: replaying %s inode=%d", t.namespace, t.source, rf.path, rf.inode)
	lr := newLineReader(r, 0, t.maxLine)
	partial, err := t.streamLines(ctx, lr, rf.inode, from, emit)
	if partial {
		log.V(2).Infof("records[%s/%s]: %s ends without newline; trailing fragment dropped", t.namespace, t.source, rf.path)
	}
	return err
}

// streamLines reads lr to EOF, emitting lines stamped at or after `from`.
// Once one line passes the filter every following line is emitted, including
// unstamped ones (banners), so ordering is preserved. It returns whether an
// incomplete trailing line is held in lr.
func (t *FileTailer) streamLines(ctx context.Context, lr *lineReader, inode uint64, from time.Time, emit emitFunc) (bool, error) {
	passed := from.IsZero()
	for {
		line, start, err := lr.next()
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
				return lr.hasPartial(), err
			}
			atomic.AddUint64(&t.stats.linesDropped, lr.dropped)
			lr.dropped = 0
			return lr.hasPartial(), nil
		}
		if !passed {
			ts, ok := parseRecordTS(line)
			if !ok || ts.Before(from) {
				atomic.AddUint64(&t.stats.linesSkipped, 1)
				continue
			}
			passed = true
		}
		if err := emit(RawLine{Source: t.source, Seq: t.seq(inode, start), Line: line}); err != nil {
			return lr.hasPartial(), err
		}
		if ctx.Err() != nil {
			return lr.hasPartial(), ctx.Err()
		}
	}
}

// ---------------------------------------------------------------------------
// Live tail
// ---------------------------------------------------------------------------

// tail drains the live file, then polls: on each tick it stats the path and
// handles a missing file, a rotation (inode change) or a truncation.
func (t *FileTailer) tail(ctx context.Context, st *tailState, emit emitFunc) error {
	ticker := time.NewTicker(t.pollInterval)
	defer ticker.Stop()
	for {
		if st.f != nil {
			if err := t.drain(ctx, st, emit); err != nil {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		if err := t.checkFile(ctx, st, emit); err != nil {
			return err
		}
	}
}

// drain emits every complete line currently available in the live file.
func (t *FileTailer) drain(ctx context.Context, st *tailState, emit emitFunc) error {
	for {
		line, start, err := st.lr.next()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				log.V(2).Infof("records[%s/%s]: read %s: %v", t.namespace, t.source, t.livePath, err)
			}
			if st.lr.dropped > 0 {
				atomic.AddUint64(&t.stats.linesDropped, st.lr.dropped)
				log.V(2).Infof("records[%s/%s]: dropped %d over-long line(s) in %s", t.namespace, t.source, st.lr.dropped, t.livePath)
				st.lr.dropped = 0
			}
			return nil
		}
		if err := emit(RawLine{Source: t.source, Seq: t.seq(st.inode, start), Line: line}); err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
}

// checkFile stats the live path and reconciles st with what is on disk.
func (t *FileTailer) checkFile(ctx context.Context, st *tailState, emit emitFunc) error {
	now := time.Now()
	if st.f == nil && !st.lastStat.IsZero() && now.Sub(st.lastStat) < t.retryMissing {
		return nil // throttle retries while the file is missing
	}
	st.lastStat = now

	info, err := os.Stat(t.livePath)
	if err != nil {
		if os.IsNotExist(err) {
			if !st.missingLogged {
				log.V(2).Infof("records[%s/%s]: %s not present; waiting", t.namespace, t.source, t.livePath)
				st.missingLogged = true
			}
			// Keep any old fd open: orchagent may still write to it until it
			// gets SIGHUP. Lines keep flowing from it via drain.
			return nil
		}
		log.V(2).Infof("records[%s/%s]: stat %s: %v", t.namespace, t.source, t.livePath, err)
		return nil
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	inode := fileInode(info)

	switch {
	case st.f == nil:
		// (Re)appeared. If we previously followed a file, this is a rotation
		// we observed as "missing" for a while; start the new one at 0.
		if err := t.openLive(st, false); err != nil {
			log.V(2).Infof("records[%s/%s]: open %s: %v", t.namespace, t.source, t.livePath, err)
		}
		return nil

	case inode != st.inode:
		// Rotated: drain whatever landed in the old file after our last read,
		// then switch to the new file at offset 0.
		if err := t.drain(ctx, st, emit); err != nil {
			return err
		}
		if st.lr.hasPartial() {
			log.V(2).Infof("records[%s/%s]: rotated file left a partial line; dropped", t.namespace, t.source)
		}
		atomic.AddUint64(&t.stats.rotations, 1)
		log.V(2).Infof("records[%s/%s]: rotation detected inode %d -> %d", t.namespace, t.source, st.inode, inode)
		if err := t.openLive(st, false); err != nil {
			log.V(2).Infof("records[%s/%s]: reopen after rotation: %v", t.namespace, t.source, err)
			st.close()
		}
		return nil

	case info.Size() < st.lr.offset:
		// Same inode, shorter file: truncated in place. We cannot know what
		// was lost, so say so and start over from 0.
		atomic.AddUint64(&t.stats.gaps, 1)
		log.V(2).Infof("records[%s/%s]: %s truncated (size %d < offset %d)", t.namespace, t.source, t.livePath, info.Size(), st.lr.offset)
		if err := t.openLive(st, false); err != nil {
			log.V(2).Infof("records[%s/%s]: reopen after truncate: %v", t.namespace, t.source, err)
			st.close()
		}
		return emit(t.controlLine("gap", map[string]string{
			"reason": "truncate",
			"file":   t.livePath,
		}))
	}
	return nil
}
