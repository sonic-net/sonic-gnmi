package client

import "strings"

// DecodeJsonPointer decodes JSON Pointer escaping per RFC 6901.
// ~1 is decoded to / and ~0 is decoded to ~.
// Order matters: ~1 must be decoded first to avoid turning ~01 into / instead of ~1.
func DecodeJsonPointer(s string) string {
	s = strings.ReplaceAll(s, "~1", "/")
	s = strings.ReplaceAll(s, "~0", "~")
	return s
}
