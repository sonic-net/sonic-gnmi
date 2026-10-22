package client

import (
	"bufio"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// RotationHarness drives a recorder file the way orchagent + logrotate do:
// append whole lines, rotate (rename to .1, gzip the previous .1 to .2.gz,
// recreate the live file with a new inode, SIGHUP-style), or truncate in
// place (copytruncate-style: same inode, size drops to zero).
//
// WS2's tailer tests use it to prove "every line exactly once" across
// rotation. Written keeps every line ever appended, in order, so a test can
// compare the tailer's output to it.
type RotationHarness struct {
	t    *testing.T
	Dir  string
	Base string // e.g. "swss.rec"

	mu      sync.Mutex
	Written []string
	rotates int
}

// NewRotationHarness creates the harness in a fresh temp dir. The live file
// exists (empty) on return, matching a switch where orchagent has started.
func NewRotationHarness(t *testing.T, base string) *RotationHarness {
	t.Helper()
	h := &RotationHarness{t: t, Dir: t.TempDir(), Base: base}
	if err := os.WriteFile(h.Live(), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return h
}

// Live is the path of the file orchagent is currently writing.
func (h *RotationHarness) Live() string { return filepath.Join(h.Dir, h.Base) }

// Rotated returns the path of rotation n (1 = uncompressed, >=2 = .gz).
func (h *RotationHarness) Rotated(n int) string {
	if n == 1 {
		return h.Live() + ".1"
	}
	return fmt.Sprintf("%s.%d.gz", h.Live(), n)
}

// Append writes complete lines (each gets a trailing newline).
func (h *RotationHarness) Append(lines ...string) {
	h.t.Helper()
	f, err := os.OpenFile(h.Live(), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		h.t.Fatal(err)
	}
	defer f.Close()
	for _, l := range lines {
		if _, err := f.WriteString(l + "\n"); err != nil {
			h.t.Fatal(err)
		}
	}
	h.mu.Lock()
	h.Written = append(h.Written, lines...)
	h.mu.Unlock()
}

// AppendPartial writes bytes with no trailing newline, to simulate a line the
// writer has not finished. Call Append("") (or Append with the rest of the
// line) to complete it. The partial text is not added to Written until then.
func (h *RotationHarness) AppendPartial(s string) {
	h.t.Helper()
	f, err := os.OpenFile(h.Live(), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		h.t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		h.t.Fatal(err)
	}
}

// CompletePartial finishes a line started with AppendPartial and records the
// whole line in Written.
func (h *RotationHarness) CompletePartial(prefix, rest string) {
	h.t.Helper()
	f, err := os.OpenFile(h.Live(), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		h.t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(rest + "\n"); err != nil {
		h.t.Fatal(err)
	}
	h.mu.Lock()
	h.Written = append(h.Written, prefix+rest)
	h.mu.Unlock()
}

// Rotate mimics logrotate with `compress` + `delaycompress`:
// .N.gz -> .N+1.gz, .1 -> .2.gz (gzipped now), live -> .1 (rename, same
// inode), then a brand new live file. The old inode keeps its content, so a
// tailer holding the old fd can still drain it.
func (h *RotationHarness) Rotate() {
	h.t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	// Shift compressed generations up, highest first.
	for n := h.rotates + 1; n >= 2; n-- {
		src := h.Rotated(n)
		if _, err := os.Stat(src); err == nil {
			if err := os.Rename(src, h.Rotated(n+1)); err != nil {
				h.t.Fatal(err)
			}
		}
	}
	// delaycompress: the previous .1 is compressed on this rotation.
	if _, err := os.Stat(h.Rotated(1)); err == nil {
		gzipFile(h.t, h.Rotated(1), h.Rotated(2))
		if err := os.Remove(h.Rotated(1)); err != nil {
			h.t.Fatal(err)
		}
	}
	if err := os.Rename(h.Live(), h.Rotated(1)); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(h.Live(), nil, 0o644); err != nil {
		h.t.Fatal(err)
	}
	h.rotates++
}

// Truncate empties the live file in place (copytruncate). Same inode, size 0.
// Lines already written stay in Written: a tailer that had read them must not
// emit them again, and one that had not is allowed to report a gap.
func (h *RotationHarness) Truncate() {
	h.t.Helper()
	if err := os.Truncate(h.Live(), 0); err != nil {
		h.t.Fatal(err)
	}
}

// Inode returns the live file's inode, for asserting rename-vs-truncate.
func (h *RotationHarness) Inode() uint64 {
	h.t.Helper()
	st, err := os.Stat(h.Live())
	if err != nil {
		h.t.Fatal(err)
	}
	return st.Sys().(*syscall.Stat_t).Ino
}

// Snapshot returns a copy of Written.
func (h *RotationHarness) Snapshot() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.Written...)
}

// RunWriter appends total lines named "<prefix>-<i>" every interval, rotating
// after every rotateEvery lines (0 = never). It returns when done or when ctx
// is cancelled. Run it in a goroutine next to the tailer under test.
func (h *RotationHarness) RunWriter(ctx context.Context, prefix string, total int, interval time.Duration, rotateEvery int) {
	for i := 0; i < total; i++ {
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
		h.Append(fmt.Sprintf("%s-%d", prefix, i))
		if rotateEvery > 0 && (i+1)%rotateEvery == 0 && i+1 < total {
			h.Rotate()
		}
	}
}

// AllLinesOnDisk reads every generation oldest-first plus the live file, so a
// test can prove the harness itself lost nothing.
func (h *RotationHarness) AllLinesOnDisk() []string {
	h.t.Helper()
	rotated, err := discoverRotated(h.Dir, h.Base)
	if err != nil {
		h.t.Fatal(err)
	}
	var out []string
	for _, rf := range rotated {
		out = append(out, readRecLines(h.t, rf.path)...)
	}
	return append(out, readRecLines(h.t, h.Live())...)
}

// readRecLines returns the lines of a plain or gzip recorder file.
func readRecLines(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(path, ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		defer gz.Close()
		r = gz
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 4*1024*1024) // sairedis bulk lines are long
	var lines []string
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return lines
}

func gzipFile(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	gz := gzip.NewWriter(out)
	if _, err := io.Copy(gz, in); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------------------
// Self-tests: the harness must model logrotate faithfully or WS2's tests
// prove nothing.
// ---------------------------------------------------------------------------

func TestRotationHarnessGenerations(t *testing.T) {
	h := NewRotationHarness(t, "swss.rec")
	h.Append("a1", "a2", "a3")
	ino0 := h.Inode()
	h.Rotate()
	if h.Inode() == ino0 {
		t.Error("Rotate must give the live file a new inode")
	}
	h.Append("b1", "b2")
	h.Rotate()
	h.Append("c1")

	if got := readRecLines(t, h.Live()); strings.Join(got, ",") != "c1" {
		t.Errorf("live = %v", got)
	}
	if got := readRecLines(t, h.Rotated(1)); strings.Join(got, ",") != "b1,b2" {
		t.Errorf(".1 = %v", got)
	}
	if !strings.HasSuffix(h.Rotated(2), ".gz") {
		t.Fatal(".2 must be gzip")
	}
	if got := readRecLines(t, h.Rotated(2)); strings.Join(got, ",") != "a1,a2,a3" {
		t.Errorf(".2.gz = %v", got)
	}
	if _, err := os.Stat(h.Rotated(3)); !os.IsNotExist(err) {
		t.Error("no .3.gz expected after two rotations")
	}
	if all := h.AllLinesOnDisk(); strings.Join(all, ",") != strings.Join(h.Snapshot(), ",") {
		t.Errorf("on disk %v != written %v", all, h.Snapshot())
	}
}

func TestRotationHarnessThirdRotationShiftsGz(t *testing.T) {
	h := NewRotationHarness(t, "sairedis.rec")
	h.Append("a")
	h.Rotate()
	h.Append("b")
	h.Rotate()
	h.Append("c")
	h.Rotate()
	h.Append("d")
	want := map[string]string{h.Rotated(3): "a", h.Rotated(2): "b", h.Rotated(1): "c", h.Live(): "d"}
	for p, l := range want {
		if got := readRecLines(t, p); strings.Join(got, ",") != l {
			t.Errorf("%s = %v, want %s", filepath.Base(p), got, l)
		}
	}
}

func TestRotationHarnessTruncateKeepsInode(t *testing.T) {
	h := NewRotationHarness(t, "swss.rec")
	h.Append("x", "y")
	ino := h.Inode()
	h.Truncate()
	if h.Inode() != ino {
		t.Error("Truncate must keep the inode")
	}
	st, _ := os.Stat(h.Live())
	if st.Size() != 0 {
		t.Errorf("size after truncate = %d", st.Size())
	}
	if len(h.Snapshot()) != 2 {
		t.Error("Written must keep the pre-truncate lines")
	}
}

func TestRotationHarnessPartialLine(t *testing.T) {
	h := NewRotationHarness(t, "swss.rec")
	h.AppendPartial("2026-09-27.10:00:00.000001|ROUTE_TABLE:10.1.0.0/24|SET|next")
	data, _ := os.ReadFile(h.Live())
	if strings.HasSuffix(string(data), "\n") {
		t.Fatal("partial line must not end in newline")
	}
	if len(h.Snapshot()) != 0 {
		t.Fatal("partial line must not count as written")
	}
	h.CompletePartial("2026-09-27.10:00:00.000001|ROUTE_TABLE:10.1.0.0/24|SET|next", "hop:10.0.0.1")
	got := readRecLines(t, h.Live())
	if len(got) != 1 || !strings.HasSuffix(got[0], "|SET|nexthop:10.0.0.1") {
		t.Errorf("completed line = %v", got)
	}
}

func TestRotationHarnessRunWriter(t *testing.T) {
	h := NewRotationHarness(t, "swss.rec")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h.RunWriter(ctx, "L", 10, time.Millisecond, 4)
	if len(h.Snapshot()) != 10 {
		t.Fatalf("written %d lines", len(h.Snapshot()))
	}
	// 10 lines, rotate after 4 and 8: .2.gz = L-0..3, .1 = L-4..7, live = L-8..9
	if got := readRecLines(t, h.Live()); strings.Join(got, ",") != "L-8,L-9" {
		t.Errorf("live = %v", got)
	}
	if all := h.AllLinesOnDisk(); strings.Join(all, ",") != strings.Join(h.Snapshot(), ",") {
		t.Errorf("on disk %v != written %v", all, h.Snapshot())
	}
}
