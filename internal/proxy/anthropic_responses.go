package proxy

// Anthropic <-> Responses translation for Responses-native models.
//
// Meta muse-spark* models are served ONLY on the upstream Responses API, so
// POST /v1/messages for them is translated here instead of failing fast:
// Anthropic request -> Responses request (this file, Convert...Body),
// upstream /v1/responses, Responses response -> Anthropic response
// (ConvertResponsesToAnthropicResponse, ResponsesToAnthropicStreamer).
//
// Fidelity notes (v1): thinking blocks are dropped from history (Responses
// has no echo requirement); tool_result images ride along as follow-up
// input_image items (function_call_output is string-only); stop_sequences
// are not forwarded (Responses has no equivalent); temperature/top_p pass
// through.

import (
	"fmt"
	"strings"
)

// BridgeOptions tunes the Anthropic -> Responses translation.
type BridgeOptions struct {
	// PromptCacheKey is an opaque sticky key (may be "") — when set it is
	// forwarded as the upstream prompt_cache_key so repeated turns in one
	// session hit provider prompt cache.
	PromptCacheKey string
	// EffortLevels is the target model's ordered reasoning scale, low to
	// high; nil/empty falls back to DefaultReasoningLevels.
	EffortLevels []string
	// DefaultEffort is used when thinking is absent; "" means minimal.
	// Garbage values fall back to minimal, never to upstream default.
	DefaultEffort string
}

// ConvertAnthropicToResponsesBody builds an upstream Responses API request
// body from an Anthropic Messages request for targetModel.
func ConvertAnthropicToResponsesBody(in *AnthropicRequest, targetModel string, opts BridgeOptions) ([]byte, error) {
	if in == nil {
		return nil, fmt.Errorf("request is nil")
	}
	body := map[string]any{
		"model":  targetModel,
		"stream": in.Stream,
		"store":  false,
	}
	if len(in.System.Blocks) > 0 {
		var parts []string
		for _, b := range in.System.Blocks {
			if b.Type == "text" && b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		if len(parts) > 0 {
			body["instructions"] = strings.Join(parts, "\n")
		}
	}
	if in.MaxTokens > 0 {
		body["max_output_tokens"] = in.MaxTokens
	}
	// Reasoning effort: absent thinking means a plain tool-loop turn, which
	// runs ~4x faster on "minimal" than the upstream default (1.6s vs ~7s
	// on a trivial prompt, 2026-09-15 probe). Explicit thinking maps across
	// the model's real scale (names exact, unknown clamped to max).
	body["reasoning"] = map[string]any{
		"effort": ResolveBridgeEffort(in.Thinking, opts.EffortLevels, opts.DefaultEffort),
	}
	if opts.PromptCacheKey != "" {
		body["prompt_cache_key"] = opts.PromptCacheKey
		body["prompt_cache_retention"] = "24h"
	}
	if in.Temperature != nil {
		body["temperature"] = *in.Temperature
	}
	if in.TopP != nil {
		body["top_p"] = *in.TopP
	}
	items, err := anthropicMessagesToResponsesInput(in.Messages)
	if err != nil {
		return nil, err
	}
	body["input"] = items
	var tools []any
	for _, t := range in.Tools {
		if t.Name == "" {
			continue
		}
		tools = append(tools, map[string]any{
			"type":        "function",
			"name":        t.Name,
			"description": t.Description,
			"parameters":  ensureResponsesSchema(t.InputSchema),
		})
	}
	if len(tools) > 0 {
		body["tools"] = tools
	}
	// Meta Responses currently accepts only tool_choice "auto".
	body["tool_choice"] = "auto"
	return jsonMarshal(body)
}

// anthropicMessagesToResponsesInput maps conversation history to Responses
// input items. Assistant text uses output_text parts; tool_use/tool_result
// become function_call/function_call_output items sharing the call id.
func anthropicMessagesToResponsesInput(messages []AnthropicMessage) ([]any, error) {
	items := []any{}
	for _, m := range messages {
		role := m.Role
		if role != "user" && role != "assistant" {
			role = "user"
		}
		if m.Content.IsStr {
			if role == "assistant" || strings.TrimSpace(m.Content.Text) == "" {
				items = append(items, responsesTextMessage(role, m.Content.Text))
				continue
			}
			// User string content goes through the same video split as
			// text blocks: clients send simple turns as a bare string.
			spans, err := splitVideoMarkers(m.Content.Text)
			if err != nil {
				return nil, err
			}
			for _, s := range spans {
				switch {
				case s.isVideo:
					items = append(items, responsesVideoMessage(role, s.videoURL))
				case strings.TrimSpace(s.text) != "":
					items = append(items, responsesTextMessage(role, s.text))
				}
			}
			continue
		}
		var assistantText []string
		flushAssistantText := func() {
			if len(assistantText) == 0 {
				return
			}
			items = append(items, responsesTextMessage("assistant", strings.Join(assistantText, "")))
			assistantText = nil
		}
		for _, b := range m.Content.Blocks {
			switch b.Type {
			case "text":
				if role == "assistant" {
					assistantText = append(assistantText, b.Text)
				} else {
					// User text may carry [[video ...]] markers (the stock
					// clients cannot emit video blocks). Each marker becomes
					// a native input_video part in place.
					flushAssistantText()
					spans, err := splitVideoMarkers(b.Text)
					if err != nil {
						return nil, err
					}
					for _, s := range spans {
						switch {
						case s.isVideo:
							items = append(items, responsesVideoMessage(role, s.videoURL))
						case strings.TrimSpace(s.text) != "":
							items = append(items, responsesTextMessage(role, s.text))
						}
					}
				}
			case "image":
				flushAssistantText()
				if url := anthropicImageURL(b.Source); url != "" {
					items = append(items, map[string]any{
						"type": "message", "role": role,
						"content": []any{map[string]any{
							"type": "input_image", "image_url": url,
						}},
					})
				}
			case "video":
				// Proxy-specific block (no Anthropic equivalent): a video
				// attachment with a base64 or URL source. Goes upstream as
				// native input_video — never frame-split.
				flushAssistantText()
				url, err := anthropicVideoURL(b.Source)
				if err != nil {
					return nil, err
				}
				if url != "" {
					items = append(items, responsesVideoMessage(role, url))
				}
			case "document":
				// Stock clients cannot emit video blocks, so a document
				// block carrying video/mp4 is also accepted as video.
				// Anything else falls back to its text (usually "").
				flushAssistantText()
				if isVideoSource(b.Source) {
					url, err := anthropicVideoURL(b.Source)
					if err != nil {
						return nil, err
					}
					if url != "" {
						items = append(items, responsesVideoMessage(role, url))
						break
					}
				}
				if strings.TrimSpace(b.Text) != "" {
					items = append(items, responsesTextMessage(role, b.Text))
				}
			case "tool_use":
				flushAssistantText()
				callID := b.ID
				if callID == "" {
					callID = "call_" + randHex(24)
				}
				args := string(b.Input)
				if strings.TrimSpace(args) == "" {
					args = "{}"
				}
				items = append(items, map[string]any{
					"type": "function_call", "call_id": callID,
					"name": b.Name, "arguments": args,
				})
			case "tool_result":
				flushAssistantText()
				callID := b.ToolUseID
				if callID == "" {
					return nil, fmt.Errorf("tool_result block without tool_use_id")
				}
				items = append(items, map[string]any{
					"type": "function_call_output", "call_id": callID,
					"output": anthropicToolResultText(b),
				})
				// function_call_output is string-only: any image parts ride
				// along as follow-up user input_image items so visual tool
				// results (screenshots, Read images) still reach the model.
				for _, c := range anthropicToolResultImages(b) {
					items = append(items, map[string]any{
						"type": "message", "role": "user",
						"content": []any{map[string]any{
							"type": "input_image", "image_url": c,
						}},
					})
				}
			case "thinking":
				// No Responses echo requirement; dropped.
				continue
			default:
				// server_tool_use, web_search_tool_result and anything
				// server-side never appear in client requests; ignore.
				continue
			}
		}
		flushAssistantText()
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("no convertible messages in request")
	}
	return items, nil
}

func responsesTextMessage(role, text string) map[string]any {
	partType := "input_text"
	if role == "assistant" {
		partType = "output_text"
	}
	return map[string]any{
		"type": "message", "role": role,
		"content": []any{map[string]any{"type": partType, "text": text}},
	}
}

// maxVideoBytes caps a single inline base64 video payload. Staging probes
// (Sep 2026) showed upstream /v1/responses stalling on large inline video:
// 7MB took 65s with a retry, 10MB never returned headers. 8MB fails fast
// here instead of hanging the client ~110s and wedging a shared upstream
// slot. Local files over videoTranscodeThreshold are ffmpeg-transcoded down
// first (see video.go), so this cap normally only bites when ffmpeg is
// missing or the clip won't compress.
const maxVideoBytes = 8 << 20

// anthropicVideoURL resolves a video block's source to an upstream video_url:
// inline base64 mp4 becomes a data: URI, remote URLs pass through. A nil or
// empty source returns "". An explicit video block whose base64 source is not
// mp4 (or exceeds the cap) is an error — upstream accepts mp4 only.
func anthropicVideoURL(src *AnthropicImageSource) (string, error) {
	if src == nil {
		return "", nil
	}
	if src.URL != "" {
		return src.URL, nil
	}
	if src.Type != "base64" || src.Data == "" {
		return "", nil
	}
	media := src.MediaType
	if media == "" {
		media = "video/mp4"
	}
	if media != "video/mp4" {
		return "", fmt.Errorf("unsupported video media type %q: upstream accepts video/mp4 only", media)
	}
	if len(src.Data) > maxVideoBytes*4/3 {
		return "", fmt.Errorf("video payload exceeds the 20MB cap (%d bytes base64)", len(src.Data))
	}
	return "data:video/mp4;base64," + src.Data, nil
}

// isVideoSource reports whether a document block's source carries video (as
// opposed to a PDF or other file). Stock clients cannot emit video blocks, so
// this is the fallback ingress for video attachments. A url source counts as
// video only when its media type is empty or video/*: a document block
// pointing at e.g. a PDF URL must stay a document, otherwise upstream tries
// to download it as media and fails (media_url_origin_error).
func isVideoSource(src *AnthropicImageSource) bool {
	if src == nil {
		return false
	}
	if src.Type == "base64" {
		media := src.MediaType
		if media == "" {
			return false
		}
		return strings.HasPrefix(media, "video/")
	}
	if src.Type == "url" && src.URL != "" {
		return src.MediaType == "" || strings.HasPrefix(src.MediaType, "video/")
	}
	return false
}

func responsesVideoMessage(role, url string) map[string]any {
	return map[string]any{
		"type": "message", "role": role,
		"content": []any{map[string]any{
			"type": "input_video", "video_url": url,
		}},
	}
}

func anthropicImageURL(src *AnthropicImageSource) string {
	if src == nil {
		return ""
	}
	if src.URL != "" {
		return src.URL
	}
	if src.Type == "base64" && src.Data != "" {
		media := src.MediaType
		if media == "" {
			media = "image/png"
		}
		return "data:" + media + ";base64," + src.Data
	}
	return ""
}

// anthropicToolResultText keeps text parts; image parts have no
// function_call_output representation (see anthropicToolResultImages).
func anthropicToolResultText(b AnthropicContent) string {
	if b.Content == nil {
		return ""
	}
	if b.Content.IsStr {
		return b.Content.Text
	}
	var parts []string
	for _, c := range b.Content.Blocks {
		if c.Type == "text" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// anthropicToolResultImages extracts image URLs (data URI or remote) from a
// tool_result block for forwarding as input_image items.
func anthropicToolResultImages(b AnthropicContent) []string {
	if b.Content == nil || b.Content.IsStr {
		return nil
	}
	var out []string
	for _, c := range b.Content.Blocks {
		if c.Type == "image" {
			if url := anthropicImageURL(c.Source); url != "" {
				out = append(out, url)
			}
		}
	}
	return out
}

func ensureResponsesSchema(raw jsonRawMessage) any {
	if len(raw) == 0 {
		return map[string]any{"type": "object"}
	}
	var v any
	if err := jsonUnmarshal(raw, &v); err != nil {
		return map[string]any{"type": "object"}
	}
	if m, ok := v.(map[string]any); ok {
		if _, ok := m["type"]; !ok {
			m["type"] = "object"
		}
		return m
	}
	return map[string]any{"type": "object"}
}

// ConvertResponsesToAnthropicResponse translates a non-streaming upstream
// Responses API response into an Anthropic Messages response.
func ConvertResponsesToAnthropicResponse(raw []byte, incomingModel string) (*AnthropicResponse, error) {
	var in ResponsesResponse
	if err := jsonUnmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("could not parse upstream Responses response: %w", err)
	}
	out := &AnthropicResponse{
		ID:    "msg_" + randHex(24),
		Type:  "message",
		Role:  "assistant",
		Model: incomingModel,
	}
	stop := "end_turn"
	if in.Status == "incomplete" {
		stop = "max_tokens"
	}
	out.StopReason = &stop
	for _, item := range in.Output {
		switch item.Type {
		case "message":
			for _, p := range item.Content {
				if p.Type == "output_text" && p.Text != "" {
					out.Content = append(out.Content, AnthropicContent{Type: "text", Text: p.Text})
				}
			}
		case "function_call":
			callID := item.CallID
			if callID == "" {
				callID = "call_" + randHex(24)
			}
			args := item.Arguments
			if strings.TrimSpace(args) == "" {
				args = "{}"
			}
			out.Content = append(out.Content, AnthropicContent{
				Type: "tool_use", ID: callID, Name: item.Name,
				Input: jsonRawMessage(args),
			})
			toolStop := "tool_use"
			out.StopReason = &toolStop
		default:
			// reasoning summaries and anything else are dropped.
			continue
		}
	}
	if in.Usage != nil {
		out.Usage = AnthropicUsage{
			InputTokens:          in.Usage.InputTokens,
			OutputTokens:         in.Usage.OutputTokens,
			CacheReadInputTokens: in.Usage.InputTokensDetails.CachedTokens,
		}
	}
	if out.StopReason != nil && *out.StopReason == "tool_use" {
		// keep tool_use even on incomplete streams; max_tokens only wins
		// when no tool call was produced.
	} else if in.Status == "incomplete" {
		maxStop := "max_tokens"
		out.StopReason = &maxStop
	}
	return out, nil
}
