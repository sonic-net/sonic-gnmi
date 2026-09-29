package client

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
)

const (
	recordsParamFrom = "from"
	recordsParamOps  = "ops"

	recordsDBAppl         = "APPL_DB"
	recordsDBAsic         = "ASIC_DB"
	recordsTableAsicState = "ASIC_STATE"

	// Operator spelling for the default SONiC namespace.
	recordsNamespaceLocalhost = "localhost"
)

// recordsSubscription is one parsed RECORDS subscribe path.
//
//	/RECORDS/<namespace>/<DB>/<TABLE>[/<key elements...>][from=<when>][ops=<list>]
type recordsSubscription struct {
	path      *gnmipb.Path
	namespace string
	db        string
	table     string
	key       string // empty => whole table / type prefix
	from      time.Time
	ops       []string // empty => all ops
}

// recordsGetNamespaces lists configured DB namespaces. Overridable in tests.
var recordsGetNamespaces = sdcfg.GetDbAllNamespaces

// recordsNow is wall clock for relative from= parsing. Overridable in tests.
var recordsNow = time.Now

func parseRecordsPath(path *gnmipb.Path) (*recordsSubscription, error) {
	if path == nil {
		return nil, fmt.Errorf("RECORDS: path is required")
	}

	var (
		names   []string
		fromStr string
		opsStr  string
	)
	for _, e := range path.GetElem() {
		if e == nil {
			continue
		}
		name := e.GetName()
		if name != "" {
			names = append(names, name)
		}
		for k, v := range e.GetKey() {
			switch k {
			case recordsParamFrom:
				fromStr = v
			case recordsParamOps:
				opsStr = v
			}
		}
	}

	// Target may already be RECORDS in the prefix; drop a leading target elem.
	if len(names) > 0 && strings.EqualFold(names[0], "RECORDS") {
		names = names[1:]
	}

	// <namespace>/<DB>/<TABLE> required.
	if len(names) < 3 {
		return nil, fmt.Errorf("RECORDS: path must be /RECORDS/<namespace>/<DB>/<TABLE>[/<key>...][from=][ops=], got %v", pathElemNames(path))
	}

	sub := &recordsSubscription{
		path:      path,
		namespace: names[0],
		db:        names[1],
		table:     names[2],
	}
	if len(names) > 3 {
		sub.key = strings.Join(names[3:], "/")
	}

	switch sub.db {
	case recordsDBAppl:
		// table is free-form (ROUTE_TABLE, NEIGH_TABLE, ...)
	case recordsDBAsic:
		if sub.table != recordsTableAsicState {
			return nil, fmt.Errorf("RECORDS: ASIC_DB table must be %s, got %q", recordsTableAsicState, sub.table)
		}
	default:
		return nil, fmt.Errorf("RECORDS: DB must be %s or %s, got %q", recordsDBAppl, recordsDBAsic, sub.db)
	}

	if sub.namespace == "" {
		return nil, fmt.Errorf("RECORDS: namespace is required")
	}

	from, err := parseRecordsFrom(fromStr, recordsNow())
	if err != nil {
		return nil, err
	}
	sub.from = from

	ops, err := parseRecordsOps(opsStr)
	if err != nil {
		return nil, err
	}
	sub.ops = ops

	return sub, nil
}

func parseRecordsFrom(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil // live only
	}

	// Relative: -30m, -2h, -1d (ParseDuration has no "d").
	if strings.HasPrefix(s, "-") {
		rel := s[1:]
		if strings.HasSuffix(rel, "d") {
			days, err := strconv.Atoi(strings.TrimSuffix(rel, "d"))
			if err != nil || days < 0 {
				return time.Time{}, fmt.Errorf("RECORDS: invalid from=%q", s)
			}
			return now.Add(-time.Duration(days) * 24 * time.Hour), nil
		}
		d, err := time.ParseDuration("-" + rel)
		if err != nil {
			return time.Time{}, fmt.Errorf("RECORDS: invalid from=%q: %v", s, err)
		}
		return now.Add(d), nil
	}

	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	// Plan examples sometimes omit zone; interpret in local TZ.
	if t, err := time.ParseInLocation("2006-01-02T15:04:05", s, time.Local); err == nil {
		return t, nil
	}
	if sec, err := strconv.ParseInt(s, 10, 64); err == nil {
		return time.Unix(sec, 0), nil
	}
	return time.Time{}, fmt.Errorf("RECORDS: invalid from=%q (want RFC3339, epoch seconds, or relative like -30m)", s)
}

func parseRecordsOps(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	parts := strings.Split(s, ",")
	ops := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			return nil, fmt.Errorf("RECORDS: invalid ops=%q (empty token)", s)
		}
		ops = append(ops, p)
	}
	return ops, nil
}

// validateRecordsNamespace checks the operator namespace against configured DB
// namespaces. "localhost" is the operator spelling for the default ("").
func validateRecordsNamespace(ns string) error {
	list, err := recordsGetNamespaces()
	if err != nil {
		return fmt.Errorf("RECORDS: list namespaces: %v", err)
	}
	if recordsNamespaceAllowed(ns, list) {
		return nil
	}
	return fmt.Errorf("RECORDS: unknown namespace %q (configured: %s)", ns, formatNamespaceList(list))
}

func recordsNamespaceAllowed(ns string, configured []string) bool {
	if ns == recordsNamespaceLocalhost {
		for _, c := range configured {
			if c == "" || c == recordsNamespaceLocalhost {
				return true
			}
		}
		// No namespaces configured yet (unit/test hosts): allow localhost.
		if len(configured) == 0 {
			return true
		}
		return false
	}
	for _, c := range configured {
		if c == ns {
			return true
		}
	}
	return false
}

func formatNamespaceList(list []string) string {
	if len(list) == 0 {
		return "(none)"
	}
	out := make([]string, len(list))
	for i, n := range list {
		if n == "" {
			out[i] = recordsNamespaceLocalhost
		} else {
			out[i] = n
		}
	}
	return strings.Join(out, ", ")
}

func pathElemNames(path *gnmipb.Path) []string {
	var names []string
	for _, e := range path.GetElem() {
		if e != nil && e.GetName() != "" {
			names = append(names, e.GetName())
		}
	}
	return names
}
