package client

import (
	"encoding/json"
	"strings"
)

// RecordsMatcher implements the Matcher interface.
// It evaluates a Record against a list of Subscriptions and returns
// whether any subscription matched and how.
type RecordsMatcher struct {
	subs []Subscription
}

// NewRecordsMatcher creates a matcher for the given set of subscriptions.
func NewRecordsMatcher(subs []Subscription) *RecordsMatcher {
	return &RecordsMatcher{subs: subs}
}

// Match checks a Record against all subscriptions.
// Returns (true, how) on the first hit, or (false, "") on miss.
func (m *RecordsMatcher) Match(r *Record) (ok bool, how string) {
	ok, how, _ = m.matchWithIndex(r)
	return
}

// matchWithIndex is Match plus the index of the subscription that hit (-1 on miss).
func (m *RecordsMatcher) matchWithIndex(r *Record) (ok bool, how string, idx int) {
	for i := range m.subs {
		if hit, reason := m.matchOne(r, &m.subs[i]); hit {
			if !opsFilterPass(r, &m.subs[i]) {
				continue
			}
			if !filterPass(r, &m.subs[i]) {
				continue
			}
			return true, reason, i
		}
	}
	return false, "", -1
}

// matchOne evaluates a single Record against a single Subscription.
func (m *RecordsMatcher) matchOne(r *Record, sub *Subscription) (bool, string) {
	if r.DB == sub.DB {
		return m.directMatch(r, sub)
	}

	if sub.DB == "APPL_DB" && r.DB == "ASIC_DB" {
		return m.correlateApplToSai(r, sub)
	}
	if sub.DB == "ASIC_DB" && r.DB == "APPL_DB" {
		return m.correlateSaiToAppl(r, sub)
	}

	return false, ""
}

// directMatch handles the case where the record's DB matches the subscription's DB.
func (m *RecordsMatcher) directMatch(r *Record, sub *Subscription) (bool, string) {
	// ASIC_DB records carry the concrete SAI object type in r.Table
	// (e.g. SAI_OBJECT_TYPE_ROUTE_ENTRY), while the RECORDS path grammar uses
	// the umbrella table name "ASIC_STATE". Treat "ASIC_STATE" as "any SAI
	// type", with the optional key acting as a SAI-type or entry-key prefix.
	if sub.DB == "ASIC_DB" && sub.Table == "ASIC_STATE" {
		if sub.Key == "" {
			return true, "prefix"
		}
		if strings.HasPrefix(r.Table, sub.Key) || keysEqual(r, sub) {
			return true, "exact"
		}
		return false, ""
	}

	if r.Table != sub.Table {
		return false, ""
	}

	if sub.Key == "" {
		return true, "prefix"
	}

	if keysEqual(r, sub) {
		return true, "exact"
	}

	return false, ""
}

// keysEqual compares a Record's key against a Subscription's key,
// handling CIDR normalisation and JSON canonical form.
func keysEqual(r *Record, sub *Subscription) bool {
	rKey := r.Key
	sKey := sub.Key

	if rKey == sKey {
		return true
	}

	rNorm := normaliseCIDR(rKey)
	sNorm := normaliseCIDR(sKey)
	if rNorm == sNorm {
		return true
	}

	rNorm = normaliseIP(rKey)
	sNorm = normaliseIP(sKey)
	if rNorm == sNorm {
		return true
	}

	return false
}

// CorrelationRule defines how an APPL_DB table maps to a SAI object type.
// Only entry-type SAI objects (those with JSON keys) can be correlated.
// OID-based objects (PORT, ACL, BUFFER, etc.) cannot be correlated from
// the rec files alone.
type CorrelationRule struct {
	ApplTable  string // e.g. "ROUTE_TABLE"
	SaiType    string // e.g. "SAI_OBJECT_TYPE_ROUTE_ENTRY"
	MatchField string // JSON field in the SAI entry key to compare

	// ApplKeyPos selects which colon-separated segment of the APPL_DB key
	// to use for comparison. -1 means use the entire key.
	// For NEIGH_TABLE keys like "Ethernet0:10.0.0.1", ApplKeyPos=1 extracts "10.0.0.1".
	// For FDB_TABLE keys like "Vlan100:AA:BB:CC:DD:EE:FF", ApplKeyPos=1 extracts the MAC.
	ApplKeyPos int
}

// correlationRules is the static lookup table for APPL_DB ↔ ASIC_DB correlation.
var correlationRules = []CorrelationRule{
	{
		ApplTable:  "ROUTE_TABLE",
		SaiType:    "SAI_OBJECT_TYPE_ROUTE_ENTRY",
		MatchField: "dest",
		ApplKeyPos: -1,
	},
	{
		ApplTable:  "NEIGH_TABLE",
		SaiType:    "SAI_OBJECT_TYPE_NEIGHBOR_ENTRY",
		MatchField: "ip_address",
		ApplKeyPos: 1,
	},
	{
		ApplTable:  "FDB_TABLE",
		SaiType:    "SAI_OBJECT_TYPE_FDB_ENTRY",
		MatchField: "mac_address",
		ApplKeyPos: 1,
	},
	{
		ApplTable:  "INSEG_TABLE",
		SaiType:    "SAI_OBJECT_TYPE_INSEG_ENTRY",
		MatchField: "label",
		ApplKeyPos: -1,
	},
	{
		ApplTable:  "NAT_TABLE",
		SaiType:    "SAI_OBJECT_TYPE_NAT_ENTRY",
		MatchField: "nat_data",
		ApplKeyPos: -1,
	},
	{
		ApplTable:  "MCAST_FDB_TABLE",
		SaiType:    "SAI_OBJECT_TYPE_MCAST_FDB_ENTRY",
		MatchField: "mac_address",
		ApplKeyPos: 1,
	},
}

// extractApplKeyPart extracts the comparable portion from an APPL_DB key
// based on the rule's ApplKeyPos.
func extractApplKeyPart(key string, rule *CorrelationRule) string {
	if rule.ApplKeyPos < 0 {
		return key
	}
	idx := -1
	for i := 0; i < rule.ApplKeyPos; i++ {
		next := strings.IndexByte(key[idx+1:], ':')
		if next < 0 {
			return key
		}
		idx += next + 1
	}
	return key[idx+1:]
}

// correlateApplToSai checks if a sairedis record matches an APPL_DB subscription.
func (m *RecordsMatcher) correlateApplToSai(r *Record, sub *Subscription) (bool, string) {
	for _, rule := range correlationRules {
		if sub.Table != rule.ApplTable {
			continue
		}
		if r.Table != rule.SaiType {
			continue
		}

		if sub.Key == "" {
			return true, "correlation:" + rule.ApplTable
		}

		val := extractJsonField(r.Key, rule.MatchField)
		if val == "" {
			continue
		}

		applKey := extractApplKeyPart(sub.Key, &rule)

		if valuesEqual(val, applKey) {
			return true, "correlation:" + rule.ApplTable + "." + rule.MatchField
		}
	}
	return false, ""
}

// correlateSaiToAppl checks if a swss record matches an ASIC_DB subscription (reverse).
func (m *RecordsMatcher) correlateSaiToAppl(r *Record, sub *Subscription) (bool, string) {
	for _, rule := range correlationRules {
		if sub.Table != rule.SaiType {
			continue
		}
		if r.Table != rule.ApplTable {
			continue
		}

		if sub.Key == "" {
			return true, "reverse-correlation:" + rule.SaiType
		}

		subVal := extractJsonField(sub.Key, rule.MatchField)
		if subVal == "" {
			continue
		}

		applKey := extractApplKeyPart(r.Key, &rule)

		if valuesEqual(applKey, subVal) {
			return true, "reverse-correlation:" + rule.SaiType + "." + rule.MatchField
		}
	}
	return false, ""
}

// extractJsonField parses a JSON string and returns the value of a specific field.
func extractJsonField(jsonKey string, field string) string {
	if len(jsonKey) == 0 || jsonKey[0] != '{' {
		return ""
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(jsonKey), &m); err != nil {
		return ""
	}
	return m[field]
}

// valuesEqual compares two values with normalisation (CIDR, IP).
func valuesEqual(a, b string) bool {
	if a == b {
		return true
	}
	if normaliseCIDR(a) == normaliseCIDR(b) {
		return true
	}
	if normaliseIP(a) == normaliseIP(b) {
		return true
	}
	return false
}

// filterPass reports whether the record's raw text contains the subscription's
// filter substring. An empty filter matches everything. This is the ASIC_DB
// filter=<string> option: show only records whose recorder line contains the
// given text.
func filterPass(r *Record, sub *Subscription) bool {
	if sub.Filter == "" {
		return true
	}
	return strings.Contains(r.Raw, sub.Filter)
}

// opsFilterPass checks whether a record's operation passes the subscription's ops filter.
func opsFilterPass(r *Record, sub *Subscription) bool {
	if len(sub.Ops) == 0 {
		return true
	}

	for _, op := range sub.Ops {
		if strings.EqualFold(op, "E") && r.Status != "" {
			return true
		}
		if strings.EqualFold(op, r.Op) {
			return true
		}
	}
	return false
}
