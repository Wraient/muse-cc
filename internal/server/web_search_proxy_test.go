package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wraient/muse-cc/internal/config"
	"github.com/Wraient/muse-cc/internal/proxy"
)

func TestWebSearchShimNonStreamReturnsServerToolBlocks(t *testing.T) {
	restoreWebSearchProvider(t)

	var upstreamCalls int
	var sawSearchContext bool
	var upstreamModels []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		var body struct {
			Model             string `json:"model"`
			Tools             any    `json:"tools"`
			ToolChoice        any    `json:"tool_choice"`
			ParallelToolCalls any    `json:"parallel_tool_calls"`
			Thinking          any    `json:"thinking"`
			ThinkingBudget    any    `json:"thinking_budget"`
			ReasoningEffort   string `json:"reasoning_effort"`
			Messages          []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		upstreamModels = append(upstreamModels, body.Model)
		if body.Tools != nil || body.ToolChoice != nil || body.ParallelToolCalls != nil ||
			body.Thinking != nil || body.ThinkingBudget != nil || body.ReasoningEffort != "" {
			t.Fatalf("answer request should not include tool/thinking controls: %+v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		if upstreamCalls != 1 {
			t.Fatalf("unexpected upstream call %d", upstreamCalls)
		}
		for _, msg := range body.Messages {
			if msg.Role == "user" && strings.Contains(msg.Content, "https://openai.com") {
				sawSearchContext = true
			}
			if msg.Role == "tool" {
				t.Fatalf("answer request should not use tool role: %+v", body.Messages)
			}
		}
		_, _ = io.WriteString(w, `{
			"id":"chatcmpl-answer",
			"choices":[{
				"index":0,
				"message":{"role":"assistant","content":"OpenAI's official website is https://openai.com."},
				"finish_reason":"stop"
			}],
			"usage":{"prompt_tokens":120,"completion_tokens":12,"total_tokens":132}
		}`)
	}))
	defer upstream.Close()
	cfg := config.Default()
	cfg.Upstreams = []config.Upstream{{BaseURL: upstream.URL, APIKey: "test-key", Enabled: true}}
	cfg.ModelMappings = []config.ModelMapping{{Match: "*", Target: "glm-expensive"}}
	cfg.WebSearchModel = "glm-cheap"
	cfg.NativeAnthropic = false
	srv, _ := newTestServerWithCfg(t, cfg)

	body := []byte(`{
		"model":"claude-opus-4-8",
		"max_tokens":256,
		"stream":false,
		"tools":[{"type":"web_search_20250305","name":"web_search","max_uses":1}],
		"tool_choice":{"type":"tool","name":"web_search"},
		"messages":[{"role":"user","content":"Search for OpenAI's official website."}]
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	srv.Proxy()(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	if upstreamCalls != 1 || !sawSearchContext {
		t.Fatalf("upstreamCalls=%d sawSearchContext=%v", upstreamCalls, sawSearchContext)
	}
	if len(upstreamModels) != 1 || upstreamModels[0] != "glm-cheap" {
		t.Fatalf("upstream models = %+v, want glm-cheap", upstreamModels)
	}
	var out struct {
		Content []struct {
			Type      string `json:"type"`
			Name      string `json:"name"`
			ToolUseID string `json:"tool_use_id"`
			Content   any    `json:"content"`
			Text      string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens   int `json:"input_tokens"`
			OutputTokens  int `json:"output_tokens"`
			ServerToolUse struct {
				WebSearchRequests int `json:"web_search_requests"`
			} `json:"server_tool_use"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v\n%s", err, rr.Body.String())
	}
	if len(out.Content) < 3 {
		t.Fatalf("content = %+v", out.Content)
	}
	if out.Content[0].Type != "server_tool_use" || out.Content[0].Name != "web_search" {
		t.Fatalf("server_tool_use missing: %s", rr.Body.String())
	}
	if out.Content[1].Type != "web_search_tool_result" || out.Content[1].ToolUseID == "" {
		t.Fatalf("web_search_tool_result missing: %s", rr.Body.String())
	}
	if out.Content[2].Type != "text" || !strings.Contains(out.Content[2].Text, "openai.com") {
		t.Fatalf("final text missing: %s", rr.Body.String())
	}
	if out.Usage.ServerToolUse.WebSearchRequests != 1 {
		t.Fatalf("web_search_requests = %d", out.Usage.ServerToolUse.WebSearchRequests)
	}
	if strings.Contains(rr.Body.String(), `"type":"tool_use"`) {
		t.Fatalf("ordinary tool_use leaked into response: %s", rr.Body.String())
	}
}

func TestWebSearchShimStreamReturnsServerToolEvents(t *testing.T) {
	restoreWebSearchProvider(t)

	var upstreamCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		w.Header().Set("Content-Type", "application/json")
		if upstreamCalls != 1 {
			t.Fatalf("unexpected upstream call %d", upstreamCalls)
		}
		_, _ = io.WriteString(w, `{
			"id":"chatcmpl-answer",
			"choices":[{"index":0,"message":{"role":"assistant","content":"OpenAI is at https://openai.com."},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":40,"completion_tokens":8,"total_tokens":48}
		}`)
	}))
	defer upstream.Close()
	srv, _ := newTestServer(t, upstream.URL)

	body := []byte(`{
		"model":"claude-opus-4-8",
		"max_tokens":256,
		"stream":true,
		"tools":[{"type":"web_search_20250305","name":"web_search","max_uses":1}],
		"tool_choice":{"type":"tool","name":"web_search"},
		"messages":[{"role":"user","content":"Search for OpenAI's official website."}]
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	srv.Proxy()(rr, req)

	got := rr.Body.String()
	for _, want := range []string{
		`event: message_start`,
		`"server_tool_use":{"web_search_requests":1}`,
		`"type":"server_tool_use"`,
		`"type":"web_search_tool_result"`,
		`"type":"web_search_result"`,
		`"type":"text_delta"`,
		`"text":"OpenAI is at https://openai.com."`,
		`"stop_reason":"end_turn"`,
		`event: message_stop`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q\n%s", want, got)
		}
	}
	if strings.Contains(got, `"type":"tool_use"`) {
		t.Fatalf("ordinary tool_use leaked into stream:\n%s", got)
	}
	if upstreamCalls != 1 {
		t.Fatalf("upstreamCalls = %d, want 1", upstreamCalls)
	}
}

func TestWebSearchShimUsesMainModelWhenOverrideEmpty(t *testing.T) {
	restoreWebSearchProvider(t)

	var firstModel string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if firstModel == "" {
			firstModel = body.Model
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"id":"chatcmpl-answer",
			"choices":[{"index":0,"message":{"role":"assistant","content":"OpenAI is at https://openai.com."},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":30,"completion_tokens":5,"total_tokens":35}
		}`)
	}))
	defer upstream.Close()
	cfg := config.Default()
	cfg.Upstreams = []config.Upstream{{BaseURL: upstream.URL, APIKey: "test-key", Enabled: true}}
	cfg.ModelMappings = []config.ModelMapping{{Match: "*", Target: "glm-main"}}
	cfg.NativeAnthropic = false
	srv, _ := newTestServerWithCfg(t, cfg)

	body := []byte(`{
		"model":"claude-opus-4-8",
		"max_tokens":256,
		"stream":false,
		"tools":[{"type":"web_search_20250305","name":"web_search","max_uses":1}],
		"tool_choice":{"type":"tool","name":"web_search"},
		"messages":[{"role":"user","content":"Search for OpenAI's official website."}]
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	srv.Proxy()(rr, req)

	if firstModel != "glm-main" {
		t.Fatalf("first upstream model = %q, want glm-main", firstModel)
	}
}

func TestWebSearchNativeModePassesThroughMessages(t *testing.T) {
	var upstreamCalls int
	searchUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("path = %s, want /v1/messages", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer search-key" {
			t.Fatalf("Authorization = %q, want search key", got)
		}
		raw, _ := io.ReadAll(r.Body)
		body := string(raw)
		if !strings.Contains(body, `"model":"deepseek-v4-flash"`) ||
			!strings.Contains(body, `"type":"web_search_20250305"`) ||
			strings.Contains(body, `"function"`) {
			t.Fatalf("unexpected native body: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"id":"msg_native",
			"type":"message",
			"role":"assistant",
			"model":"glm-main",
			"content":[{"type":"text","text":"native search"}],
			"stop_reason":"end_turn",
			"usage":{"input_tokens":10,"output_tokens":2}
		}`)
	}))
	defer searchUpstream.Close()
	mainUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("main upstream should not receive native web_search request")
	}))
	defer mainUpstream.Close()

	cfg := config.Default()
	cfg.Upstreams = []config.Upstream{{BaseURL: mainUpstream.URL, APIKey: "main-key", Enabled: true}}
	cfg.ModelMappings = []config.ModelMapping{{Match: "*", Target: "glm-main"}}
	cfg.NativeAnthropic = false
	cfg.WebSearchMode = config.WebSearchModeNative
	cfg.WebSearchBaseURL = searchUpstream.URL
	cfg.WebSearchAPIKey = "search-key"
	cfg.WebSearchModel = "deepseek-v4-flash"
	srv, _ := newTestServerWithCfg(t, cfg)

	body := []byte(`{
		"model":"claude-opus-4-8",
		"max_tokens":256,
		"stream":false,
		"tools":[{"type":"web_search_20250305","name":"web_search","max_uses":1}],
		"tool_choice":{"type":"tool","name":"web_search"},
		"messages":[{"role":"user","content":"Search for OpenAI."}]
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	srv.Proxy()(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	if upstreamCalls != 1 {
		t.Fatalf("upstreamCalls = %d, want 1", upstreamCalls)
	}
	if !strings.Contains(rr.Body.String(), "native search") {
		t.Fatalf("native response was not relayed: %s", rr.Body.String())
	}
}

func restoreWebSearchProvider(t *testing.T) {
	t.Helper()
	old := webSearchProvider
	webSearchProvider = func(context.Context, string, []string, []string, int) ([]proxy.AnthropicWebSearchResult, error) {
		return []proxy.AnthropicWebSearchResult{{
			Type:             "web_search_result",
			Title:            "OpenAI",
			URL:              "https://openai.com",
			URI:              "https://openai.com",
			EncryptedContent: "Official OpenAI website.",
		}}, nil
	}
	t.Cleanup(func() { webSearchProvider = old })
}
