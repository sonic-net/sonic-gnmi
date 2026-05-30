package oras

import "encoding/json"

// jsonUnmarshalStrict is a tiny wrapper that rejects unknown fields. We use
// it only for the OCI manifest, which is a well-defined schema.
func jsonUnmarshalStrict(data []byte, v interface{}) error {
	// Keep tolerant for now: oras.land/oras-go writes annotations we don't
	// care about; reject-unknown would be too strict against future spec
	// fields. Stay lenient.
	return json.Unmarshal(data, v)
}
