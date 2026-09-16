package proxy

import "strings"

// IsNativeAnthropicModel reports whether a model id should use the native
// Anthropic Messages upstream path instead of OpenAI-compatible translation.
// Meta serves muse-* natively on /v1/messages (native content blocks,
// thinking, tool_use and stop_reason are preserved byte-for-byte).
func IsNativeAnthropicModel(model string) bool {
	model = strings.TrimSpace(strings.ToLower(model))
	if slash := strings.IndexByte(model, '/'); slash >= 0 {
		model = model[slash+1:]
	}
	return strings.HasPrefix(model, "muse-") ||
		strings.HasPrefix(model, "claude-") || strings.HasPrefix(model, "qwen")
}

// IsResponsesNativeModel reports whether a model id is served on the
// upstream Responses API (/v1/responses). Meta serves muse-spark* there
// natively (it is Meta's recommended full-featured surface: reasoning
// replay, tool loops, search grounding), so /v1/responses requests for
// these models are passed through instead of translated.
func IsResponsesNativeModel(model string) bool {
	model = strings.TrimSpace(strings.ToLower(model))
	if slash := strings.IndexByte(model, '/'); slash >= 0 {
		model = model[slash+1:]
	}
	return strings.HasPrefix(model, "muse-spark")
}
