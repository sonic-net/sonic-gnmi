package client

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

const tailerTestTimeout = 5 * time.Second

// swssLine builds a swss.rec style line stamped at ts.
func swssLine(ts time.Time, key, op string) string {
	return fmt.Sprintf("%s|ROUTE_TABLE:%s|%s|nexthop:10.0.0.1|ifname:Ethernet0", ts.Format(recordsFileTSLayout), key, op)
}

func writeFile(t *testing.T, path string, lines ...string) {
	t.Helper()
	var sb strings.Builder
	for _, l := range lines {
		sb.WriteString(l)
		sb.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func writeGzFile(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	for _, l := range lines {
		if _, err := gz.Write([]byte(l + "\n")); err != nil {
			t.Fatalf("gzip write: %v", err)
		}
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
}

func appendFile(t *testing.T, path, data string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatalf("open append %s: %v", path, err)
	}
	defer f.Close()
	if _, err := f.WriteString(data); err != nil {
		t.Fatalf("append %s: %v", path, err)
	}
}

func newTestTailer(t *testing.T, dir, namespace, source string) *FileTailer {
	t.Helper()
	ft, err := NewFileTailer(dir, namespace, source)
	if err != nil {
		t.Fatalf("NewFileTailer: %v", err)
	}
	ft.pollInterval = 5 * time.Millisecond
	ft.retryMissing = 5 * time.Millisecond
	return ft
}

// runTailer starts ft.Run in the background and returns the output channel,
// a cancel function that also waits for Run to return, and the error result.
type tailerRun struct {
	out    chan RawLine
	cancel func()
	done   chan struct{}
	err    error
}

func startTailer(t *testing.T, ft *FileTailer, from time.Time) *tailerRun {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	tr := &tailerRun{out: make(chan RawLine), done: make(chan struct{})}
	go func() {
		tr.err = ft.Run(ctx, from, tr.out)
		close(tr.done)
	}()
	tr.cancel = func() {
		cancel()
		select {
		case <-tr.done:
		case <-time.After(tailerTestTimeout):
			t.Fatal("Run did not return after cancel")
		}
	}
	t.Cleanup(tr.cancel)
	return tr
}

// recv returns the next RawLine or fails the test.
func (tr *tailerRun) recv(t *testing.T) RawLine {
	t.Helper()
	select {
	case l := <-tr.out:
		return l
	case <-time.After(tailerTestTimeout):
		t.Fatal("timeout waiting for RawLine")
		return RawLine{}
	}
}

// recvN collects n non-control lines, failing on timeout. Control lines are
// returned separately.
func (tr *tailerRun) recvN(t *testing.T, n int) (lines []string, controls []string) {
	t.Helper()
	deadline := time.After(tailerTestTimeout)
	for len(lines) < n {
		select {
		case l := <-tr.out:
			if l.Source == RecordsSourceControl {
				controls = append(controls, l.Line)
				continue
			}
			lines = append(lines, l.Line)
		case <-deadline:
			t.Fatalf("timeout: got %d/%d lines: %q", len(lines), n, lines)
		}
	}
	return lines, controls
}

// expectNoLine asserts nothing arrives within d.
func (tr *tailerRun) expectNoLine(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case l := <-tr.out:
		t.Fatalf("unexpected line: %+v", l)
	case <-time.After(d):
	}
}

func controlEvent(t *testing.T, line string) map[string]string {
	t.Helper()
	var m map[string]string
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("control line is not JSON: %q: %v", line, err)
	}
	return m
}

func expectControl(t *testing.T, l RawLine, event string) {
	t.Helper()
	if l.Source != RecordsSourceControl {
		t.Fatalf("expected control line, got %+v", l)
	}
	if m := controlEvent(t, l.Line); m["event"] != event {
		t.Fatalf("control event = %q, want %q (%s)", m["event"], event, l.Line)
	}
}

// ---------------------------------------------------------------------------
// file mapping and discovery
// ---------------------------------------------------------------------------

func TestRecordsLiveFileName(t *testing.T) {
	cases := []struct {
		ns, src, want string
		wantErr       bool
	}{
		{"localhost", "swss", "swss.rec", false},
		{"localhost", "sairedis", "sairedis.rec", false},
		{"asic0", "swss", "swss.asic0.rec", false},
		{"asic7", "sairedis", "sairedis.asic7.rec", false},
		{"asic12", "swss", "swss.asic12.rec", false},
		{"", "swss", "", true},
		{"asic", "swss", "", true},
		{"asicX", "swss", "", true},
		{"ASIC0", "swss", "", true},
		{"foo", "swss", "", true},
		{"localhost", "retry", "", true},
		{"localhost", "", "", true},
	}
	for _, c := range cases {
		got, err := recordsLiveFileName(c.ns, c.src)
		if c.wantErr {
			if err == nil {
				t.Errorf("(%q,%q): want error, got %q", c.ns, c.src, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("(%q,%q): %v", c.ns, c.src, err)
			continue
		}
		if got != c.want {
			t.Errorf("(%q,%q) = %q, want %q", c.ns, c.src, got, c.want)
		}
	}
}

func TestNewFileTailerRejectsBeforeOpen(t *testing.T) {
	// Directory does not exist; a bad namespace must fail without touching it.
	if _, err := NewFileTailer("/nonexistent/records/dir", "asicX", "swss"); err == nil {
		t.Fatal("expected error for unknown namespace")
	}
	if _, err := NewFileTailer("/nonexistent/records/dir", "localhost", "nope"); err == nil {
		t.Fatal("expected error for unknown source")
	}
	ft, err := NewFileTailer("/nonexistent/records/dir", "asic1", "sairedis")
	if err != nil {
		t.Fatalf("valid namespace rejected: %v", err)
	}
	if ft.LivePath() != "/nonexistent/records/dir/sairedis.asic1.rec" {
		t.Errorf("LivePath = %q", ft.LivePath())
	}
	if ft.Source() != "sairedis" {
		t.Errorf("Source = %q", ft.Source())
	}
}

func TestNewFileTailerDefaultDir(t *testing.T) {
	old := RecordsDir()
	defer SetRecordsDir(old)
	SetRecordsDir("/some/where")
	ft, err := NewFileTailer("", "localhost", "swss")
	if err != nil {
		t.Fatal(err)
	}
	if ft.LivePath() != "/some/where/swss.rec" {
		t.Errorf("LivePath = %q", ft.LivePath())
	}
	SetRecordsDir("") // no-op
	if RecordsDir() != "/some/where" {
		t.Errorf("SetRecordsDir(\"\") changed dir to %q", RecordsDir())
	}
}

func TestDiscoverRotated(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{
		"swss.rec", "swss.rec.1", "swss.rec.2.gz", "swss.rec.3.gz", "swss.rec.10.gz",
		"swss.asic0.rec", "swss.asic0.rec.1", // different family, must be ignored
		"sairedis.rec.1",    // different family
		"swss.rec.bak",      // not numeric
		"swss.rec.0",        // index 0 is not a logrotate output
		"swss.rec.2",        // both .2 and .2.gz: uncompressed wins
		"swss.rec.4.gz.tmp", // garbage
	} {
		writeFile(t, filepath.Join(dir, n), "x")
	}
	if err := os.Mkdir(filepath.Join(dir, "swss.rec.5"), 0o755); err != nil { // a directory, ignored
		t.Fatal(err)
	}
	got, err := discoverRotated(dir, "swss.rec")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, rf := range got {
		names = append(names, filepath.Base(rf.path))
		if rf.inode == 0 {
			t.Errorf("%s: inode not captured", rf.path)
		}
		if rf.mtime.IsZero() {
			t.Errorf("%s: mtime not captured", rf.path)
		}
	}
	want := []string{"swss.rec.10.gz", "swss.rec.3.gz", "swss.rec.2", "swss.rec.1"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("discoverRotated = %v, want %v", names, want)
	}
	for _, rf := range got {
		if rf.index == 2 && rf.gz {
			t.Error("index 2: expected the uncompressed copy to win")
		}
		if rf.index == 3 && !rf.gz {
			t.Error("index 3: expected gz")
		}
	}

	if _, err := discoverRotated(filepath.Join(dir, "missing"), "swss.rec"); !os.IsNotExist(err) {
		t.Errorf("missing dir: err = %v, want IsNotExist", err)
	}
}

// ---------------------------------------------------------------------------
// timestamps
// ---------------------------------------------------------------------------

func TestParseRecordTS(t *testing.T) {
	loc := RecordsLocation()
	ts, ok := parseRecordTS("2026-09-26.10:15:32.101234|ROUTE_TABLE:10.1.0.0/24|SET|nexthop:10.0.0.1")
	if !ok {
		t.Fatal("expected ok")
	}
	want := time.Date(2026, 9, 26, 10, 15, 32, 101234000, loc)
	if !ts.Equal(want) {
		t.Errorf("ts = %v, want %v", ts, want)
	}
	for _, bad := range []string{"", "short", "# comment line", "2026-09-26 10:15:32.101234|x", "not-a-date-at-all-12345678|x"} {
		if _, ok := parseRecordTS(bad); ok {
			t.Errorf("%q: expected !ok", bad)
		}
	}
}

func TestParseRecordsFrom(t *testing.T) {
	// from= parsing lives in records_path.go. This locks the forms the
	// tailer relies on when a subscription hands it a parsed time.
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		in      string
		want    time.Time
		wantErr bool
	}{
		{"", time.Time{}, false},
		{"  ", time.Time{}, false},
		{"-30m", now.Add(-30 * time.Minute), false},
		{"-2h", now.Add(-2 * time.Hour), false},
		{"-1d", now.Add(-24 * time.Hour), false},
		{"-1h30m", now.Add(-90 * time.Minute), false},
		{"1700000000", time.Unix(1700000000, 0), false},
		{"2026-09-26T10:00:00Z", time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC), false},
		{"2026-09-26T10:00:00+02:00", time.Date(2026, 9, 26, 8, 0, 0, 0, time.UTC), false},
		{"2026-09-26T10:00:00", time.Date(2026, 9, 26, 10, 0, 0, 0, time.Local), false},
		{"-1.5d", time.Time{}, true},
		// Zone-less local time; Go accepts fractional seconds here even though
		// the layout has none, so this parses rather than erroring.
		{"2026-09-26T10:00:00.5", time.Date(2026, 9, 26, 10, 0, 0, 500000000, time.Local), false},
		{"2026-09-26.10:00:00.000000", time.Time{}, true},
		{"2026-09-26", time.Time{}, true},
		{"-", time.Time{}, true},
		{"-xyz", time.Time{}, true},
		{"-5x", time.Time{}, true},
		{"--1h", time.Time{}, true},
		{"-9999999999999999999999d", time.Time{}, true},
		{"yesterday", time.Time{}, true},
		{"-1", time.Time{}, true},
	}
	for _, c := range cases {
		got, err := parseRecordsFrom(c.in, now)
		if c.wantErr {
			if err == nil {
				t.Errorf("%q: want error, got %v", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if !got.Equal(c.want) {
			t.Errorf("%q = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestSetRecordsTZ(t *testing.T) {
	defer SetRecordsTZ("")
	if err := SetRecordsTZ("Not/AZone"); err == nil {
		t.Fatal("expected error for bad zone")
	}
	if err := SetRecordsTZ("UTC"); err != nil {
		t.Fatal(err)
	}
	if RecordsLocation() != time.UTC {
		t.Errorf("location = %v, want UTC", RecordsLocation())
	}
	ts, ok := parseRecordTS("2026-09-26.10:15:32.000000|x")
	if !ok || !ts.Equal(time.Date(2026, 9, 26, 10, 15, 32, 0, time.UTC)) {
		t.Errorf("ts = %v ok=%v", ts, ok)
	}
	if err := SetRecordsTZ(""); err != nil {
		t.Fatal(err)
	}
	if RecordsLocation() != time.Local {
		t.Errorf("location = %v, want Local", RecordsLocation())
	}
}

// ---------------------------------------------------------------------------
// line reader
// ---------------------------------------------------------------------------

func TestLineReaderPartialAndOffsets(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	appendFile(t, p, "aaa\nbb\ncccc") // no trailing newline
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	lr := newLineReader(f, 0, 0)

	l, off, err := lr.next()
	if err != nil || l != "aaa" || off != 0 {
		t.Fatalf("1: %q %d %v", l, off, err)
	}
	l, off, err = lr.next()
	if err != nil || l != "bb" || off != 4 {
		t.Fatalf("2: %q %d %v", l, off, err)
	}
	if _, _, err = lr.next(); err == nil {
		t.Fatal("3: expected EOF with partial held")
	}
	if !lr.hasPartial() {
		t.Fatal("expected partial to be held")
	}
	appendFile(t, p, "c\r\nlast\n")
	l, off, err = lr.next()
	if err != nil || l != "ccccc" || off != 7 {
		t.Fatalf("4: %q %d %v", l, off, err)
	}
	l, off, err = lr.next()
	if err != nil || l != "last" || off != 14 {
		t.Fatalf("5: %q %d %v", l, off, err)
	}
	if lr.hasPartial() {
		t.Fatal("no partial expected")
	}
}

func TestLineReaderLongLines(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	big := strings.Repeat("x", 1<<20) // 1 MB, spans many bufio chunks
	tooBig := strings.Repeat("y", 3000)
	appendFile(t, p, big+"\n"+tooBig+"\nafter\n")
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	lr := newLineReader(f, 0, 2048) // cap between the two sizes' behaviour: big is dropped too
	// With a 2 KB cap both long lines are dropped; "after" survives.
	l, off, err := lr.next()
	if err != nil || l != "after" {
		t.Fatalf("got %q %d %v", l, off, err)
	}
	if off != int64(len(big)+1+len(tooBig)+1) {
		t.Errorf("offset = %d", off)
	}
	if lr.dropped != 2 {
		t.Errorf("dropped = %d, want 2", lr.dropped)
	}

	// Default cap (4 MB) keeps the 1 MB line intact.
	f2, _ := os.Open(p)
	defer f2.Close()
	lr2 := newLineReader(f2, 0, 0)
	l, _, err = lr2.next()
	if err != nil || len(l) != len(big) {
		t.Fatalf("1MB line: len=%d err=%v", len(l), err)
	}
}

// ---------------------------------------------------------------------------
// live tail
// ---------------------------------------------------------------------------

func TestFileTailerLiveAppendAndPartialLine(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "swss.rec")
	now := time.Now()
	writeFile(t, p, swssLine(now, "1.1.1.0/24", "SET")) // pre-existing, must not be emitted live-only

	ft := newTestTailer(t, dir, "localhost", "swss")
	tr := startTailer(t, ft, time.Time{})
	expectControl(t, tr.recv(t), "live")
	tr.expectNoLine(t, 30*time.Millisecond)

	l1 := swssLine(now, "2.2.2.0/24", "SET")
	appendFile(t, p, l1+"\n")
	got := tr.recv(t)
	if got.Line != l1 || got.Source != "swss" {
		t.Fatalf("got %+v", got)
	}
	wantSeqPrefix := "swss:"
	if !strings.HasPrefix(got.Seq, wantSeqPrefix) || !strings.HasSuffix(got.Seq, fmt.Sprintf(":%d", len(swssLine(now, "1.1.1.0/24", "SET"))+1)) {
		t.Errorf("seq = %q", got.Seq)
	}

	// Partial line: nothing until the newline arrives.
	l2 := swssLine(now, "3.3.3.0/24", "DEL")
	appendFile(t, p, l2[:20])
	tr.expectNoLine(t, 30*time.Millisecond)
	appendFile(t, p, l2[20:]+"\n")
	if got := tr.recv(t); got.Line != l2 {
		t.Fatalf("partial join: got %q want %q", got.Line, l2)
	}
	st := ft.Stats()
	if st.LinesEmitted != 3 || st.FilesOpened != 1 {
		t.Errorf("stats = %+v", st)
	}
}

func TestFileTailerMissingFileThenCreated(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sairedis.asic1.rec")
	ft := newTestTailer(t, dir, "asic1", "sairedis")
	tr := startTailer(t, ft, time.Time{})
	expectControl(t, tr.recv(t), "live")
	tr.expectNoLine(t, 30*time.Millisecond)

	line := "2026-09-26.10:15:32.123456|c|SAI_OBJECT_TYPE_ROUTE_ENTRY:{}|SAI_ROUTE_ENTRY_ATTR_NEXT_HOP_ID=oid:0x1"
	writeFile(t, p, line)
	got := tr.recv(t)
	if got.Line != line || got.Source != "sairedis" || !strings.HasPrefix(got.Seq, "sairedis:") || !strings.HasSuffix(got.Seq, ":0") {
		t.Fatalf("got %+v", got)
	}
}

func TestFileTailerMissingDirThenCreated(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "swss")
	ft := newTestTailer(t, dir, "localhost", "swss")
	tr := startTailer(t, ft, time.Now().Add(-time.Hour)) // replay path with no dir
	expectControl(t, tr.recv(t), "live")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := swssLine(time.Now(), "9.9.9.0/24", "SET")
	writeFile(t, filepath.Join(dir, "swss.rec"), line)
	if got := tr.recv(t); got.Line != line {
		t.Fatalf("got %+v", got)
	}
}

func TestFileTailerRotationExactlyOnce(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "swss.rec")
	writeFile(t, p)
	ft := newTestTailer(t, dir, "localhost", "swss")
	tr := startTailer(t, ft, time.Time{})
	expectControl(t, tr.recv(t), "live")

	const total = 300
	now := time.Now()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < total; i++ {
			appendFile(t, p, swssLine(now, fmt.Sprintf("10.0.%d.0/24", i), "SET")+"\n")
			switch i {
			case 60, 140, 230:
				// logrotate: rename live to .1 (and gzip the previous .1), recreate live.
				if i > 60 {
					// simulate compressing the old .1 into .2.gz
					data, _ := os.ReadFile(p + ".1")
					writeGzFile(t, p+".2.gz", strings.Split(strings.TrimRight(string(data), "\n"), "\n")...)
				}
				if err := os.Rename(p, p+".1"); err != nil {
					t.Errorf("rename: %v", err)
				}
				// Writer keeps appending to the renamed file for a moment (pre-SIGHUP).
				appendFile(t, p+".1", swssLine(now, fmt.Sprintf("10.0.%d.0/24", i)+"-late", "SET")+"\n")
				time.Sleep(15 * time.Millisecond)
				writeFile(t, p)
			}
			if i%25 == 0 {
				time.Sleep(3 * time.Millisecond)
			}
		}
	}()

	lines, _ := tr.recvN(t, total+3)
	wg.Wait()

	seen := map[string]int{}
	for _, l := range lines {
		seen[l]++
	}
	for l, n := range seen {
		if n != 1 {
			t.Errorf("line emitted %d times: %s", n, l)
		}
	}
	for i := 0; i < total; i++ {
		if seen[swssLine(now, fmt.Sprintf("10.0.%d.0/24", i), "SET")] == 0 {
			t.Errorf("missing line %d", i)
		}
	}
	for _, i := range []int{60, 140, 230} {
		if seen[swssLine(now, fmt.Sprintf("10.0.%d.0/24", i)+"-late", "SET")] == 0 {
			t.Errorf("missing post-rename line %d", i)
		}
	}
	tr.expectNoLine(t, 30*time.Millisecond)
	if st := ft.Stats(); st.Rotations != 3 {
		t.Errorf("rotations = %d, want 3 (%+v)", st.Rotations, st)
	}
}

func TestFileTailerTruncateEmitsGap(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "swss.rec")
	now := time.Now()
	writeFile(t, p, swssLine(now, "1.0.0.0/8", "SET"), swssLine(now, "2.0.0.0/8", "SET"))
	ft := newTestTailer(t, dir, "localhost", "swss")
	tr := startTailer(t, ft, time.Time{})
	expectControl(t, tr.recv(t), "live")

	// Truncate in place (same inode), then write something shorter.
	if err := os.Truncate(p, 0); err != nil {
		t.Fatal(err)
	}
	short := swssLine(now, "3.0.0.0/8", "DEL")
	appendFile(t, p, short+"\n")

	var gotGap, gotLine bool
	for i := 0; i < 2; i++ {
		l := tr.recv(t)
		if l.Source == RecordsSourceControl {
			m := controlEvent(t, l.Line)
			if m["event"] != "gap" || m["reason"] != "truncate" {
				t.Fatalf("unexpected control %v", m)
			}
			gotGap = true
		} else if l.Line == short && strings.HasSuffix(l.Seq, ":0") {
			gotLine = true
		} else {
			t.Fatalf("unexpected %+v", l)
		}
	}
	if !gotGap || !gotLine {
		t.Fatalf("gap=%v line=%v", gotGap, gotLine)
	}
	if st := ft.Stats(); st.Gaps != 1 {
		t.Errorf("gaps = %d", st.Gaps)
	}
}

func TestFileTailerCancelIsPrompt(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "swss.rec"), swssLine(time.Now(), "1.0.0.0/8", "SET"))
	ft := newTestTailer(t, dir, "localhost", "swss")
	ft.pollInterval = time.Second // a long tick must not delay shutdown

	// Consumer never reads: Run must still return on cancel (blocked on send).
	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan RawLine)
	done := make(chan error, 1)
	go func() { done <- ft.Run(ctx, time.Now().Add(-time.Hour), out) }()
	time.Sleep(20 * time.Millisecond)
	start := time.Now()
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Errorf("err = %v, want context.Canceled", err)
		}
		if d := time.Since(start); d > 200*time.Millisecond {
			t.Errorf("Run took %v to return after cancel", d)
		}
	case <-time.After(tailerTestTimeout):
		t.Fatal("Run did not return")
	}
}

// ---------------------------------------------------------------------------
// replay
// ---------------------------------------------------------------------------

// buildReplayFixture lays out swss.rec.3.gz (old), .2.gz, .1, and live with
// 4 lines each, one minute apart, starting at base. It returns all 16 lines
// in chronological order.
func buildReplayFixture(t *testing.T, dir string, base time.Time) []string {
	t.Helper()
	var all []string
	mk := func(start int) []string {
		var ls []string
		for i := 0; i < 4; i++ {
			ls = append(ls, swssLine(base.Add(time.Duration(start+i)*time.Minute), fmt.Sprintf("10.%d.0.0/16", start+i), "SET"))
		}
		all = append(all, ls...)
		return ls
	}
	p := filepath.Join(dir, "swss.rec")
	writeGzFile(t, p+".3.gz", mk(0)...)
	writeGzFile(t, p+".2.gz", mk(4)...)
	writeFile(t, p+".1", mk(8)...)
	writeFile(t, p, mk(12)...)
	// mtimes: each file's mtime is the time of its last line (as logrotate would leave it).
	for i, name := range []string{p + ".3.gz", p + ".2.gz", p + ".1", p} {
		mt := base.Add(time.Duration(i*4+3) * time.Minute)
		if err := os.Chtimes(name, mt, mt); err != nil {
			t.Fatal(err)
		}
	}
	return all
}

func TestFileTailerReplayFromMiddleOfGz(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	all := buildReplayFixture(t, dir, base)

	// from = line index 5 (second line of .2.gz). Expect lines 5..15 then live.
	from := base.Add(5 * time.Minute)
	ft := newTestTailer(t, dir, "localhost", "swss")
	tr := startTailer(t, ft, from)

	lines, controls := tr.recvN(t, 11)
	if len(controls) != 0 {
		t.Fatalf("control before replay finished: %v", controls)
	}
	expectControl(t, tr.recv(t), "live")
	for i, l := range lines {
		if l != all[5+i] {
			t.Errorf("line %d = %q, want %q", i, l, all[5+i])
		}
	}
	// .3.gz must have been pruned by mtime, never opened.
	if st := ft.Stats(); st.ReplayedFiles != 3 || st.LinesSkipped != 1 {
		t.Errorf("stats = %+v (want 3 replayed files, 1 skipped line)", st)
	}

	// After replay we are live: appended lines still arrive.
	live := swssLine(time.Now(), "192.168.0.0/16", "SET")
	appendFile(t, filepath.Join(dir, "swss.rec"), live+"\n")
	if got := tr.recv(t); got.Line != live {
		t.Fatalf("live after replay: %+v", got)
	}
}

func TestFileTailerReplayAllAndSeqTokens(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	all := buildReplayFixture(t, dir, base)
	ft := newTestTailer(t, dir, "localhost", "swss")
	tr := startTailer(t, ft, base.Add(-time.Hour))

	deadline := time.After(tailerTestTimeout)
	var got []RawLine
	for len(got) < 16 {
		select {
		case l := <-tr.out:
			if l.Source == RecordsSourceControl {
				t.Fatalf("control before replay finished: %s", l.Line)
			}
			got = append(got, l)
		case <-deadline:
			t.Fatalf("got %d/16", len(got))
		}
	}
	expectControl(t, tr.recv(t), "live")

	// Order, content and seq shape: source:inode:offset with offsets
	// restarting at 0 per file and 4 distinct inodes.
	inodes := map[string]bool{}
	var lastInode string
	var expectOff int64
	for i, l := range got {
		if l.Line != all[i] {
			t.Errorf("line %d = %q want %q", i, l.Line, all[i])
		}
		parts := strings.Split(l.Seq, ":")
		if len(parts) != 3 || parts[0] != "swss" {
			t.Fatalf("seq %q malformed", l.Seq)
		}
		if parts[1] != lastInode {
			lastInode = parts[1]
			inodes[lastInode] = true
			expectOff = 0
		}
		if parts[2] != fmt.Sprint(expectOff) {
			t.Errorf("line %d seq offset = %s want %d", i, parts[2], expectOff)
		}
		expectOff += int64(len(l.Line)) + 1
	}
	if len(inodes) != 4 {
		t.Errorf("expected 4 distinct inodes, got %d", len(inodes))
	}
}

func TestFileTailerReplayLiveOnlyFileNoRotated(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sairedis.rec")
	now := time.Now()
	old := fmt.Sprintf("%s|c|SAI_OBJECT_TYPE_X:oid:0x1|A=1", now.Add(-time.Hour).Format(recordsFileTSLayout))
	banner := "# rotation banner without a stamp"
	recent := fmt.Sprintf("%s|s|SAI_OBJECT_TYPE_X:oid:0x1|A=2", now.Add(-time.Minute).Format(recordsFileTSLayout))
	trailing := "# banner after from must be emitted to keep order"
	writeFile(t, p, old, banner, recent, trailing)

	ft := newTestTailer(t, dir, "localhost", "sairedis")
	tr := startTailer(t, ft, now.Add(-10*time.Minute))
	lines, controls := tr.recvN(t, 2)
	if lines[0] != recent || lines[1] != trailing {
		t.Errorf("lines = %q", lines)
	}
	if len(controls) != 0 {
		t.Errorf("unexpected controls before replay end: %v", controls)
	}
	expectControl(t, tr.recv(t), "live")
}

func TestFileTailerReplayHandlesRotationDuringReplay(t *testing.T) {
	// A large .1 makes replay slow enough (consumer paced) that we can rotate
	// the live file mid-replay; the old live must be picked up as the new .1
	// exactly once, before the new live file.
	dir := t.TempDir()
	p := filepath.Join(dir, "swss.rec")
	base := time.Now().Add(-time.Hour)
	var dot1, live, newLive []string
	for i := 0; i < 50; i++ {
		dot1 = append(dot1, swssLine(base.Add(time.Duration(i)*time.Second), fmt.Sprintf("1.0.%d.0/24", i), "SET"))
	}
	for i := 0; i < 20; i++ {
		live = append(live, swssLine(base.Add(time.Duration(100+i)*time.Second), fmt.Sprintf("2.0.%d.0/24", i), "SET"))
	}
	for i := 0; i < 5; i++ {
		newLive = append(newLive, swssLine(base.Add(time.Duration(200+i)*time.Second), fmt.Sprintf("3.0.%d.0/24", i), "SET"))
	}
	writeFile(t, p+".1", dot1...)
	writeFile(t, p, live...)

	ft := newTestTailer(t, dir, "localhost", "swss")
	tr := startTailer(t, ft, base.Add(-time.Minute))

	var got []string
	// Read 10 lines of .1, then rotate under the tailer.
	for len(got) < 10 {
		l := tr.recv(t)
		got = append(got, l.Line)
	}
	if err := os.Rename(p+".1", p+".2"); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(p, p+".1"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, p, newLive...)

	want := append(append(append([]string{}, dot1...), live...), newLive...)
	deadline := time.After(tailerTestTimeout)
	for len(got) < len(want) {
		select {
		case l := <-tr.out:
			if l.Source == RecordsSourceControl {
				continue
			}
			got = append(got, l.Line)
		case <-deadline:
			t.Fatalf("got %d/%d lines", len(got), len(want))
		}
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line %d = %q want %q", i, got[i], want[i])
		}
	}
	expectControl(t, tr.recv(t), "live")
	tr.expectNoLine(t, 30*time.Millisecond)
}

func TestFileTailerReplayCorruptGzIsSkipped(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "swss.rec")
	now := time.Now()
	writeFile(t, p+".2.gz", "this is not gzip")
	good := swssLine(now.Add(-time.Minute), "5.5.5.0/24", "SET")
	writeFile(t, p+".1", good)
	writeFile(t, p)
	ft := newTestTailer(t, dir, "localhost", "swss")
	tr := startTailer(t, ft, now.Add(-time.Hour))
	lines, _ := tr.recvN(t, 1)
	if lines[0] != good {
		t.Fatalf("got %q", lines)
	}
	expectControl(t, tr.recv(t), "live")
}

func TestFileTailerMultiAsicIsolation(t *testing.T) {
	// asic0 and asic1 families live in one directory; each tailer must read
	// only its own pair and localhost must map to the plain names.
	dir := t.TempDir()
	now := time.Now()
	families := map[string]string{
		"swss.rec":           swssLine(now, "0.0.0.0/0", "SET"),
		"swss.asic0.rec":     swssLine(now, "10.0.0.0/8", "SET"),
		"swss.asic1.rec":     swssLine(now, "10.1.0.0/16", "SET"),
		"sairedis.asic0.rec": now.Format(recordsFileTSLayout) + "|c|SAI_OBJECT_TYPE_ROUTE_ENTRY:{a}|X=0",
		"sairedis.asic1.rec": now.Format(recordsFileTSLayout) + "|c|SAI_OBJECT_TYPE_ROUTE_ENTRY:{b}|X=1",
		"swss.asic0.rec.1":   swssLine(now.Add(-time.Minute), "10.0.0.0/8", "DEL"),
		"swss.rec.1":         swssLine(now.Add(-time.Minute), "0.0.0.0/0", "DEL"),
	}
	for name, line := range families {
		writeFile(t, filepath.Join(dir, name), line)
	}
	check := func(ns, src string, want ...string) {
		t.Helper()
		ft := newTestTailer(t, dir, ns, src)
		tr := startTailer(t, ft, now.Add(-time.Hour))
		lines, _ := tr.recvN(t, len(want))
		expectControl(t, tr.recv(t), "live")
		if strings.Join(lines, "\n") != strings.Join(want, "\n") {
			t.Errorf("%s/%s: got %q want %q", ns, src, lines, want)
		}
		tr.expectNoLine(t, 20*time.Millisecond)
		tr.cancel()
	}
	check("asic0", "swss", families["swss.asic0.rec.1"], families["swss.asic0.rec"])
	check("asic1", "swss", families["swss.asic1.rec"])
	check("asic0", "sairedis", families["sairedis.asic0.rec"])
	check("asic1", "sairedis", families["sairedis.asic1.rec"])
	check("localhost", "swss", families["swss.rec.1"], families["swss.rec"])
}

func TestFileTailerControlLineShape(t *testing.T) {
	ft := newTestTailer(t, t.TempDir(), "asic3", "sairedis")
	l := ft.controlLine("gap", map[string]string{"reason": "truncate"})
	if l.Source != RecordsSourceControl || l.Seq != "" {
		t.Fatalf("%+v", l)
	}
	m := controlEvent(t, l.Line)
	if m["event"] != "gap" || m["reason"] != "truncate" || m["tailer_source"] != "sairedis" || m["namespace"] != "asic3" {
		t.Errorf("control body = %v", m)
	}
	// Must not confuse PassThroughParser into a real record.
	r, ok := PassThroughParser{}.Parse(l)
	if !ok || r.Source != RecordsSourceControl || r.Raw != l.Line {
		t.Errorf("PassThroughParser on control: ok=%v r=%+v", ok, r)
	}
}
