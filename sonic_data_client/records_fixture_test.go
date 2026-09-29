package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// These tests guard the fixture scenario and its goldens, which every other
// RECORDS test builds on. If gen_scenario.py drifts from the real recorder
// formats, or a golden points at a line that is not in the scenario, this is
// where it shows up.

const recGoldenDir = recordsScenarioDir + "/golden"

var recTSPrefix = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}\.\d{2}:\d{2}:\d{2}\.\d{6}\|`)

// recPathsOldestFirst lists one namespace/source's files under dir oldest
// first, live last, skipping any that do not exist.
func recPathsOldestFirst(t *testing.T, dir, ns, source string) []string {
	t.Helper()
	live, rotated := recFilesOldestFirst(t, dir, ns, source)
	var paths []string
	for _, rf := range rotated {
		paths = append(paths, rf.path)
	}
	if _, err := os.Stat(live); err == nil {
		paths = append(paths, live)
	}
	return paths
}

// scenarioFiles lists one source's scenario files oldest first, live last.
func scenarioFiles(t *testing.T, source string) []string {
	t.Helper()
	return recPathsOldestFirst(t, recordsScenarioDir, RecordsDefaultNamespace, source)
}

func lineTS(t *testing.T, line string) time.Time {
	t.Helper()
	ts, err := time.ParseInLocation(recordsFileTSLayout, strings.SplitN(line, "|", 2)[0], time.Local)
	if err != nil {
		t.Fatalf("bad timestamp in %q: %v", line, err)
	}
	return ts
}

func TestScenarioFilesAreWellFormedAndOrdered(t *testing.T) {
	for _, src := range []string{RecordsSourceSwss, RecordsSourceSairedis} {
		var prev time.Time
		for _, p := range scenarioFiles(t, src) {
			lines := readRecLines(t, p)
			if len(lines) == 0 {
				t.Errorf("%s is empty", p)
			}
			for _, l := range lines {
				if !recTSPrefix.MatchString(l) {
					t.Errorf("%s: line lacks ts| prefix: %.80q", filepath.Base(p), l)
					continue
				}
				ts := lineTS(t, l)
				if ts.Before(prev) {
					t.Errorf("%s: timestamps go backwards at %.60q (prev %v)", filepath.Base(p), l, prev)
				}
				prev = ts
			}
		}
	}
}

func TestScenarioCoversRealLineShapes(t *testing.T) {
	var sai, swss []string
	for _, p := range scenarioFiles(t, RecordsSourceSairedis) {
		sai = append(sai, readRecLines(t, p)...)
	}
	for _, p := range scenarioFiles(t, RecordsSourceSwss) {
		swss = append(swss, readRecLines(t, p)...)
	}
	has := func(lines []string, pat string) bool {
		re := regexp.MustCompile(pat)
		for _, l := range lines {
			if re.MatchString(l) {
				return true
			}
		}
		return false
	}
	saiShapes := map[string]string{
		"bulk create (C, type once, || groups)": `\|C\|SAI_OBJECT_TYPE_ROUTE_ENTRY\|\|\{`,
		"bulk remove (R)":                       `\|R\|SAI_OBJECT_TYPE_ROUTE_ENTRY\|\|\{`,
		"single set (s)":                        `\|s\|SAI_OBJECT_TYPE_ROUTE_ENTRY:\{`,
		"single create (c)":                     `\|c\|SAI_OBJECT_TYPE_`,
		"single remove (r)":                     `\|r\|SAI_OBJECT_TYPE_NEXT_HOP_GROUP:oid:`,
		"synthetic failure (E)":                 `\|E\|SAI_STATUS_[A-Z_]+$`,
		"real-shape failure (Q with status)":    `\|Q\|[a-z_]+\|SAI_STATUS_(?:NOT_SUPPORTED|[A-Z_]+)\|`,
		"get + response (g/G)":                  `\|G\|SAI_STATUS_SUCCESS\|`,
		"notification (n)":                      `\|n\|fdb_event\|\[`,
		"logrotate banner (#)":                  `\|#\|logrotate on: /var/log/swss/sairedis\.rec$`,
	}
	for name, pat := range saiShapes {
		if !has(sai, pat) {
			t.Errorf("sairedis scenario lacks %s (/%s/)", name, pat)
		}
	}
	swssShapes := map[string]string{
		"SET with fields":      `\|ROUTE_TABLE:10\.1\.0\.0/24\|SET\|nexthop:`,
		"DEL without fields":   `\|ROUTE_TABLE:10\.1\.0\.0/24\|DEL$`,
		"key containing ':'":   `\|NEIGH_TABLE:Ethernet0:10\.0\.0\.1\|`,
		"IPv6 key":             `\|NEIGH_TABLE:Ethernet0:fe80::1\|`,
		"FDB key (vlan + mac)": `\|FDB_TABLE:Vlan100:00:11:22:33:44:55\|`,
	}
	for name, pat := range swssShapes {
		if !has(swss, pat) {
			t.Errorf("swss scenario lacks %s (/%s/)", name, pat)
		}
	}
}

// bufio.Scanner's default limit is 64 KB; a real sairedis bulk line can be far
// longer. The scenario must contain one so the tailer's buffer cap is tested.
func TestScenarioHasBulkLineOver64K(t *testing.T) {
	longest := 0
	for _, p := range scenarioFiles(t, RecordsSourceSairedis) {
		for _, l := range readRecLines(t, p) {
			if len(l) > longest {
				longest = len(l)
			}
			if len(l) > 64*1024 {
				if !strings.Contains(l, "|C|SAI_OBJECT_TYPE_ROUTE_ENTRY||") {
					t.Errorf("long line is not a bulk create: %.100q", l)
				}
				return
			}
		}
	}
	t.Errorf("no sairedis line over 64 KB (longest %d)", longest)
}

// The E line must directly follow the operation it reports on, because that
// is the only way the parser can attach it (plan section 6).
func TestScenarioFailureLineFollowsItsOperation(t *testing.T) {
	for _, p := range scenarioFiles(t, RecordsSourceSairedis) {
		lines := readRecLines(t, p)
		for i, l := range lines {
			if !strings.Contains(l, "|E|") {
				continue
			}
			if i == 0 || strings.Contains(lines[i-1], "|E|") || strings.Contains(lines[i-1], "|#|") {
				t.Errorf("%s:%d E line has no operation before it", filepath.Base(p), i+1)
			}
		}
	}
}

// goldenRecord mirrors the JSON_IETF payload in the plan (section 3.2) minus
// seq, plus raw for cross-checking against the fixture.
type goldenRecord struct {
	TS        string            `json:"ts"`
	Source    string            `json:"source"`
	DB        string            `json:"db"`
	Table     string            `json:"table"`
	Key       string            `json:"key"`
	Op        string            `json:"op"`
	Fields    map[string]string `json:"fields"`
	Status    string            `json:"status"`
	MatchedBy string            `json:"matched_by"`
	Raw       string            `json:"raw"`
}

func loadGolden(t *testing.T, name string) []goldenRecord {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(recGoldenDir, name))
	if err != nil {
		t.Fatal(err)
	}
	var recs []goldenRecord
	if err := json.Unmarshal(data, &recs); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if len(recs) == 0 {
		t.Fatalf("%s: empty golden", name)
	}
	return recs
}

// Every golden record must come from a fixture line, in fixture order, with a
// ts that is the fixture ts re-spelled in the output layout.
func TestGoldensMatchScenarioLines(t *testing.T) {
	// golden -> which fixture directory it was drawn from
	dirs := map[string]string{
		"appl_route_key.json":      recordsScenarioDir,
		"appl_neigh_table.json":    recordsScenarioDir,
		"asic_nhg_failures.json":   recordsScenarioDir,
		"asic1_route_entries.json": recordsMultiAsicDir,
	}
	entries, err := os.ReadDir(recGoldenDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(dirs) {
		t.Errorf("golden dir has %d files, this test knows %d; update the map", len(entries), len(dirs))
	}

	// Build "line -> position" for all lines of a source under a dir.
	index := func(dir, ns, src string) map[string]int {
		pos := map[string]int{}
		n := 0
		for _, p := range recPathsOldestFirst(t, dir, ns, src) {
			for _, l := range readRecLines(t, p) {
				pos[l] = n
				n++
			}
		}
		return pos
	}

	for name, dir := range dirs {
		ns := RecordsDefaultNamespace
		if strings.HasPrefix(name, "asic1_") {
			ns = "asic1"
		}
		idx := map[string]map[string]int{
			RecordsSourceSwss:     index(dir, ns, RecordsSourceSwss),
			RecordsSourceSairedis: index(dir, ns, RecordsSourceSairedis),
		}
		lastPos := map[string]int{RecordsSourceSwss: -1, RecordsSourceSairedis: -1}
		for i, r := range loadGolden(t, name) {
			for field, v := range map[string]string{"ts": r.TS, "source": r.Source, "db": r.DB,
				"table": r.Table, "key": r.Key, "op": r.Op, "matched_by": r.MatchedBy, "raw": r.Raw} {
				if v == "" {
					t.Errorf("%s[%d]: empty %s", name, i, field)
				}
			}
			if r.Fields == nil {
				t.Errorf("%s[%d]: fields must be an object, not null", name, i)
			}
			if (r.Source == RecordsSourceSwss) != (r.DB == "APPL_DB") {
				t.Errorf("%s[%d]: source %s with db %s", name, i, r.Source, r.DB)
			}
			pos, ok := idx[r.Source][r.Raw]
			if !ok {
				t.Errorf("%s[%d]: raw line not found in %s %s files: %.100q", name, i, dir, r.Source, r.Raw)
				continue
			}
			if pos <= lastPos[r.Source] {
				t.Errorf("%s[%d]: out of file order (pos %d after %d)", name, i, pos, lastPos[r.Source])
			}
			lastPos[r.Source] = pos
			wantTS := lineTS(t, r.Raw).Format(recordTSLayout)
			if r.TS != wantTS {
				t.Errorf("%s[%d]: ts %s, want %s (from raw)", name, i, r.TS, wantTS)
			}
			if r.Status != "" && r.Fields["_response"] != "E" {
				t.Errorf("%s[%d]: status set without _response marker", name, i)
			}
		}
	}
}

// The ops=E golden is the whole point of the project: the failed remove with
// its status attached.
func TestGoldenFailureRecord(t *testing.T) {
	recs := loadGolden(t, "asic_nhg_failures.json")
	if len(recs) != 1 {
		t.Fatalf("want exactly one failure record, got %d", len(recs))
	}
	r := recs[0]
	if r.Op != "r" || r.Status != "SAI_STATUS_OBJECT_IN_USE" || r.Key != "oid:0x5000000000a3c" {
		t.Errorf("unexpected failure record: %+v", r)
	}
}

// Following one route must yield APPL_DB and correlated ASIC_DB records
// interleaved in time order, with matched_by explaining each.
func TestGoldenRouteCorrelationInterleaves(t *testing.T) {
	recs := loadGolden(t, "appl_route_key.json")
	var ops, how []string
	var prev time.Time
	for _, r := range recs {
		ops = append(ops, r.Op)
		how = append(how, r.MatchedBy)
		ts, err := time.ParseInLocation(recordTSLayout, r.TS, time.Local)
		if err != nil {
			t.Fatal(err)
		}
		if ts.Before(prev) {
			t.Errorf("golden not in time order at %s", r.TS)
		}
		prev = ts
	}
	if got := strings.Join(ops, ","); got != "SET,C,SET,s,DEL,R" {
		t.Errorf("ops = %s", got)
	}
	for i, h := range how {
		if recs[i].Source == RecordsSourceSwss && h != "key" {
			t.Errorf("[%d] swss matched_by = %s, want key", i, h)
		}
		if recs[i].Source == RecordsSourceSairedis && h != "correlation:ROUTE_TABLE.dest" {
			t.Errorf("[%d] sairedis matched_by = %s", i, h)
		}
	}
	// Bulk records carry all their entry keys as a JSON array in _keys, and
	// key is the first of them.
	for _, r := range recs {
		if r.Op != "C" && r.Op != "R" {
			continue
		}
		var keys []string
		if err := json.Unmarshal([]byte(r.Fields["_keys"]), &keys); err != nil {
			t.Errorf("bulk %s record: _keys is not a JSON array: %v", r.Op, err)
			continue
		}
		if len(keys) == 0 || keys[0] != r.Key || !strings.Contains(keys[0], `"dest":"10.1.0.0/24"`) {
			t.Errorf("bulk %s record: key=%q _keys=%v", r.Op, r.Key, keys)
		}
	}
}
