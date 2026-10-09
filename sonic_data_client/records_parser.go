package client

import (
	"encoding/json"
	"net"
	"sort"
	"strconv"
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
	// lastSairedis holds the Records of the most recently parsed sairedis
	// line (one per entry for bulk ops) so a following E|<status> line can be
	// attached to each of them.
	lastSairedis []*Record

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

// saiKeyFallbackLen bounds how much of an unrecognised entry key is copied
// into dest= so an unexpected recorder format still yields a readable key.
const saiKeyFallbackLen = 128

// saiEntry is one object within a sairedis line: a single-op line has exactly
// one, a bulk line (upper-case opcode) has one per "||" group.
type saiEntry struct {
	key   string            // raw entry key: JSON for entry types, oid:0x.. for objects
	attrs map[string]string // attr=val pairs belonging to this entry
}

// parseAttrs turns attr=val tokens into a map, skipping blanks and malformed tokens.
func parseAttrs(tokens []string, into map[string]string) {
	for _, av := range tokens {
		if av == "" {
			continue
		}
		idx := strings.IndexByte(av, '=')
		if idx < 0 {
			continue
		}
		into[av[:idx]] = av[idx+1:]
	}
}

// saiEntries splits a sairedis line into its entries.
//
// Single op:  ts|c|SAI_OBJECT_TYPE_X:<key>|attr=val|...
// Bulk op:    ts|C|SAI_OBJECT_TYPE_X||<key1>|attr=val|...||<key2>|attr=val|...
//
// In the bulk form the object type is stated once and each "||" group carries
// its own key and attributes, so attributes are per entry, never per line.
func saiEntries(line string, parts []string, isBulk bool, objKey string) []saiEntry {
	if !isBulk {
		e := saiEntry{key: objKey, attrs: make(map[string]string)}
		parseAttrs(parts[3:], e.attrs)
		return []saiEntry{e}
	}
	groups := strings.Split(line, "||")
	if len(groups) < 2 {
		// Upper-case opcode but no "||" groups: treat like a single op.
		e := saiEntry{key: objKey, attrs: make(map[string]string)}
		parseAttrs(parts[3:], e.attrs)
		return []saiEntry{e}
	}
	entries := make([]saiEntry, 0, len(groups)-1)
	for _, g := range groups[1:] {
		toks := strings.Split(g, "|")
		e := saiEntry{key: toks[0], attrs: make(map[string]string)}
		parseAttrs(toks[1:], e.attrs)
		entries = append(entries, e)
	}
	return entries
}

// formatSaiKey renders the operator-facing key for one SAI entry as key=value
// pairs.
//
// SAI_OBJECT_TYPE_ROUTE_ENTRY: "dest=<prefix>" with the prefix canonicalised
// (IPv4 or IPv6, host bits zeroed, IPv6 compressed), plus ",nh=<oid>" when
// SAI_ROUTE_ENTRY_ATTR_NEXT_HOP_ID is present (creates and sets; removes carry
// no attributes). If the entry key is not the JSON shape we expect, the first
// saiKeyFallbackLen characters of the raw key go into dest= unchanged, so the
// record is still identifiable and nothing is dropped.
//
// Other object types keep their canonical JSON (entry types) or oid (objects).
func formatSaiKey(saiType, rawKey string, attrs map[string]string) string {
	switch saiType {
	case "SAI_OBJECT_TYPE_ROUTE_ENTRY":
		dest := ""
		if len(rawKey) > 0 && rawKey[0] == '{' {
			var m map[string]string
			if err := json.Unmarshal([]byte(rawKey), &m); err == nil {
				dest = m["dest"]
			}
		}
		if dest == "" {
			fb := rawKey
			if len(fb) > saiKeyFallbackLen {
				fb = fb[:saiKeyFallbackLen]
			}
			dest = fb
		} else {
			dest = normaliseCIDR(dest)
		}
		key := "dest=" + dest
		if nh, ok := attrs["SAI_ROUTE_ENTRY_ATTR_NEXT_HOP_ID"]; ok && nh != "" {
			key += ",nh=" + nh
		}
		return key
	}
	return normaliseEntryKey(rawKey)
}

// ParseAll is Parse for lines that may yield several Records: a bulk sairedis
// line produces one Record per entry, and an E line re-emits every Record of
// the operation it reports on. RecordsClient prefers this over Parse.
func (p *RecordsParser) ParseAll(l RawLine) []*Record {
	if len(l.Line) == 0 {
		return nil
	}
	switch l.Source {
	case "swss":
		if r, ok := p.parseSwss(l); ok {
			return []*Record{r}
		}
		return nil
	case "sairedis":
		return p.parseSairedisAll(l)
	default:
		log.V(4).Infof("records_parser: unknown source %q", l.Source)
		return nil
	}
}

// parseSairedis returns the first Record of the line (Parser interface).
func (p *RecordsParser) parseSairedis(l RawLine) (*Record, bool) {
	recs := p.parseSairedisAll(l)
	if len(recs) == 0 {
		return nil, false
	}
	return recs[0], true
}

// parseSairedisAll parses a sairedis.rec line into one Record per entry.
// Format: timestamp|opcode|key|attr=val|attr=val|...   (see saiEntries for bulk)
func (p *RecordsParser) parseSairedisAll(l RawLine) []*Record {
	line := l.Line

	if isSkippableLine(line) {
		return nil
	}

	parts := strings.Split(line, "|")
	if len(parts) < 2 {
		return nil
	}

	ts, ok := p.parseTimestamp(parts[0])
	if !ok {
		return nil
	}

	opcode := parts[1]

	if opcode == "E" {
		return p.handleELine(l, ts, parts)
	}

	if len(parts) < 3 {
		return nil
	}

	saiType, objKey := splitSaiKey(parts[2])
	if saiType == "" {
		return nil
	}

	isBulk := len(opcode) == 1 && opcode[0] >= 'A' && opcode[0] <= 'Z'
	entries := saiEntries(line, parts, isBulk, objKey)

	recs := make([]*Record, 0, len(entries))
	for i, e := range entries {
		fields := e.attrs
		fields["_entry"] = e.key
		if isBulk {
			fields["_bulk_index"] = strconv.Itoa(i)
			fields["_bulk_count"] = strconv.Itoa(len(entries))
		}
		recs = append(recs, &Record{
			Seq:    l.Seq,
			TS:     ts,
			Source: "sairedis",
			DB:     "ASIC_DB",
			Table:  saiType,
			Key:    formatSaiKey(saiType, e.key, e.attrs),
			Op:     opcode,
			Fields: fields,
			Status: "",
			Raw:    line,
		})
	}

	p.lastSairedis = recs
	return recs
}

// handleELine processes a sairedis E failure line and re-emits every Record
// of the preceding operation with Status set and _response=E.
//
// Single op:  ts|E|<status>
// Bulk op:    ts|E|<overall status>||<status entry 0>||<status entry 1>...
//
// For a bulk failure each re-emitted Record gets its own entry status (a bulk
// remove can fail on one entry with ITEM_NOT_FOUND while the rest succeed);
// the overall status is kept in Fields["_bulk_status"]. If the per-entry list
// does not line up with the entries we hold, every Record gets the overall
// status.
func (p *RecordsParser) handleELine(l RawLine, ts time.Time, parts []string) []*Record {
	groups := strings.Split(l.Line, "||")
	head := strings.Split(groups[0], "|")
	overall := ""
	if len(head) >= 3 {
		overall = head[2]
	}
	perEntry := groups[1:]

	if len(p.lastSairedis) == 0 {
		return []*Record{{
			Seq:    l.Seq,
			TS:     ts,
			Source: "sairedis",
			DB:     "ASIC_DB",
			Table:  "",
			Key:    "",
			Op:     "E",
			Fields: map[string]string{"_response": "E"},
			Status: overall,
			Raw:    l.Line,
		}}
	}

	usePerEntry := len(perEntry) == len(p.lastSairedis)
	out := make([]*Record, 0, len(p.lastSairedis))
	for i, prev := range p.lastSairedis {
		attached := *prev
		attached.Status = overall
		if usePerEntry && perEntry[i] != "" {
			attached.Status = perEntry[i]
		}
		attached.Seq = l.Seq
		newFields := make(map[string]string, len(attached.Fields)+2)
		for k, v := range attached.Fields {
			newFields[k] = v
		}
		newFields["_response"] = "E"
		if len(perEntry) > 0 {
			newFields["_bulk_status"] = overall
		}
		attached.Fields = newFields
		out = append(out, &attached)
	}
	p.lastSairedis = nil
	return out
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
