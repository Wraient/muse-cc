package proxy

import (
	"reflect"
	"testing"
)

var testMuseLevels = []string{"minimal", "low", "medium", "high", "xhigh"}

func TestClampEffort(t *testing.T) {
	cases := []struct {
		want   string
		levels []string
		expect string
	}{
		{"high", testMuseLevels, "high"},
		{"XHIGH", testMuseLevels, "xhigh"},
		{"  Medium ", testMuseLevels, "medium"},
		{"ultra", testMuseLevels, "xhigh"},
		{"ultracode", testMuseLevels, "xhigh"},
		{"max", testMuseLevels, "xhigh"}, // listed upstream but 400s: clamp down
		{"none", testMuseLevels, "minimal"},
		{"minimal", []string{"low", "medium", "high"}, "low"},
		{"max", []string{"low", "medium", "high"}, "high"},
		{"whatever", []string{"low", "medium", "high"}, "high"},
		{"high", nil, "high"},
	}
	for _, c := range cases {
		if got := ClampEffort(c.want, c.levels); got != c.expect {
			t.Errorf("ClampEffort(%q, %v) = %q, want %q", c.want, c.levels, got, c.expect)
		}
	}
}

func TestResolveBridgeEffort(t *testing.T) {
	cases := []struct {
		name     string
		thinking *AnthropicThinking
		levels   []string
		def      string
		expect   string
	}{
		{"nil thinking uses default", nil, testMuseLevels, "minimal", "minimal"},
		{"nil thinking honors xhigh default", nil, testMuseLevels, "xhigh", "xhigh"},
		{"garbage default falls back", nil, testMuseLevels, "bogus", "minimal"},
		{"empty levels fall back", nil, nil, "", "minimal"},
		{"disabled means minimum", &AnthropicThinking{Type: "disabled"}, testMuseLevels, "xhigh", "minimal"},
		{"unknown type means minimum", &AnthropicThinking{Type: "fancy"}, testMuseLevels, "xhigh", "minimal"},
		{"effort exact", &AnthropicThinking{Type: "enabled", Effort: "medium"}, testMuseLevels, "minimal", "medium"},
		{"effort case", &AnthropicThinking{Type: "enabled", Effort: "XHIGH"}, testMuseLevels, "minimal", "xhigh"},
		{"effort max clamps to xhigh", &AnthropicThinking{Type: "enabled", Effort: "max"}, testMuseLevels, "minimal", "xhigh"},
		{"effort ultracode clamps to xhigh", &AnthropicThinking{Type: "enabled", Effort: "ultracode"}, testMuseLevels, "minimal", "xhigh"},
		{"effort none clamps down", &AnthropicThinking{Type: "enabled", Effort: "none"}, testMuseLevels, "minimal", "minimal"},
		{"budget 0 is low", &AnthropicThinking{Type: "enabled"}, testMuseLevels, "minimal", "low"},
		{"budget 1k low", &AnthropicThinking{Type: "enabled", BudgetTokens: 1024}, testMuseLevels, "minimal", "low"},
		{"budget 4k medium", &AnthropicThinking{Type: "enabled", BudgetTokens: 4096}, testMuseLevels, "minimal", "medium"},
		{"budget 12k high", &AnthropicThinking{Type: "enabled", BudgetTokens: 12000}, testMuseLevels, "minimal", "high"},
		{"budget 20k xhigh", &AnthropicThinking{Type: "enabled", BudgetTokens: 20000}, testMuseLevels, "minimal", "xhigh"},
		{"ultrathink budget hits top", &AnthropicThinking{Type: "enabled", BudgetTokens: 31999}, testMuseLevels, "minimal", "xhigh"},
		{"adaptive with budget maps", &AnthropicThinking{Type: "adaptive", BudgetTokens: 4096}, testMuseLevels, "minimal", "medium"},
		{"narrow levels clamp budget", &AnthropicThinking{Type: "enabled", BudgetTokens: 31999}, []string{"low", "medium", "high"}, "minimal", "high"},
	}
	for _, c := range cases {
		if got := ResolveBridgeEffort(c.thinking, c.levels, c.def); got != c.expect {
			t.Errorf("%s: got %q, want %q", c.name, got, c.expect)
		}
	}
}

func TestParseExpectedEfforts(t *testing.T) {
	variant := "Error from provider (Console): Upstream request failed: [invalid_request_error] `reasoning.effort`: unknown variant `ultra`, expected one of `none`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max`"
	if got := ParseExpectedEfforts(variant); !reflect.DeepEqual(got, []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}) {
		t.Errorf("variant shape: %v", got)
	}
	supported := "reasoning_effort 'none' is not supported for model 'm'. Supported values: [minimal, low, medium, high, xhigh, max]"
	if got := ParseExpectedEfforts(supported); !reflect.DeepEqual(got, []string{"minimal", "low", "medium", "high", "xhigh", "max"}) {
		t.Errorf("supported-values shape: %v", got)
	}
	if got := ParseExpectedEfforts("The request contains invalid parameters."); got != nil {
		t.Errorf("generic message must teach nothing: %v", got)
	}
	if got := ParseExpectedEfforts(""); got != nil {
		t.Errorf("empty must teach nothing: %v", got)
	}
}

func TestIsInvalidEffortError(t *testing.T) {
	param := `{"error":{"param":"reasoning.effort","type":"invalid_request_error","message":"unknown variant"}}`
	if !IsInvalidEffortError(400, []byte(param)) {
		t.Error("param shape must match")
	}
	msg := `{"error":{"type":"invalid_request_error","message":"reasoning_effort 'none' is not supported for model 'm'. Supported values: [low]"}}`
	if !IsInvalidEffortError(400, []byte(msg)) {
		t.Error("supported-values shape must match")
	}
	generic := `{"error":{"type":"invalid_request_error","message":"The request contains invalid parameters."}}`
	if IsInvalidEffortError(400, []byte(generic)) {
		t.Error("generic 400 must not match")
	}
	enc := `{"error":{"type":"invalid_request_error","message":"reasoning ` + "`encrypted_content`" + ` was not issued to this caller"}}`
	if IsInvalidEffortError(400, []byte(enc)) {
		t.Error("stale-reasoning 400 must not match (separate recovery)")
	}
	if IsInvalidEffortError(500, []byte(param)) {
		t.Error("non-400 must not match")
	}
}

func TestRequestEffortRoundTrip(t *testing.T) {
	raw := []byte(`{"model":"m","reasoning":{"effort":"ultra","x":1},"input":[]}`)
	eff, ok := ExtractRequestEffort(raw)
	if !ok || eff != "ultra" {
		t.Fatalf("extract: %q %v", eff, ok)
	}
	out, err := SetRequestEffort(raw, "xhigh")
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if eff, ok := ExtractRequestEffort(out); !ok || eff != "xhigh" {
		t.Fatalf("round trip: %q %v (%s)", eff, ok, out)
	}
	if _, ok := ExtractRequestEffort([]byte(`{"model":"m"}`)); ok {
		t.Error("absent effort must report false")
	}
	if _, err := SetRequestEffort([]byte(`{nope`), "x"); err == nil {
		t.Error("invalid JSON must error")
	}
}
