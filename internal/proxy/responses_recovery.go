package proxy

// Stale-reasoning recovery for the Responses API.
//
// Meta reasoning models return encrypted reasoning blobs
// (`encrypted_content`) that are bound to the issuing caller identity. When a
// client replays history carrying blobs issued to a DIFFERENT caller (token
// rotation, direct API -> Console proxy switch, cross-account replay),
// upstream fails the request fast with:
//
//	[invalid_request_error] reasoning `encrypted_content` was not issued to this caller  (HTTP 400)
//
// The blobs carry no visible text, and reasoning items without a blob are
// simply skipped from stateless replays — so the proxy can strip them and
// retry the same request transparently instead of surfacing the error.

import (
	"encoding/json"
	"net/http"
	"strings"
)

// staleReasoningBlobKeys are the encrypted-reasoning payload fields across
// API spellings (the Responses wire format is snake_case; stored histories
// and some SDKs use camelCase).
var staleReasoningBlobKeys = []string{
	"encrypted_content",
	"reasoningEncryptedContent",
	"encryptedContent",
	"reasoning_encrypted_content",
}

// IsStaleReasoningError reports whether an upstream failure carries the stale
// encrypted-reasoning signature: HTTP 400 plus both marker substrings.
// Narrow on purpose — anything else must keep flowing to the normal relays.
func IsStaleReasoningError(status int, body []byte) bool {
	if status != http.StatusBadRequest || len(body) == 0 {
		return false
	}
	lowered := strings.ToLower(string(body))
	return strings.Contains(lowered, "encrypted_content") &&
		strings.Contains(lowered, "not issued to this caller")
}

// StripStaleReasoningBlobs removes stale encrypted-reasoning payloads from a
// Responses request body so a rejected request can be retried:
//
//   - blob keys are deleted from every input item,
//   - a reasoning item whose blob was stripped also loses its server-side id
//     (a dangling id reference is useless without its blob in stateless mode),
//   - a reasoning item left with no summary/content at all is dropped
//     entirely (an empty reasoning item is not valid input),
//   - everything else (function calls/outputs, text, tool defs) is untouched.
//
// It returns the cleaned body and the number of changes made (blob keys
// deleted + items dropped). Zero changes means there is nothing to fix and
// the caller must NOT retry (e.g. a previous_response_id chain whose poison
// lives server-side — replaying it cannot help).
func StripStaleReasoningBlobs(reqBody []byte) ([]byte, int, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(reqBody, &payload); err != nil {
		return nil, 0, err
	}
	raw, ok := payload["input"]
	if !ok || len(raw) == 0 || !strings.HasPrefix(strings.TrimSpace(string(raw)), "[") {
		return reqBody, 0, nil // string-form input carries no items
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, 0, err
	}
	changes := 0
	out := make([]map[string]json.RawMessage, 0, len(items))
	for _, item := range items {
		var typ struct {
			Type string `json:"type"`
		}
		if b, err := json.Marshal(item); err == nil {
			_ = json.Unmarshal(b, &typ)
		}
		removedHere := 0
		for _, k := range staleReasoningBlobKeys {
			if _, present := item[k]; present {
				delete(item, k)
				removedHere++
			}
		}
		changes += removedHere
		if typ.Type != "reasoning" {
			out = append(out, item)
			continue
		}
		if removedHere > 0 {
			delete(item, "id")
		}
		if reasoningItemEmpty(item) {
			changes++
			continue
		}
		out = append(out, item)
	}
	if changes == 0 {
		return reqBody, 0, nil
	}
	payload["input"], _ = json.Marshal(out)
	cleaned, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, err
	}
	return cleaned, changes, nil
}

// reasoningItemEmpty reports whether a reasoning item carries no replayable
// content: no summary text, no content parts, no plain text.
func reasoningItemEmpty(item map[string]json.RawMessage) bool {
	if raw, ok := item["summary"]; ok && len(raw) > 0 {
		var parts []struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(raw, &parts); err != nil {
			return false // shape we don't understand: keep the item
		}
		for _, p := range parts {
			if strings.TrimSpace(p.Text) != "" {
				return false
			}
		}
	}
	for _, k := range []string{"content", "text"} {
		if raw, ok := item[k]; ok {
			if s := strings.TrimSpace(string(raw)); s != "" && s != "null" && s != "[]" && s != `""` && s != "{}" {
				return false
			}
		}
	}
	return true
}
