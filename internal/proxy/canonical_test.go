package proxy

import (
	"testing"
)

// TestCanonicalJSONStable asserts that two JSON values with the same content
// but different key ordering produce identical bytes — this is what keeps the
// upstream token-prefix prompt cache hitting across rounds. Covers nested
// objects and objects inside arrays.
func TestCanonicalJSONStable(t *testing.T) {
	cases := []struct{ name, a, b string }{
		{
			"top-level key order",
			`{"b":1,"a":2,"c":3}`,
			`{"c":3,"a":2,"b":1}`,
		},
		{
			"nested object key order",
			`{"outer":{"z":1,"a":2}}`,
			`{"outer":{"a":2,"z":1}}`,
		},
		{
			"object inside array",
			`{"list":[{"y":"1","x":"0"},{"b":2,"a":1}]}`,
			`{"list":[{"x":"0","y":"1"},{"a":1,"b":2}]}`,
		},
		{
			"deeply nested",
			`{"a":{"d":{"c":3,"a":1},"b":2}}`,
			`{"a":{"b":2,"d":{"a":1,"c":3}}}`,
		},
	}
	for _, tc := range cases {
		ca, oka := canonicalJSON(jsonRawMessage(tc.a))
		cb, okb := canonicalJSON(jsonRawMessage(tc.b))
		if !oka || !okb {
			t.Errorf("%s: canonicalJSON returned not-ok", tc.name)
			continue
		}
		if string(ca) != string(cb) {
			t.Errorf("%s: outputs differ\n a -> %s\n b -> %s", tc.name, ca, cb)
		}
	}
}

// TestCanonicalJSONPreservesNumberPrecision ensures json.Number is kept so large
// integers are not re-encoded as floats (which would change bytes and semantics).
func TestCanonicalJSONPreservesNumberPrecision(t *testing.T) {
	in := jsonRawMessage(`{"id":9007199254740993,"n":1.5}`)
	out, ok := canonicalJSON(in)
	if !ok {
		t.Fatalf("canonicalJSON returned not-ok")
	}
	// The big integer must remain an integer (no scientific notation / float).
	s := string(out)
	if !contains(s, `"id":9007199254740993`) {
		t.Errorf("large integer not preserved exactly: %s", s)
	}
}

// TestCanonicalJSONRejectsTrailing keeps the strict single-value contract.
func TestCanonicalJSONRejectsTrailing(t *testing.T) {
	_, ok := canonicalJSON(jsonRawMessage(`{"a":1}{"b":2}`))
	if ok {
		t.Errorf("expected rejection of trailing content, got ok")
	}
}

// TestEnsureObjectSchemaStable verifies the same logical schema produces
// identical bytes regardless of input key order, including when "type" must be
// injected. This sits in the tools prefix, so byte stability directly affects
// cache hits.
func TestEnsureObjectSchemaStable(t *testing.T) {
	cases := []struct{ name, a, b string }{
		{
			"key order differs",
			`{"properties":{"cmd":{"type":"string"}},"required":["cmd"],"type":"object"}`,
			`{"type":"object","required":["cmd"],"properties":{"cmd":{"type":"string"}}}`,
		},
		{
			"type injected both sides",
			`{"properties":{"x":{"type":"number","description":"d"}}}`,
			`{"type":"object","properties":{"x":{"description":"d","type":"number"}}}`,
		},
		{
			"nested properties order",
			`{"properties":{"z":{"type":"string"},"a":{"type":"number"}},"type":"object"}`,
			`{"type":"object","properties":{"a":{"type":"number"},"z":{"type":"string"}}}`,
		},
	}
	for _, tc := range cases {
		ea := ensureObjectSchema(jsonRawMessage(tc.a))
		eb := ensureObjectSchema(jsonRawMessage(tc.b))
		if string(ea) != string(eb) {
			t.Errorf("%s: outputs differ\n a -> %s\n b -> %s", tc.name, ea, eb)
		}
	}
}

// TestEnsureObjectSchemaAddsType confirms the type:object is always present.
func TestEnsureObjectSchemaAddsType(t *testing.T) {
	out := ensureObjectSchema(jsonRawMessage(`{"properties":{"a":{"type":"string"}}}`))
	if !contains(string(out), `"type":"object"`) {
		t.Errorf("type:object not added: %s", out)
	}
}

// TestEnsureObjectSchemaEmptyAndInvalid covers the edge cases.
func TestEnsureObjectSchemaEmptyAndInvalid(t *testing.T) {
	if got := string(ensureObjectSchema(nil)); got != `{"type":"object"}` {
		t.Errorf("empty schema: got %s", got)
	}
	if got := string(ensureObjectSchema(jsonRawMessage(`not json`))); got != `{"type":"object"}` {
		t.Errorf("invalid schema: got %s", got)
	}
}

// contains is a tiny local helper to avoid importing strings just for tests.
func contains(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
