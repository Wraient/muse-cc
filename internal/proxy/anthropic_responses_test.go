package proxy

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConvertAnthropicToResponsesBody(t *testing.T) {
	temp := 0.7
	body, err := ConvertAnthropicToResponsesBody(&AnthropicRequest{
		Model:       "muse-spark-1.3-contributor-free",
		System:      AnthropicSystem{Blocks: []AnthropicContent{{Type: "text", Text: "Be concise."}}},
		MaxTokens:   256,
		Temperature: &temp,
		Messages: []AnthropicMessage{
			{Role: "user", Content: AnthropicMessageContent{Text: "hi", IsStr: true}},
		},
		Tools: []AnthropicTool{{
			Name: "get_weather", Description: "city weather",
			InputSchema: jsonRawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`),
		}},
	}, "muse-spark-1.3-contributor-free", BridgeOptions{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if req["model"] != "muse-spark-1.3-contributor-free" {
		t.Errorf("model not rewritten: %v", req["model"])
	}
	if req["instructions"] != "Be concise." {
		t.Errorf("instructions: %v", req["instructions"])
	}
	if req["tool_choice"] != "auto" {
		t.Errorf("tool_choice must be auto: %v", req["tool_choice"])
	}
	if req["store"] != false {
		t.Errorf("store must be false: %v", req["store"])
	}
	input, _ := req["input"].([]any)
	if len(input) != 1 {
		t.Fatalf("expected 1 input item, got %d", len(input))
	}
	item, _ := input[0].(map[string]any)
	if item["role"] != "user" {
		t.Errorf("role: %v", item)
	}
	tools, _ := req["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %v", req["tools"])
	}
	tool, _ := tools[0].(map[string]any)
	if tool["type"] != "function" || tool["name"] != "get_weather" {
		t.Errorf("tool shape: %v", tool)
	}
}

func TestConvertAnthropicHistoryRoundTrip(t *testing.T) {
	body, err := ConvertAnthropicToResponsesBody(&AnthropicRequest{
		Model:     "muse-spark-1.3-contributor-free",
		MaxTokens: 64,
		Messages: []AnthropicMessage{
			{Role: "user", Content: AnthropicMessageContent{Text: "weather?", IsStr: true}},
			{Role: "assistant", Content: AnthropicMessageContent{Blocks: []AnthropicContent{
				{Type: "thinking", Thinking: "should check"},
				{Type: "text", Text: "checking"},
				{Type: "tool_use", ID: "call_1", Name: "get_weather", Input: jsonRawMessage(`{"city":"Paris"}`)},
			}}},
			{Role: "user", Content: AnthropicMessageContent{Blocks: []AnthropicContent{
				{Type: "tool_result", ToolUseID: "call_1", Content: &AnthropicMessageContent{Text: "sunny", IsStr: true}},
			}}},
		},
	}, "muse-spark-1.3-contributor-free", BridgeOptions{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var req struct {
		Input []struct {
			Type      string `json:"type"`
			Role      string `json:"role"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
			Output    string `json:"output"`
		} `json:"input"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(req.Input) != 4 {
		t.Fatalf("expected 4 items (thinking dropped), got %d: %s", len(req.Input), body)
	}
	if req.Input[1].Type != "message" || req.Input[1].Role != "assistant" {
		t.Errorf("assistant history item: %+v", req.Input[1])
	}
	if req.Input[2].Type != "function_call" || req.Input[2].CallID != "call_1" ||
		req.Input[2].Name != "get_weather" || !strings.Contains(req.Input[2].Arguments, "Paris") {
		t.Errorf("function_call item: %+v", req.Input[2])
	}
	if req.Input[3].Type != "function_call_output" || req.Input[3].CallID != "call_1" ||
		req.Input[3].Output != "sunny" {
		t.Errorf("function_call_output item: %+v", req.Input[3])
	}
}

func TestConvertAnthropicImageAndEmpty(t *testing.T) {
	body, err := ConvertAnthropicToResponsesBody(&AnthropicRequest{
		Model:     "muse-spark-1.3-contributor-free",
		MaxTokens: 64,
		Messages: []AnthropicMessage{
			{Role: "user", Content: AnthropicMessageContent{Blocks: []AnthropicContent{
				{Type: "image", Source: &AnthropicImageSource{Type: "base64", MediaType: "image/png", Data: "aGVsbG8="}},
			}}},
		},
	}, "muse-spark-1.3-contributor-free", BridgeOptions{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !strings.Contains(string(body), "input_image") || !strings.Contains(string(body), "data:image/png;base64,aGVsbG8=") {
		t.Errorf("image not mapped to input_image data URI: %s", body)
	}

	_, err = ConvertAnthropicToResponsesBody(&AnthropicRequest{
		Model: "muse-spark-1.3-contributor-free", MaxTokens: 64,
	}, "muse-spark-1.3-contributor-free", BridgeOptions{})
	if err == nil {
		t.Error("expected error for message-less request")
	}
}

func TestConvertAnthropicToolResultImageRidesAlong(t *testing.T) {
	body, err := ConvertAnthropicToResponsesBody(&AnthropicRequest{
		Model:     "muse-spark-1.3-contributor-free",
		MaxTokens: 64,
		Messages: []AnthropicMessage{
			{Role: "assistant", Content: AnthropicMessageContent{Blocks: []AnthropicContent{
				{Type: "tool_use", ID: "call_2", Name: "Read", Input: jsonRawMessage(`{"path":"/tmp/a.png"}`)},
			}}},
			{Role: "user", Content: AnthropicMessageContent{Blocks: []AnthropicContent{
				{Type: "tool_result", ToolUseID: "call_2", Content: &AnthropicMessageContent{Blocks: []AnthropicContent{
					{Type: "image", Source: &AnthropicImageSource{Type: "base64", MediaType: "image/png", Data: "aGVsbG8="}},
				}}},
			}}},
		},
	}, "muse-spark-1.3-contributor-free", BridgeOptions{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var req struct {
		Input []struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			CallID  string `json:"call_id"`
			Content []struct {
				Type     string `json:"type"`
				ImageURL string `json:"image_url"`
			} `json:"content"`
		} `json:"input"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(req.Input) != 3 {
		t.Fatalf("expected call + output + image items, got %d: %s", len(req.Input), body)
	}
	if req.Input[0].Type != "function_call" || req.Input[1].Type != "function_call_output" {
		t.Errorf("first items: %+v %+v", req.Input[0], req.Input[1])
	}
	img := req.Input[2]
	if img.Type != "message" || img.Role != "user" || len(img.Content) != 1 ||
		img.Content[0].Type != "input_image" ||
		img.Content[0].ImageURL != "data:image/png;base64,aGVsbG8=" {
		t.Errorf("image item: %+v", img)
	}
}

func TestConvertResponsesToAnthropicResponse(t *testing.T) {
	raw := []byte(`{
		"id":"resp_1","object":"response","status":"completed","model":"muse-spark-1.3-contributor-free",
		"output":[
			{"id":"msg_1","type":"message","status":"completed","role":"assistant",
			 "content":[{"type":"output_text","text":"Sunny!","annotations":[]}]},
			{"id":"fc_1","type":"function_call","status":"completed",
			 "call_id":"call_9","name":"get_weather","arguments":"{\"city\":\"Paris\"}"}
		],
		"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15,
			"input_tokens_details":{"cached_tokens":2},"output_tokens_details":{}}
	}`)
	out, err := ConvertResponsesToAnthropicResponse(raw, "client-model")
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if out.Role != "assistant" || out.Model != "client-model" {
		t.Errorf("envelope: %+v", out)
	}
	if out.StopReason == nil || *out.StopReason != "tool_use" {
		t.Errorf("stop_reason must be tool_use: %+v", out.StopReason)
	}
	if len(out.Content) != 2 || out.Content[0].Type != "text" || out.Content[0].Text != "Sunny!" {
		t.Errorf("text block: %+v", out.Content)
	}
	tu := out.Content[1]
	if tu.Type != "tool_use" || tu.ID != "call_9" || tu.Name != "get_weather" ||
		!strings.Contains(string(tu.Input), "Paris") {
		t.Errorf("tool_use block: %+v", tu)
	}
	if out.Usage.InputTokens != 10 || out.Usage.OutputTokens != 5 || out.Usage.CacheReadInputTokens != 2 {
		t.Errorf("usage: %+v", out.Usage)
	}
}

func TestConvertResponsesIncompleteMapsMaxTokens(t *testing.T) {
	raw := []byte(`{"id":"resp_2","object":"response","status":"incomplete","model":"m",
		"output":[{"id":"msg_2","type":"message","role":"assistant",
			"content":[{"type":"output_text","text":"partial","annotations":[]}]}],
		"usage":{"input_tokens":3,"output_tokens":9,"total_tokens":12,
			"input_tokens_details":{},"output_tokens_details":{}}}`)
	out, err := ConvertResponsesToAnthropicResponse(raw, "m")
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if out.StopReason == nil || *out.StopReason != "max_tokens" {
		t.Errorf("stop_reason must be max_tokens: %+v", out.StopReason)
	}
}

func TestConvertAnthropicReasoningEffort(t *testing.T) {
	msg := []AnthropicMessage{{Role: "user", Content: AnthropicMessageContent{Text: "hi", IsStr: true}}}
	effortOf := func(thinking *AnthropicThinking) string {
		t.Helper()
		body, err := ConvertAnthropicToResponsesBody(&AnthropicRequest{
			Model: "m", MaxTokens: 64, Messages: msg, Thinking: thinking,
		}, "m", BridgeOptions{})
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		var req struct {
			Reasoning struct {
				Effort string `json:"effort"`
			} `json:"reasoning"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return req.Reasoning.Effort
	}
	if got := effortOf(nil); got != "minimal" {
		t.Errorf("nil thinking must map to minimal, got %q", got)
	}
	if got := effortOf(&AnthropicThinking{Type: "disabled"}); got != "minimal" {
		t.Errorf("disabled thinking must map to minimal, got %q", got)
	}
	if got := effortOf(&AnthropicThinking{Type: "enabled", BudgetTokens: 1024}); got != "low" {
		t.Errorf("small budget must map to low, got %q", got)
	}
	if got := effortOf(&AnthropicThinking{Type: "enabled", BudgetTokens: 4096}); got != "medium" {
		t.Errorf("mid budget must map to medium, got %q", got)
	}
	if got := effortOf(&AnthropicThinking{Type: "enabled", BudgetTokens: 16000}); got != "high" {
		t.Errorf("large budget must map to high, got %q", got)
	}
}

func TestConvertAnthropicPromptCacheKey(t *testing.T) {
	msg := []AnthropicMessage{{Role: "user", Content: AnthropicMessageContent{Text: "hi", IsStr: true}}}
	body, err := ConvertAnthropicToResponsesBody(&AnthropicRequest{
		Model: "m", MaxTokens: 64, Messages: msg,
	}, "m", BridgeOptions{PromptCacheKey: "ses_sticky123"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if req["prompt_cache_key"] != "ses_sticky123" {
		t.Errorf("prompt_cache_key not forwarded: %v", req["prompt_cache_key"])
	}
	if req["prompt_cache_retention"] != "24h" {
		t.Errorf("prompt_cache_retention: %v", req["prompt_cache_retention"])
	}

	plain, err := ConvertAnthropicToResponsesBody(&AnthropicRequest{
		Model: "m", MaxTokens: 64, Messages: msg,
	}, "m", BridgeOptions{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if strings.Contains(string(plain), "prompt_cache_key") {
		t.Errorf("empty sticky key must omit prompt_cache_key: %s", plain)
	}
}

func TestConvertAnthropicReasoningTopBands(t *testing.T) {
	msg := []AnthropicMessage{{Role: "user", Content: AnthropicMessageContent{Text: "hi", IsStr: true}}}
	muse := []string{"minimal", "low", "medium", "high", "xhigh"}
	effortOf := func(thinking *AnthropicThinking, levels []string, def string) string {
		t.Helper()
		body, err := ConvertAnthropicToResponsesBody(&AnthropicRequest{
			Model: "m", MaxTokens: 64, Messages: msg, Thinking: thinking,
		}, "m", BridgeOptions{EffortLevels: levels, DefaultEffort: def})
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		var req struct {
			Reasoning struct {
				Effort string `json:"effort"`
			} `json:"reasoning"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return req.Reasoning.Effort
	}
	if got := effortOf(&AnthropicThinking{Type: "enabled", BudgetTokens: 20000}, muse, ""); got != "xhigh" {
		t.Errorf("20k budget must reach xhigh, got %q", got)
	}
	if got := effortOf(&AnthropicThinking{Type: "enabled", BudgetTokens: 31999}, muse, ""); got != "xhigh" {
		t.Errorf("ultrathink budget must clamp to xhigh, got %q", got)
	}
	if got := effortOf(&AnthropicThinking{Type: "enabled", Effort: "ultracode"}, muse, ""); got != "xhigh" {
		t.Errorf("unknown effort name must clamp to xhigh, got %q", got)
	}
	if got := effortOf(nil, muse, "xhigh"); got != "xhigh" {
		t.Errorf("xhigh default must be honored, got %q", got)
	}
	if got := effortOf(nil, muse, "bogus"); got != "minimal" {
		t.Errorf("bogus default must fall back to minimal, got %q", got)
	}
}
