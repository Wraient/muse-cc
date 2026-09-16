package proxy

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStripDisabledThinking(t *testing.T) {
	muse := "muse-spark-1.3-contributor"
	disabled := `{"model":"m","max_tokens":64,"thinking":{"type":"disabled"},"messages":[]}`
	enabled := `{"model":"m","max_tokens":4096,"thinking":{"type":"enabled","budget_tokens":1024},"messages":[]}`
	plain := `{"model":"m","max_tokens":64,"messages":[]}`

	if out := StripDisabledThinking([]byte(disabled), muse); strings.Contains(string(out), "thinking") {
		t.Errorf("disabled not stripped: %s", out)
	}
	if out := StripDisabledThinking([]byte(enabled), muse); !strings.Contains(string(out), `"budget_tokens":1024`) {
		t.Errorf("enabled altered: %s", out)
	}
	if out := StripDisabledThinking([]byte(plain), muse); string(out) != plain {
		t.Errorf("plain altered: %s", out)
	}
	// Non-Meta targets pass through untouched.
	if out := StripDisabledThinking([]byte(disabled), "gpt-5-mini"); string(out) != disabled {
		t.Errorf("non-meta altered: %s", out)
	}
	// Provider prefixes are stripped before the family check.
	if out := StripDisabledThinking([]byte(disabled), "anthropic/muse-spark-1.3"); strings.Contains(string(out), "thinking") {
		t.Errorf("prefixed meta not stripped: %s", out)
	}
	// Garbage in, same garbage out (never errors).
	for _, bad := range []string{"", "{nope", "[]", `{"thinking":42}`} {
		if out := StripDisabledThinking([]byte(bad), muse); string(out) != bad {
			t.Errorf("bad input altered: %q -> %q", bad, out)
		}
	}
	var decoded map[string]any
	if err := json.Unmarshal(StripDisabledThinking([]byte(disabled), muse), &decoded); err != nil {
		t.Fatalf("stripped body is not valid JSON: %v", err)
	}
}
