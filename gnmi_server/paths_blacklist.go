package gnmi

// Paths blacklist support: rejects gNMI RPCs (Get/Set/Subscribe) that
// reference blacklisted paths. The blacklist is loaded once at process
// startup from a plain text file passed via the telemetry -paths_blacklist
// flag.
//
// File format, one "TARGET /path/to/subtree" entry per line, e.g.:
//
//	COUNTERS_DB /COUNTERS/Ethernet0
//	* /some/path
//
// TARGET is matched against the request prefix target; "*" matches any
// target. Path elements are matched by name, "*" matches any element.
// Matching is by subtree overlap: a request is rejected when the blacklist
// entry covers the request path or the request path covers the entry, since
// either way the response would include blacklisted data.

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const blacklistWildcard = "*"

type blacklistEntry struct {
	target string
	elems  []string
}

// PathsBlacklist is an immutable set of blacklisted paths. A nil
// *PathsBlacklist is valid and blocks nothing.
type PathsBlacklist struct {
	entries []blacklistEntry
}

// LoadPathsBlacklist reads and parses a blacklist file.
func LoadPathsBlacklist(filename string) (*PathsBlacklist, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ParsePathsBlacklist(f, filename)
}

// ParsePathsBlacklist reads blacklist entries from r. name is used in error
// messages only.
func ParsePathsBlacklist(r io.Reader, name string) (*PathsBlacklist, error) {
	b := &PathsBlacklist{}
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
			return nil, fmt.Errorf("%s:%d: expected 'TARGET PATH', got %q", name, lineNum, line)
		}
		trimmed := strings.TrimPrefix(fields[1], "/")
		if trimmed == "" {
			return nil, fmt.Errorf("%s:%d: empty path %q", name, lineNum, fields[1])
		}
		elems := strings.Split(trimmed, "/")
		for _, elem := range elems {
			if elem == "" {
				return nil, fmt.Errorf("%s:%d: invalid path %q: empty element", name, lineNum, fields[1])
			}
			if strings.ContainsAny(elem, "[]") {
				return nil, fmt.Errorf("%s:%d: invalid path %q: [key=value] qualifiers are not supported, use element names only", name, lineNum, fields[1])
			}
		}
		b.entries = append(b.entries, blacklistEntry{target: fields[0], elems: elems})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%s: %v", name, err)
	}
	return b, nil
}

// Len returns the number of blacklist entries.
func (b *PathsBlacklist) Len() int {
	if b == nil {
		return 0
	}
	return len(b.entries)
}

// CheckPaths returns a PermissionDenied error if any of the given request
// paths, combined with the request prefix, is covered by the blacklist.
// A nil PathsBlacklist allows everything.
func (b *PathsBlacklist) CheckPaths(prefix *gnmipb.Path, paths []*gnmipb.Path) error {
	if b == nil || len(b.entries) == 0 {
		return nil
	}
	target := prefix.GetTarget()
	for _, path := range paths {
		reqElems := requestElemNames(prefix, path)
		for _, e := range b.entries {
			if e.target != blacklistWildcard && e.target != target {
				continue
			}
			if blacklistOverlaps(e.elems, reqElems) {
				return status.Errorf(codes.PermissionDenied,
					"request path %q is blacklisted (matched entry %q)",
					target+"/"+strings.Join(reqElems, "/"),
					e.target+" /"+strings.Join(e.elems, "/"))
			}
		}
	}
	return nil
}

// blacklistOverlaps reports whether one path is a prefix of the other (or
// both are equal), where "*" on either side matches any element name.
// Blocking in both directions is deliberate: a request deeper than a
// blacklisted subtree reads blacklisted data, and a request for an ancestor
// of a blacklisted subtree would include that subtree in its response.
func blacklistOverlaps(bl, req []string) bool {
	n := len(bl)
	if len(req) < n {
		n = len(req)
	}
	for i := 0; i < n; i++ {
		if bl[i] != blacklistWildcard && req[i] != blacklistWildcard && bl[i] != req[i] {
			return false
		}
	}
	return true
}

// requestElemNames flattens prefix + path into a list of element names.
// Paths using the deprecated Element field are supported for backward
// compatibility. Key qualifiers on elements are ignored; matching is by
// element name only.
func requestElemNames(prefix, path *gnmipb.Path) []string {
	var out []string
	if len(prefix.GetElem()) > 0 || len(path.GetElem()) > 0 {
		for _, pe := range prefix.GetElem() {
			out = append(out, pe.GetName())
		}
		for _, pe := range path.GetElem() {
			out = append(out, pe.GetName())
		}
		return out
	}
	out = append(out, prefix.GetElement()...)
	out = append(out, path.GetElement()...)
	return out
}
