package proxy

import (
	"encoding/json"
	"strings"
)

// StripDisabledThinking removes a `thinking: {"type": "disabled"}` block from
// an Anthropic Messages request body when the target is a Meta (muse-*)
// model. Meta translates `disabled` to `reasoning_effort: "none"`, which it
// then rejects with HTTP 400 — while an absent `thinking` field (the same
// "no extended thinking" semantics) returns 200. Claude Code sends the
// disabled form on some calls (e.g. session titles), so passing it through
// verbatim would fail turns that Meta is otherwise happy to serve.
//
// Non-Meta targets are returned untouched: other Anthropic-compatible
// upstreams accept the disabled form. Never errors: on any parse surprise
// the original body is returned.
func StripDisabledThinking(body []byte, targetModel string) []byte {
	if !isMetaTarget(targetModel) {
		return body
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil || payload == nil {
		return body
	}
	raw, ok := payload["thinking"]
	if len(raw) == 0 || !ok {
		return body
	}
	var thinking struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &thinking); err != nil {
		return body
	}
	if !strings.EqualFold(strings.TrimSpace(thinking.Type), "disabled") {
		return body
	}
	delete(payload, "thinking")
	fixed, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return fixed
}

func isMetaTarget(model string) bool {
	model = strings.TrimSpace(strings.ToLower(model))
	if slash := strings.IndexByte(model, '/'); slash >= 0 {
		model = model[slash+1:]
	}
	return strings.HasPrefix(model, "muse-")
}
