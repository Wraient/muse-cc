package proxy

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConvertResponsesRequestForCodex(t *testing.T) {
	body := []byte(`{
		"model":"client-model",
		"instructions":"You are a coding agent.",
		"input":[
			{"type":"message","role":"developer","content":[{"type":"input_text","text":"Use PowerShell."}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"Inspect the repo."}]},
			{"type":"function_call","call_id":"call_123","name":"shell_command","arguments":"{\"command\":\"Get-ChildItem\"}"},
			{"type":"function_call_output","call_id":"call_123","output":"README.md"}
		],
		"tools":[
			{"type":"function","name":"shell_command","description":"Run a command","parameters":{"type":"object","properties":{"command":{"type":"string"}}}},
			{"type":"function","name":"apply_patch","description":"Patch files","parameters":{"type":"object"}},
			{"type":"web_search"}
		],
		"tool_choice":"auto",
		"parallel_tool_calls":false,
		"stream":true
	}`)
	req, err := ParseResponsesRequest(body)
	if err != nil {
		t.Fatalf("parse request: %v", err)
	}
	out, err := ConvertResponsesRequest(req, func(string) string { return "kimi-k2.7-code" })
	if err != nil {
		t.Fatalf("convert request: %v", err)
	}

	if out.Model != "kimi-k2.7-code" || !out.Stream {
		t.Fatalf("unexpected request header fields: %+v", out)
	}
	if out.StreamOptions == nil || !out.StreamOptions.IncludeUsage {
		t.Fatal("stream_options.include_usage was not enabled")
	}
	if out.ParallelToolCalls == nil || *out.ParallelToolCalls {
		t.Fatalf("parallel_tool_calls = %v, want false", out.ParallelToolCalls)
	}
	if len(out.Tools) != 2 ||
		out.Tools[0].Function.Name != "apply_patch" ||
		out.Tools[1].Function.Name != "shell_command" {
		t.Fatalf("function tools = %+v", out.Tools)
	}
	if len(out.Messages) != 5 {
		t.Fatalf("messages = %+v", out.Messages)
	}
	if out.Messages[0].Role != "system" || out.Messages[0].Content != "You are a coding agent." {
		t.Fatalf("instructions message = %+v", out.Messages[0])
	}
	if out.Messages[1].Role != "system" || out.Messages[1].Content != "Use PowerShell." {
		t.Fatalf("developer message = %+v", out.Messages[1])
	}
	if out.Messages[3].Role != "assistant" || len(out.Messages[3].ToolCalls) != 1 {
		t.Fatalf("function call message = %+v", out.Messages[3])
	}
	if out.Messages[4].Role != "tool" || out.Messages[4].ToolCallID != "call_123" {
		t.Fatalf("function output message = %+v", out.Messages[4])
	}
}

func TestConvertResponsesToAnthropicRequestForCodex(t *testing.T) {
	body := []byte(`{
		"model":"client-model",
		"instructions":"You are a coding agent.",
		"input":[
			{"type":"message","role":"developer","content":[{"type":"input_text","text":"Use PowerShell."}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"Inspect the repo."}]},
			{"type":"function_call","call_id":"call_123","name":"shell_command","arguments":"{\"command\":\"Get-ChildItem\"}"},
			{"type":"function_call_output","call_id":"call_123","output":"README.md"}
		],
		"tools":[
			{"type":"function","name":"z_shell","description":"Late tool","parameters":{"type":"object"}},
			{"type":"function","name":"shell_command","description":"Run a command","parameters":{"properties":{"command":{"type":"string"}}}}
		],
		"tool_choice":{"type":"function","name":"shell_command"},
		"max_output_tokens":256,
		"stream":true
	}`)
	req, err := ParseResponsesRequest(body)
	if err != nil {
		t.Fatalf("parse request: %v", err)
	}
	out, err := ConvertResponsesToAnthropicRequest(req, func(string) string { return "claude-sonnet-4-5" })
	if err != nil {
		t.Fatalf("convert request: %v", err)
	}

	if out.Model != "claude-sonnet-4-5" || out.MaxTokens != 256 || !out.Stream {
		t.Fatalf("unexpected request fields: %+v", out)
	}
	if len(out.System.Blocks) != 2 ||
		out.System.Blocks[0].Text != "You are a coding agent." ||
		out.System.Blocks[1].Text != "Use PowerShell." {
		t.Fatalf("system blocks = %+v", out.System.Blocks)
	}
	if len(out.Messages) != 3 {
		t.Fatalf("messages = %+v", out.Messages)
	}
	if out.Messages[1].Role != "assistant" ||
		out.Messages[1].Content.Blocks[0].Type != "tool_use" ||
		string(out.Messages[1].Content.Blocks[0].Input) != `{"command":"Get-ChildItem"}` {
		t.Fatalf("tool_use message = %+v", out.Messages[1])
	}
	if out.Messages[2].Role != "user" ||
		out.Messages[2].Content.Blocks[0].Type != "tool_result" {
		t.Fatalf("tool_result message = %+v", out.Messages[2])
	}
	if len(out.Tools) != 2 ||
		out.Tools[0].Name != "shell_command" ||
		out.Tools[1].Name != "z_shell" ||
		!strings.Contains(string(out.Tools[0].InputSchema), `"type":"object"`) {
		t.Fatalf("tools = %+v", out.Tools)
	}
	if out.ToolChoice.Type != "tool" || out.ToolChoice.Name != "shell_command" {
		t.Fatalf("tool_choice = %+v", out.ToolChoice)
	}
}

func TestConvertRequestSortsTools(t *testing.T) {
	req := &AnthropicRequest{
		Model:     "client-model",
		MaxTokens: 128,
		Messages: []AnthropicMessage{{
			Role: "user",
			Content: AnthropicMessageContent{
				IsStr: true,
				Text:  "hi",
			},
		}},
		Tools: []AnthropicTool{
			{Name: "z_tool", InputSchema: json.RawMessage(`{"type":"object"}`)},
			{Name: "a_tool", InputSchema: json.RawMessage(`{"type":"object"}`)},
		},
	}

	out := ConvertRequest(req, func(string) string { return "target-model" })
	if len(out.Tools) != 2 ||
		out.Tools[0].Function.Name != "a_tool" ||
		out.Tools[1].Function.Name != "z_tool" {
		t.Fatalf("tools = %+v", out.Tools)
	}
}

func TestResponsesDeveloperMessageMovesToSystemPrefix(t *testing.T) {
	body := []byte(`{
		"model":"client-model",
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"Question"}]},
			{"type":"message","role":"developer","content":[{"type":"input_text","text":"Stable rules"}]}
		]
	}`)
	req, err := ParseResponsesRequest(body)
	if err != nil {
		t.Fatalf("parse request: %v", err)
	}
	out, err := ConvertResponsesRequest(req, func(string) string { return "target-model" })
	if err != nil {
		t.Fatalf("convert request: %v", err)
	}
	if len(out.Messages) != 2 {
		t.Fatalf("messages = %+v", out.Messages)
	}
	if out.Messages[0].Role != "system" || out.Messages[0].Content != "Stable rules" {
		t.Fatalf("system prefix = %+v", out.Messages[0])
	}
	if out.Messages[1].Role != "user" || out.Messages[1].Content != "Question" {
		t.Fatalf("user message = %+v", out.Messages[1])
	}
}

func TestPromptCacheNormalizesRawOpenAIRequest(t *testing.T) {
	payload := map[string]json.RawMessage{
		"model":    json.RawMessage(`"target-model"`),
		"metadata": json.RawMessage(`{"trace":"keep","request_id":"drop","timestamp":"drop"}`),
		"tools": json.RawMessage(`[
			{"type":"function","function":{"name":"z_tool","parameters":{"type":"object"}}},
			{"type":"function","function":{"name":"a_tool","parameters":{"type":"object"}}}
		]`),
		"messages": json.RawMessage(`[
			{"role":"user","content":[
				{"type":"document","source":{"path":"b.md"}},
				{"type":"document","source":{"path":"a.md"}},
				{"type":"text","text":"Question"}
			]},
			{"role":"developer","content":"Stable rules"}
		]`),
	}

	ApplyRawOpenAIPromptCache(payload, PromptCacheOptions{
		Enabled:          true,
		KeyPrefix:        "test",
		NormalizePrompts: true,
	})

	var key string
	if err := json.Unmarshal(payload["prompt_cache_key"], &key); err != nil || !strings.HasPrefix(key, "test:") {
		t.Fatalf("prompt_cache_key = %q, err=%v", key, err)
	}
	var retention string
	if err := json.Unmarshal(payload["prompt_cache_retention"], &retention); err != nil || retention != promptCacheRetention {
		t.Fatalf("prompt_cache_retention = %q, err=%v", retention, err)
	}
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal(payload["metadata"], &metadata); err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if _, ok := metadata["request_id"]; ok {
		t.Fatalf("volatile metadata was preserved: %s", payload["metadata"])
	}
	if string(metadata["trace"]) != `"keep"` {
		t.Fatalf("non-volatile metadata was changed: %s", payload["metadata"])
	}
	var tools []struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(payload["tools"], &tools); err != nil {
		t.Fatalf("tools: %v", err)
	}
	if tools[0].Function.Name != "a_tool" || tools[1].Function.Name != "z_tool" {
		t.Fatalf("tools not sorted: %s", payload["tools"])
	}
	var messages []map[string]json.RawMessage
	if err := json.Unmarshal(payload["messages"], &messages); err != nil {
		t.Fatalf("messages: %v", err)
	}
	if rawString(messages[0]["role"]) != "developer" || rawString(messages[1]["role"]) != "user" {
		t.Fatalf("system/developer prefix not moved: %s", payload["messages"])
	}
	var content []json.RawMessage
	if err := json.Unmarshal(messages[1]["content"], &content); err != nil {
		t.Fatalf("content: %v", err)
	}
	var firstDoc, secondDoc struct {
		Source struct {
			Path string `json:"path"`
		} `json:"source"`
	}
	if err := json.Unmarshal(content[0], &firstDoc); err != nil {
		t.Fatalf("first doc: %v", err)
	}
	if err := json.Unmarshal(content[1], &secondDoc); err != nil {
		t.Fatalf("second doc: %v", err)
	}
	if firstDoc.Source.Path != "a.md" || secondDoc.Source.Path != "b.md" {
		t.Fatalf("document context not sorted: %s", payload["messages"])
	}
}

func TestRawPromptCacheKeyUsesFirstMessagePrefix(t *testing.T) {
	makeKey := func(userText string) string {
		messages, err := json.Marshal([]map[string]any{
			{"role": "user", "content": userText},
			{"role": "developer", "content": "Stable rules"},
		})
		if err != nil {
			t.Fatalf("messages: %v", err)
		}
		payload := map[string]json.RawMessage{
			"model":    json.RawMessage(`"target-model"`),
			"messages": json.RawMessage(messages),
		}
		ApplyRawOpenAIPromptCache(payload, PromptCacheOptions{
			Enabled:          true,
			KeyPrefix:        "test",
			NormalizePrompts: true,
		})
		var key string
		if err := json.Unmarshal(payload["prompt_cache_key"], &key); err != nil || key == "" {
			t.Fatalf("prompt_cache_key = %q, err=%v", key, err)
		}
		return key
	}

	stablePrefix := strings.Repeat("stable raw OpenAI context\n", promptCacheFirstMessagePrefixBytes/26+2)
	first := makeKey(stablePrefix + "turn A")
	second := makeKey(stablePrefix + "turn B")
	other := makeKey("different raw OpenAI context\n" + stablePrefix)

	if first != second {
		t.Fatalf("same first-message prefix should keep the same key: %q vs %q", first, second)
	}
	if first == other {
		t.Fatalf("different first-message prefixes should not share the same key: %q", first)
	}
}

func TestRawPromptCacheAddsRetentionWithProvidedKey(t *testing.T) {
	payload := map[string]json.RawMessage{
		"model":            json.RawMessage(`"target-model"`),
		"prompt_cache_key": json.RawMessage(`"client-key"`),
		"messages":         json.RawMessage(`[{"role":"user","content":"Question"}]`),
	}

	ApplyRawOpenAIPromptCache(payload, PromptCacheOptions{
		Enabled:          true,
		KeyPrefix:        "test",
		NormalizePrompts: true,
	})

	if string(payload["prompt_cache_key"]) != `"client-key"` {
		t.Fatalf("prompt_cache_key was overwritten: %s", payload["prompt_cache_key"])
	}
	var retention string
	if err := json.Unmarshal(payload["prompt_cache_retention"], &retention); err != nil || retention != promptCacheRetention {
		t.Fatalf("prompt_cache_retention = %q, err=%v", retention, err)
	}
}

func TestPrepareAnthropicPromptCacheBodyAddsCacheControl(t *testing.T) {
	out, err := PrepareAnthropicPromptCacheBody([]byte(`{
		"model":"client-model",
		"system":"Stable rules",
		"messages":[{"role":"user","content":"hi"}],
		"metadata":{"trace":"keep","request_id":"drop"}
	}`), "claude-sonnet-4-5", PromptCacheOptions{
		Enabled:               true,
		AnthropicCacheControl: true,
		NormalizePrompts:      true,
	})
	if err != nil {
		t.Fatalf("prepare body: %v", err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !strings.Contains(string(payload["system"]), `"cache_control"`) {
		t.Fatalf("cache_control missing: %s", payload["system"])
	}
	if strings.Contains(string(payload["metadata"]), "request_id") ||
		!strings.Contains(string(payload["metadata"]), "keep") {
		t.Fatalf("metadata normalization failed: %s", payload["metadata"])
	}
}

func TestConvertResponsesResponse(t *testing.T) {
	finish := "tool_calls"
	in := &OpenAIResponse{
		ID: "chatcmpl-test",
		Choices: []OpenAIChoice{{
			Index: 0,
			Message: &OpenAIMessage{
				Role:    "assistant",
				Content: "checking",
				ToolCalls: []OpenAIToolCall{{
					ID:   "call_123",
					Type: "function",
					Function: OpenAIFunctionCall{
						Name:      "shell_command",
						Arguments: `{"command":"Get-ChildItem"}`,
					},
				}},
			},
			FinishReason: &finish,
		}},
		Usage: OpenAIUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}

	out := ConvertResponsesResponse(in, "client-model")
	if out.Object != "response" || out.Status != "completed" || out.Model != "client-model" {
		t.Fatalf("unexpected response: %+v", out)
	}
	if len(out.Output) != 2 {
		t.Fatalf("output = %+v", out.Output)
	}
	if out.Output[0].Type != "message" || out.Output[0].Content[0].Text != "checking" {
		t.Fatalf("message output = %+v", out.Output[0])
	}
	if out.Output[1].Type != "function_call" || out.Output[1].CallID != "call_123" {
		t.Fatalf("tool output = %+v", out.Output[1])
	}
	if out.Usage == nil || out.Usage.TotalTokens != 15 {
		t.Fatalf("usage = %+v", out.Usage)
	}
}

func TestConvertResponsesResponsePreservesReasoningContent(t *testing.T) {
	resetReasoningContentCacheForTest()
	in := &OpenAIResponse{
		ID: "chatcmpl-reasoning",
		Choices: []OpenAIChoice{{
			Index: 0,
			Message: &OpenAIMessage{
				Role:             "assistant",
				ReasoningContent: "think it through",
				Content:          "done",
			},
			FinishReason: strPtr("stop"),
		}},
		Usage: OpenAIUsage{PromptTokens: 4, CompletionTokens: 3, TotalTokens: 7},
	}

	out := ConvertResponsesResponse(in, "client-model")
	if len(out.Output) != 2 {
		t.Fatalf("output = %+v", out.Output)
	}
	if out.Output[0].Type != "reasoning" ||
		len(out.Output[0].Summary) != 1 ||
		out.Output[0].Summary[0].Text != "think it through" {
		t.Fatalf("reasoning output = %+v", out.Output[0])
	}
	if out.Output[1].Type != "message" || out.Output[1].Content[0].Text != "done" {
		t.Fatalf("message output = %+v", out.Output[1])
	}
}

func TestResponsesReasoningItemBecomesChatReasoningContent(t *testing.T) {
	resetReasoningContentCacheForTest()
	body := []byte(`{
		"model":"deepseek-v4-flash",
		"input":[
			{"type":"reasoning","summary":[{"type":"summary_text","text":"think before tool"}]},
			{"type":"function_call","call_id":"call_123","name":"shell_command","arguments":"{\"command\":\"pwd\"}"}
		]
	}`)
	req, err := ParseResponsesRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	out, err := ConvertResponsesRequest(req, func(string) string { return "deepseek-v4-flash" })
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Messages) != 1 || out.Messages[0].Role != "assistant" {
		t.Fatalf("messages = %+v", out.Messages)
	}
	if out.Messages[0].ReasoningContent != "think before tool" {
		t.Fatalf("reasoning_content = %q", out.Messages[0].ReasoningContent)
	}
}

func TestResponsesReasoningContentReplayedAfterCompaction(t *testing.T) {
	resetReasoningContentCacheForTest()
	finish := "tool_calls"
	in := &OpenAIResponse{
		ID: "chatcmpl-reasoning-tool",
		Choices: []OpenAIChoice{{
			Index: 0,
			Message: &OpenAIMessage{
				Role:             "assistant",
				ReasoningContent: "cached responses reasoning",
				ToolCalls: []OpenAIToolCall{{
					ID:   "call_abc",
					Type: "function",
					Function: OpenAIFunctionCall{
						Name:      "shell_command",
						Arguments: `{"command":"pwd"}`,
					},
				}},
			},
			FinishReason: &finish,
		}},
		Usage: OpenAIUsage{PromptTokens: 4, CompletionTokens: 3, TotalTokens: 7},
	}
	out := ConvertResponsesResponse(in, "client-model")
	if len(out.Output) != 2 || out.Output[1].CallID != "call_abc" {
		t.Fatalf("output = %+v", out.Output)
	}

	body := []byte(`{
		"model":"deepseek-v4-flash",
		"input":[
			{"type":"function_call","call_id":"call_abc","name":"shell_command","arguments":"{\"command\":\"pwd\"}"}
		]
	}`)
	req, err := ParseResponsesRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	chatReq, err := ConvertResponsesRequest(req, func(string) string { return "deepseek-v4-flash" })
	if err != nil {
		t.Fatal(err)
	}
	if len(chatReq.Messages) != 1 || chatReq.Messages[0].ReasoningContent != "cached responses reasoning" {
		t.Fatalf("messages = %+v", chatReq.Messages)
	}
}

func TestConvertAnthropicResponseToResponses(t *testing.T) {
	stop := "tool_use"
	in := &AnthropicResponse{
		ID:    "msg_test",
		Type:  "message",
		Role:  "assistant",
		Model: "claude-sonnet-4-5",
		Content: []AnthropicContent{
			{Type: "text", Text: "checking"},
			{
				Type:  "tool_use",
				ID:    "call_123",
				Name:  "shell_command",
				Input: json.RawMessage(`{"command":"Get-ChildItem"}`),
			},
		},
		StopReason: &stop,
		Usage: AnthropicUsage{
			InputTokens:          12,
			OutputTokens:         6,
			CacheReadInputTokens: 5,
		},
	}

	out := ConvertAnthropicResponseToResponses(in, "client-model")
	if out.Object != "response" || out.Status != "completed" || out.Model != "client-model" {
		t.Fatalf("unexpected response: %+v", out)
	}
	if len(out.Output) != 2 {
		t.Fatalf("output = %+v", out.Output)
	}
	if out.Output[0].Type != "message" || out.Output[0].Content[0].Text != "checking" {
		t.Fatalf("message output = %+v", out.Output[0])
	}
	if out.Output[1].Type != "function_call" ||
		out.Output[1].CallID != "call_123" ||
		out.Output[1].Arguments != `{"command":"Get-ChildItem"}` {
		t.Fatalf("tool output = %+v", out.Output[1])
	}
	if out.Usage == nil || out.Usage.TotalTokens != 18 ||
		out.Usage.InputTokensDetails.CachedTokens != 5 {
		t.Fatalf("usage = %+v", out.Usage)
	}
}

func TestResponsesStreamConverterTextAndTool(t *testing.T) {
	var stream strings.Builder
	converter, err := NewResponsesStreamConverter(&stream, "client-model")
	if err != nil {
		t.Fatalf("new converter: %v", err)
	}
	if err := converter.HandleChunk(&OpenAIStreamChunk{
		Choices: []OpenAIChoice{{
			Delta: OpenAIDelta{Content: "I will inspect."},
		}},
	}); err != nil {
		t.Fatalf("text chunk: %v", err)
	}
	if err := converter.HandleChunk(&OpenAIStreamChunk{
		Choices: []OpenAIChoice{{
			Delta: OpenAIDelta{ToolCalls: []OpenAIToolCall{{
				Index: 0,
				ID:    "call_123",
				Type:  "function",
				Function: OpenAIFunctionCall{
					Name:      "shell_command",
					Arguments: `{"command":`,
				},
			}}},
		}},
	}); err != nil {
		t.Fatalf("tool start: %v", err)
	}
	finish := "tool_calls"
	if err := converter.HandleChunk(&OpenAIStreamChunk{
		Choices: []OpenAIChoice{{
			Delta: OpenAIDelta{ToolCalls: []OpenAIToolCall{{
				Index: 0,
				Function: OpenAIFunctionCall{
					Arguments: `"Get-ChildItem"}`,
				},
			}}},
			FinishReason: &finish,
		}},
		Usage: &OpenAIUsage{PromptTokens: 20, CompletionTokens: 8, TotalTokens: 28},
	}); err != nil {
		t.Fatalf("tool continuation: %v", err)
	}
	if err := converter.Finalize(); err != nil {
		t.Fatalf("finalize: %v", err)
	}

	out := stream.String()
	for _, want := range []string{
		"event: response.created",
		"event: response.output_text.delta",
		`"delta":"I will inspect."`,
		"event: response.function_call_arguments.delta",
		`"arguments":"{\"command\":\"Get-ChildItem\"}"`,
		"event: response.completed",
		`"input_tokens":20`,
		`"output_tokens":8`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stream missing %q:\n%s", want, out)
		}
	}

	for _, block := range strings.Split(strings.TrimSpace(out), "\n\n") {
		lines := strings.Split(block, "\n")
		if len(lines) != 2 || !strings.HasPrefix(lines[1], "data: ") {
			t.Fatalf("invalid SSE block: %q", block)
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(lines[1], "data: ")), &event); err != nil {
			t.Fatalf("invalid event JSON: %v", err)
		}
		if _, ok := event["sequence_number"]; !ok {
			t.Fatalf("event has no sequence_number: %v", event)
		}
	}
}

func TestResponsesStreamConverterReasoningContent(t *testing.T) {
	var stream strings.Builder
	converter, err := NewResponsesStreamConverter(&stream, "client-model")
	if err != nil {
		t.Fatalf("new converter: %v", err)
	}
	if err := converter.HandleChunk(&OpenAIStreamChunk{
		Choices: []OpenAIChoice{{
			Delta: OpenAIDelta{ReasoningContent: "first think"},
		}},
	}); err != nil {
		t.Fatalf("reasoning chunk: %v", err)
	}
	if err := converter.HandleChunk(&OpenAIStreamChunk{
		Choices: []OpenAIChoice{{
			Delta: OpenAIDelta{Content: "then answer"},
		}},
		Usage: &OpenAIUsage{PromptTokens: 9, CompletionTokens: 4, TotalTokens: 13},
	}); err != nil {
		t.Fatalf("text chunk: %v", err)
	}
	if err := converter.Finalize(); err != nil {
		t.Fatalf("finalize: %v", err)
	}

	out := stream.String()
	for _, want := range []string{
		"event: response.reasoning_summary_part.added",
		"event: response.reasoning_summary_text.delta",
		`"delta":"first think"`,
		"event: response.reasoning_summary_text.done",
		`"type":"reasoning"`,
		`"summary":[{"type":"summary_text","text":"first think"}]`,
		`"delta":"then answer"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stream missing %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "response.reasoning_summary_text.delta") > strings.Index(out, "response.output_text.delta") {
		t.Fatalf("reasoning should be emitted before text:\n%s", out)
	}
}
