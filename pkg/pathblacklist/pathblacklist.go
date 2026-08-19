// Package pathblacklist implements parsing and matching of a gNMI paths
// blacklist policy. The policy is a plain text file with one
// "TARGET /path/to/subtree" entry per line, e.g.:
//
//	COUNTERS_DB /COUNTERS/Ethernet0
//	APPL_DB /PORT_TABLE/Ethernet*
//	* /some/path
//	CONFIG_DB /
//
// TARGET is matched against the request target; "*" matches any target.
// PATH "/" blocks the entire target. Path elements are matched by name:
// "*" matches any element, and a trailing "*" acts as a prefix glob
// (e.g. "Ethernet*" matches "Ethernet0"), mirroring the wildcard forms
// accepted by the SONiC data clients.
//
// Matching is by subtree overlap: a request matches when the entry covers
// the request path or the request path covers the entry, since either way
// the response would include blacklisted data.
//
// This package is pure (no CGO, gRPC, or file I/O); callers open the policy
// file and translate a match into a transport-level error.
package pathblacklist

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

const wildcard = "*"

// Path is a normalized request path to match against the policy.
type Path struct {
	Target string
	Elems  []string
}

type entry struct {
	target string
	elems  []string
}

// String renders an entry in the policy file format, for logging.
func (e entry) String() string {
	return e.target + " /" + strings.Join(e.elems, "/")
}

// Policy is an immutable set of blacklisted paths. A nil *Policy matches
// nothing.
type Policy struct {
	entries []entry
}

// Parse reads blacklist entries from r.
func Parse(r io.Reader) (*Policy, error) {
	p := &Policy{}
	scanner := bufio.NewScanner(r)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("line %d: expected 'TARGET PATH', got %q", lineNum, line)
		}
		elems, err := parsePath(fields[1])
		if err != nil {
			return nil, fmt.Errorf("line %d: invalid path %q: %v", lineNum, fields[1], err)
		}
		p.entries = append(p.entries, entry{target: fields[0], elems: elems})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return p, nil
}

// parsePath splits "/a/b/c" into element names. "/" yields no elements,
// which blocks the entire target.
func parsePath(s string) ([]string, error) {
	if !strings.HasPrefix(s, "/") {
		return nil, fmt.Errorf("path must start with '/'")
	}
	if s == "/" {
		return nil, nil
	}
	elems := strings.Split(s[1:], "/")
	for _, elem := range elems {
		if elem == "" {
			return nil, fmt.Errorf("empty element")
		}
		if strings.ContainsAny(elem, "[]") {
			return nil, fmt.Errorf("[key=value] qualifiers are not supported, use element names only")
		}
	}
	return elems, nil
}

// Len returns the number of policy entries.
func (p *Policy) Len() int {
	if p == nil {
		return 0
	}
	return len(p.entries)
}

// Match reports whether the request path is covered by the policy. On a
// match it returns the matched entry in policy file format, for server-side
// logging.
func (p *Policy) Match(req Path) (string, bool) {
	if p == nil {
		return "", false
	}
	for _, e := range p.entries {
		if e.target != wildcard && e.target != req.Target {
			continue
		}
		if overlaps(e.elems, req.Elems) {
			return e.String(), true
		}
	}
	return "", false
}

// overlaps reports whether one path is a prefix of the other (or both are
// equal). Blocking in both directions is deliberate: a request deeper than
// a blacklisted subtree reads blacklisted data, and a request for an
// ancestor of a blacklisted subtree would include that subtree in its
// response.
func overlaps(entry, req []string) bool {
	n := len(entry)
	if len(req) < n {
		n = len(req)
	}
	for i := 0; i < n; i++ {
		if !elemMatch(entry[i], req[i]) {
			return false
		}
	}
	return true
}

// elemMatch reports whether two element names match. "*" matches anything,
// and a trailing "*" on either side is a prefix glob, so a request for
// "Ethernet*" cannot slip past a blacklisted "Ethernet0" (and vice versa).
func elemMatch(a, b string) bool {
	if a == b || a == wildcard || b == wildcard {
		return true
	}
	if strings.HasSuffix(a, wildcard) && strings.HasPrefix(b, a[:len(a)-1]) {
		return true
	}
	if strings.HasSuffix(b, wildcard) && strings.HasPrefix(a, b[:len(b)-1]) {
		return true
	}
	return false
}
