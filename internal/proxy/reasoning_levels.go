package proxy

// Reasoning-effort levels for the Responses API.
//
// Different models accept different effort scales (verified live 2026-09-15:
// muse-spark-1.3 takes minimal/low/medium/high/xhigh and rejects none, max,
// and anything else with HTTP 400 — upstream is case-sensitive). This file
// holds the pure mapping logic shared by the bridge (Anthropic thinking ->
// effort) and the passthrough (client effort validation/clamping):
//
//   - NormalizeEffort: case-insensitive exact match to canonical form.
//   - ClampEffort: unknown names clamp to the model's maximum level (never
//     fail a request the client meant as "a lot of thinking"; the only
//     exception is "none", which explicitly means minimum and clamps down).
//   - ResolveBridgeEffort: full thinking -> effort resolution over a
//     caller-supplied ordered level list (low to high).
//
// The level lists themselves come from the server layer (built-in family
// table + upstream-taught cache); see internal/server/reasoning_levels.go.

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
)

// DefaultReasoningLevels is the fallback effort scale (low to high) for
// models with no known level list. It matches the OpenAI-documented set plus
// xhigh, which most 2026 reasoning models accept; wrong guesses self-heal
// via invalid-effort learning on the server layer.
var DefaultReasoningLevels = []string{"minimal", "low", "medium", "high", "xhigh"}

// maxReasoningBudget is the Anthropic ceiling used to scale thinking budgets
// across the effort range (ultrathink-class budgets land on the top band).
const maxReasoningBudget = 32000

// NormalizeEffort returns the canonical form of want when it matches a known
// level case-insensitively, or ("", false) when unknown.
func NormalizeEffort(want string, levels []string) (string, bool) {
	for _, lv := range levels {
		if strings.EqualFold(strings.TrimSpace(want), lv) {
			return lv, true
		}
	}
	return "", false
}

// ClampEffort maps a requested effort name into a known ordered level list
// (low to high). Exact matches (case-insensitive) resolve to canonical form.
// Known tokens outside the model's range clamp to the near end ("minimal"
// on a [low..high] model becomes "low"); genuinely unknown names ("ultra",
// "ultracode") clamp to the maximum — a client asking for an unrecognized
// level meant "a lot of thinking", and failing the request helps nobody.
// An empty list returns want unchanged (caller has no information; leave
// the request alone).
func ClampEffort(want string, levels []string) string {
	if len(levels) == 0 {
		return want
	}
	if canonical, ok := NormalizeEffort(want, levels); ok {
		return canonical
	}
	want = strings.TrimSpace(want)
	if wi := indexFold(universalEffortOrder, want); wi >= 0 {
		if mi := indexFold(universalEffortOrder, levels[0]); mi >= 0 && wi < mi {
			return levels[0]
		}
	}
	return levels[len(levels)-1]
}

// universalEffortOrder is the full known scale, low to high, used only to
// decide which end an out-of-range (but recognized) token clamps to.
var universalEffortOrder = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}

func indexFold(list []string, want string) int {
	for i, s := range list {
		if strings.EqualFold(s, want) {
			return i
		}
	}
	return -1
}

// ResolveBridgeEffort resolves an Anthropic thinking config to a Responses
// effort name over levels (ordered low to high):
//
//   - absent thinking -> defaultEffort (fast path; usually "minimal"),
//   - disabled (or unknown) thinking -> the practical minimum ("minimal"
//     when present),
//   - enabled/adaptive/auto thinking with an explicit effort name ->
//     ClampEffort (exact, else model maximum),
//   - enabled/adaptive/auto thinking with a budget -> the budget picks a
//     band ((0,2048] low, (2048,8192] medium, (8192,16384] high,
//     (16384,24576] xhigh, above that the top band), then clamped into
//     levels, so ultrathink-class budgets land on the model's real maximum
//     whatever it is.
//
// defaultEffort is validated against levels (garbage falls back to minimal,
// else levels[0]); an empty levels list falls back to DefaultReasoningLevels.
func ResolveBridgeEffort(thinking *AnthropicThinking, levels []string, defaultEffort string) string {
	if len(levels) == 0 {
		levels = DefaultReasoningLevels
	}
	def, ok := NormalizeEffort(defaultEffort, levels)
	if !ok {
		def, _ = NormalizeEffort("minimal", levels)
		if def == "" {
			def = levels[0]
		}
	}
	if thinking == nil {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(thinking.Type)) {
	case "enabled", "adaptive", "auto":
		if strings.TrimSpace(thinking.Effort) != "" {
			return ClampEffort(thinking.Effort, levels)
		}
		return ClampEffort(budgetBand(thinking.BudgetTokens), levels)
	default:
		if m, ok := NormalizeEffort("minimal", levels); ok {
			return m
		}
		return levels[0]
	}
}

// budgetBand maps a thinking budget to a canonical effort name from the full
// superset scale; the caller clamps it into the model's real levels.
func budgetBand(budget int) string {
	switch {
	case budget <= 0:
		return "low"
	case budget <= 2048:
		return "low"
	case budget <= 8192:
		return "medium"
	case budget <= 16384:
		return "high"
	case budget <= 24576:
		return "xhigh"
	default:
		return "max"
	}
}

var (
	// `reasoning.effort`: unknown variant `ultra`, expected one of `none`, `minimal`, ...
	expectedOneOfRe = regexp.MustCompile("(?i)expected one of\\s*((?:`[^`]+`\\s*,?\\s*)+)")
	// reasoning_effort 'none' is not supported for model 'm'. Supported values: [minimal, low, ...]
	supportedValuesRe = regexp.MustCompile(`(?i)supported values:\s*\[([^\]]+)\]`)
	backtickItemRe    = regexp.MustCompile("`([^`]+)`")
)

// ParseExpectedEfforts extracts the effort set an upstream invalid-effort
// error message teaches us, preserving the message's (ascending) order.
// Understands both observed shapes; returns nil when the message teaches
// nothing. Tokens are lowercased and restricted to plausible level names so
// a weird message can never poison the cache with garbage.
func ParseExpectedEfforts(errMsg string) []string {
	if m := expectedOneOfRe.FindStringSubmatch(errMsg); m != nil {
		return cleanEffortTokens(backtickItemRe.FindAllStringSubmatch(m[1], -1))
	}
	if m := supportedValuesRe.FindStringSubmatch(errMsg); m != nil {
		var out []string
		for _, tok := range strings.Split(m[1], ",") {
			tok = strings.Trim(strings.TrimSpace(tok), "`'\"")
			if isPlausibleEffort(tok) {
				out = append(out, strings.ToLower(tok))
			}
		}
		return dedupeStrings(out)
	}
	return nil
}

func cleanEffortTokens(matches [][]string) []string {
	var out []string
	for _, m := range matches {
		if len(m) < 2 || !isPlausibleEffort(m[1]) {
			continue
		}
		out = append(out, strings.ToLower(m[1]))
	}
	return dedupeStrings(out)
}

func isPlausibleEffort(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 24 {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// IsInvalidEffortError reports whether an upstream failure is an
// invalid-reasoning-effort rejection (HTTP 400 naming the effort). Shapes
// observed live: {"error":{"param":"reasoning.effort",...}} plus messages
// carrying "unknown variant", "not supported for model", or
// "Supported values:". Narrow on purpose: the generic "invalid parameters"
// 400 does NOT match (it names nothing actionable).
func IsInvalidEffortError(status int, body []byte) bool {
	if status != http.StatusBadRequest || len(body) == 0 {
		return false
	}
	var env struct {
		Error struct {
			Param   *string `json:"param"`
			Message string  `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err == nil {
		if env.Error.Param != nil && *env.Error.Param == "reasoning.effort" {
			return true
		}
		lowered := strings.ToLower(env.Error.Message)
		if strings.Contains(lowered, "unknown variant") ||
			strings.Contains(lowered, "not supported for model") ||
			strings.Contains(lowered, "supported values:") {
			return true
		}
		return false
	}
	// Unparseable body: only the strongest marker counts.
	return strings.Contains(strings.ToLower(string(body)), "reasoning.effort")
}

// ExtractRequestEffort reads reasoning.effort from a Responses request body.
// ok is false when the field is absent or malformed (nothing to clamp).
func ExtractRequestEffort(reqBody []byte) (effort string, ok bool) {
	var payload struct {
		Reasoning *struct {
			Effort string `json:"effort"`
		} `json:"reasoning"`
	}
	if err := json.Unmarshal(reqBody, &payload); err != nil {
		return "", false
	}
	if payload.Reasoning == nil || strings.TrimSpace(payload.Reasoning.Effort) == "" {
		return "", false
	}
	return payload.Reasoning.Effort, true
}

// SetRequestEffort rewrites reasoning.effort in a Responses request body,
// preserving every other field byte-for-byte. It creates the reasoning
// object when absent.
func SetRequestEffort(reqBody []byte, effort string) ([]byte, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(reqBody, &payload); err != nil {
		return nil, err
	}
	var reasoning map[string]json.RawMessage
	if raw, ok := payload["reasoning"]; ok && len(raw) > 0 {
		if err := json.Unmarshal(raw, &reasoning); err != nil {
			return nil, err
		}
	} else {
		reasoning = map[string]json.RawMessage{}
	}
	encoded, err := json.Marshal(effort)
	if err != nil {
		return nil, err
	}
	reasoning["effort"] = encoded
	payload["reasoning"], err = json.Marshal(reasoning)
	if err != nil {
		return nil, err
	}
	return json.Marshal(payload)
}
