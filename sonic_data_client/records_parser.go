package client

import (
	"encoding/json"
	"net"
	"sort"
	"strings"
	"time"

	log "github.com/golang/glog"
)

// recordTSLayout is the local fractional timestamp used in Record JSON (no zone).
const recordTSLayout = "2006-01-02T15:04:05.000000"

// PassThroughParser is a Day-1 stub Parser. If Line is JSON for a Record it
// decodes it; otherwise it returns a minimal Record with Seq/Source/Raw filled
// from the RawLine.
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

// ---------------------------------------------------------------------------
// RecordsParser — parser for swss.rec and sairedis.rec lines.
// ---------------------------------------------------------------------------

// RecordsParser implements the Parser interface for swss.rec and sairedis.rec lines.
type RecordsParser struct {
	// lastSairedis holds the most recently parsed sairedis record so that
	// a following E|<status> line can be attached to it.
	lastSairedis *Record

	// loc is the timezone used to parse the localtime timestamps in the rec files.
	// The gnmi container inherits the host timezone.
	loc *time.Location
}

// NewRecordsParser creates a parser using the given timezone for timestamp parsing.
// Pass time.Local if no override is configured.
func NewRecordsParser(loc *time.Location) *RecordsParser {
	return &RecordsParser{loc: loc}
}

// recTimestampLayout is the format orchagent uses for rec file timestamps.
// Example: 2026-09-26.10:15:32.101234
const recTimestampLayout = "2006-01-02.15:04:05.000000"

// Parse dispatches to the swss or sairedis parser based on RawLine.Source.
func (p *RecordsParser) Parse(l RawLine) (*Record, bool) {
	if len(l.Line) == 0 {
		return nil, false
	}
	switch l.Source {
	case "swss":
		return p.parseSwss(l)
	case "sairedis":
		return p.parseSairedis(l)
	default:
		log.V(4).Infof("records_parser: unknown source %q", l.Source)
		return nil, false
	}
}

// parseTimestamp parses the localtime timestamp from a rec file line.
func (p *RecordsParser) parseTimestamp(raw string) (time.Time, bool) {
	if len(raw) < 26 {
		return time.Time{}, false
	}
	ts, err := time.ParseInLocation(recTimestampLayout, raw[:26], p.loc)
	if err != nil {
		return time.Time{}, false
	}
	return ts, true
}

// parseSwss parses a swss.rec line.
// Format: timestamp|TABLE:key|op|f:v|f:v|...
func (p *RecordsParser) parseSwss(l RawLine) (*Record, bool) {
	line := l.Line

	if isSkippableLine(line) {
		return nil, false
	}

	parts := strings.Split(line, "|")
	if len(parts) < 3 {
		return nil, false
	}

	ts, ok := p.parseTimestamp(parts[0])
	if !ok {
		return nil, false
	}

	tableKey := parts[1]
	colonIdx := strings.IndexByte(tableKey, ':')
	if colonIdx < 0 {
		return nil, false
	}
	table := tableKey[:colonIdx]
	key := tableKey[colonIdx+1:]

	op := parts[2]
	if op != "SET" && op != "DEL" {
		log.V(6).Infof("records_parser: swss unknown op %q", op)
	}

	fields := make(map[string]string)
	for _, fv := range parts[3:] {
		if fv == "" {
			continue
		}
		idx := strings.IndexByte(fv, ':')
		if idx < 0 {
			fields[fv] = ""
		} else {
			fields[fv[:idx]] = fv[idx+1:]
		}
	}

	r := &Record{
		Seq:    l.Seq,
		TS:     ts,
		Source: "swss",
		DB:     "APPL_DB",
		Table:  table,
		Key:    key,
		Op:     op,
		Fields: fields,
		Status: "",
		Raw:    line,
	}
	return r, true
}

// parseSairedis parses a sairedis.rec line.
// Format: timestamp|opcode|key|attr=val|attr=val|...
func (p *RecordsParser) parseSairedis(l RawLine) (*Record, bool) {
	line := l.Line

	if isSkippableLine(line) {
		return nil, false
	}

	parts := strings.Split(line, "|")
	if len(parts) < 2 {
		return nil, false
	}

	ts, ok := p.parseTimestamp(parts[0])
	if !ok {
		return nil, false
	}

	opcode := parts[1]

	if opcode == "E" {
		return p.handleELine(l, ts, parts)
	}

	if len(parts) < 3 {
		return nil, false
	}

	rawKey := parts[2]

	saiType, objKey := splitSaiKey(rawKey)
	if saiType == "" {
		return nil, false
	}

	fields := make(map[string]string)

	isBulk := len(opcode) == 1 && opcode[0] >= 'A' && opcode[0] <= 'Z' && opcode[0] != 'E'

	if isBulk {
		bulkKeys := parseBulkKeys(line)
		if len(bulkKeys) > 1 {
			fields["_keys"] = strings.Join(bulkKeys, ",")
		}
		for _, av := range parts[3:] {
			if av == "" {
				continue
			}
			idx := strings.IndexByte(av, '=')
			if idx < 0 {
				continue
			}
			fields[av[:idx]] = av[idx+1:]
		}
	} else {
		for _, av := range parts[3:] {
			if av == "" {
				continue
			}
			idx := strings.IndexByte(av, '=')
			if idx < 0 {
				continue
			}
			fields[av[:idx]] = av[idx+1:]
		}
	}

	normKey := normaliseEntryKey(objKey)

	r := &Record{
		Seq:    l.Seq,
		TS:     ts,
		Source: "sairedis",
		DB:     "ASIC_DB",
		Table:  saiType,
		Key:    normKey,
		Op:     opcode,
		Fields: fields,
		Status: "",
		Raw:    line,
	}

	p.lastSairedis = r

	return r, true
}

// handleELine processes a sairedis E|<status> failure line.
func (p *RecordsParser) handleELine(l RawLine, ts time.Time, parts []string) (*Record, bool) {
	status := ""
	if len(parts) >= 3 {
		status = parts[2]
	}

	if p.lastSairedis == nil {
		r := &Record{
			Seq:    l.Seq,
			TS:     ts,
			Source: "sairedis",
			DB:     "ASIC_DB",
			Table:  "",
			Key:    "",
			Op:     "E",
			Fields: map[string]string{"_response": "E"},
			Status: status,
			Raw:    l.Line,
		}
		return r, true
	}

	attached := *p.lastSairedis
	attached.Status = status
	attached.Seq = l.Seq
	if attached.Fields == nil {
		attached.Fields = make(map[string]string)
	}
	newFields := make(map[string]string, len(attached.Fields)+1)
	for k, v := range attached.Fields {
		newFields[k] = v
	}
	newFields["_response"] = "E"
	attached.Fields = newFields

	p.lastSairedis = nil

	return &attached, true
}

// splitSaiKey splits a sairedis key like "SAI_OBJECT_TYPE_ROUTE_ENTRY:{...}" into
// the type and the object key.
func splitSaiKey(raw string) (saiType string, objKey string) {
	idx := strings.Index(raw, ":{")
	if idx >= 0 {
		return raw[:idx], raw[idx+1:]
	}

	idx = strings.Index(raw, ":oid:")
	if idx >= 0 {
		return raw[:idx], raw[idx+1:]
	}

	if strings.HasPrefix(raw, "SAI_") {
		return raw, ""
	}

	return "", ""
}

// normaliseEntryKey normalises a JSON entry key into a canonical form.
func normaliseEntryKey(key string) string {
	if len(key) == 0 || key[0] != '{' {
		return key
	}

	var m map[string]string
	if err := json.Unmarshal([]byte(key), &m); err != nil {
		return key
	}

	if dest, ok := m["dest"]; ok {
		m["dest"] = normaliseCIDR(dest)
	}
	if ip, ok := m["ip_address"]; ok {
		m["ip_address"] = normaliseIP(ip)
	}

	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		vb, _ := json.Marshal(m[k])
		b.Write(kb)
		b.WriteByte(':')
		b.Write(vb)
	}
	b.WriteByte('}')
	return b.String()
}

// normaliseCIDR canonicalises a CIDR prefix.
func normaliseCIDR(s string) string {
	_, ipnet, err := net.ParseCIDR(s)
	if err != nil {
		return s
	}
	return ipnet.String()
}

// normaliseIP canonicalises an IP address.
func normaliseIP(s string) string {
	ip := net.ParseIP(s)
	if ip == nil {
		return s
	}
	return ip.String()
}

// parseBulkKeys extracts all keys from a bulk sairedis line.
func parseBulkKeys(line string) []string {
	groups := strings.Split(line, "||")
	var keys []string
	for _, g := range groups {
		parts := strings.Split(g, "|")
		for _, part := range parts {
			if strings.HasPrefix(part, "SAI_") {
				keys = append(keys, part)
				break
			}
		}
	}
	return keys
}

// isSkippableLine returns true for lines that should not be parsed.
func isSkippableLine(line string) bool {
	if len(line) == 0 {
		return true
	}
	if line[0] == '#' {
		return true
	}
	if strings.HasPrefix(line, "SIGHUP") {
		return true
	}
	if strings.Contains(line, "Reopening log file") {
		return true
	}
	return false
}
