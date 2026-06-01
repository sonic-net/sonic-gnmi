package oras

import "encoding/json"

// parseManifest decodes an OCI image manifest. Lenient on unknown fields:
// oras-go writes annotations we don't care about, and rejecting unknown
// fields would also break against forward-compatible spec changes.
func parseManifest(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}
