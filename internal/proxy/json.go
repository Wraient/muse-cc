package proxy

import "encoding/json"

// jsonRawMessage is json.RawMessage with a friendly alias so the type files
// read cleanly. It lets us carry schema blobs through without fully decoding.
type jsonRawMessage = json.RawMessage

// jsonUnmarshal is a thin wrapper to keep call sites short.
func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

// jsonMarshal is a thin wrapper to keep call sites short.
func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }
