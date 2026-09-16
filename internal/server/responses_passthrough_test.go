package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wraient/muse-cc/internal/config"
	"github.com/Wraient/muse-cc/internal/store"
)

func TestSanitizePassthroughDedupesOutputsAndCoercesToolChoice(t *testing.T) {
	body := `{
		"model": "muse-spark-1.3-contributor-free",
		"tool_choice": {"type": "function", "name": "session_title"},
		"input": [
			{"type": "message", "role": "user", "content": "hi"},
			{"type": "function_call", "call_id": "call_1", "name": "a", "arguments": "{}"},
			{"type": "function_call", "call_id": "call_1", "name": "a-dup", "arguments": "{}"},
			{"type": "function_call_output", "call_id": "call_1", "output": "cancelled by user"},
			{"type": "function_call_output", "call_id": "call_1", "output": "real result"},
			{"type": "function_call_output", "call_id": "call_2", "output": "only"}
		]
	}`
	out, err := sanitizeResponsesPassthroughBody([]byte(body), "muse-spark-1.3-resolved")
	if err != nil {
		t.Fatalf("sanitize: %v", err)
	}
	var payload struct {
		Model      string `json:"model"`
		ToolChoice any    `json:"tool_choice"`
		Input      []struct {
			Type   string `json:"type"`
			CallID string `json:"call_id"`
			Name   string `json:"name"`
			Output any    `json:"output"`
			Role   string `json:"role"`
		} `json:"input"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("unmarshal sanitized: %v", err)
	}
	if payload.Model != "muse-spark-1.3-resolved" {
		t.Errorf("model = %q, want rewritten target", payload.Model)
	}
	if s, _ := payload.ToolChoice.(string); s != "auto" {
		t.Errorf("tool_choice = %v, want \"auto\"", payload.ToolChoice)
	}
	// Expect: message, FIRST function_call (name a), LAST output (real
	// result), call_2 output => 4 items.
	if len(payload.Input) != 4 {
		t.Fatalf("input items = %d, want 4: %s", len(payload.Input), out)
	}
	calls, outputs := 0, map[string]string{}
	for _, item := range payload.Input {
		switch item.Type {
		case "function_call":
			calls++
			if item.Name != "a" {
				t.Errorf("kept function_call name = %q, want first (a)", item.Name)
			}
		case "function_call_output":
			outputs[item.CallID] = strings.Trim(strings.TrimSpace(strings.Trim(string(mustJSON(item.Output)), `"`)), `"`)
		}
	}
	if calls != 1 {
		t.Errorf("function_call count = %d, want 1", calls)
	}
	if outputs["call_1"] != "real result" {
		t.Errorf("call_1 output = %q, want last (real result)", outputs["call_1"])
	}
	if outputs["call_2"] != "only" {
		t.Errorf("call_2 output = %q, want only", outputs["call_2"])
	}
}

func TestSanitizePassthroughKeepsAutoAndStringInput(t *testing.T) {
	body := `{"model": "x", "tool_choice": "auto", "input": "say ok"}`
	out, err := sanitizeResponsesPassthroughBody([]byte(body), "y")
	if err != nil {
		t.Fatalf("sanitize: %v", err)
	}
	var payload struct {
		Model      string `json:"model"`
		ToolChoice string `json:"tool_choice"`
		Input      string `json:"input"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.Model != "y" || payload.ToolChoice != "auto" || payload.Input != "say ok" {
		t.Errorf("unexpected sanitized body: %s", out)
	}
}

func TestPassthroughStreamStripsPing(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.created\ndata: {\"type\":\"response.created\"}\n\n")
		_, _ = io.WriteString(w, "event: ping\ndata: {\"type\":\"ping\"}\n\n")
		_, _ = io.WriteString(w, "event: response.output_text.delta\ndata: {\"type\":\"delta\",\"delta\":\"hi\"}\n\n")
		_, _ = io.WriteString(w, "event: ping\ndata: {\"type\":\"ping\"}\n\n")
	}))
	defer upstream.Close()

	st := mustTestStore(t)
	cfg := config.Default()
	cfg.Upstreams = []config.Upstream{{BaseURL: upstream.URL, APIKey: "k", Enabled: true}}
	srv := New(cfg, st)

	reqBody := `{"model":"muse-spark-1.3-contributor-free","input":"say ok","stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(reqBody))
	rec := httptest.NewRecorder()
	srv.ResponsesProxy().ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()
	got, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", res.StatusCode, got)
	}
	if strings.Contains(string(got), "ping") {
		t.Errorf("relayed stream contains ping frames: %q", got)
	}
	if !strings.Contains(string(got), "response.created") || !strings.Contains(string(got), "delta") {
		t.Errorf("relayed stream dropped real frames: %q", got)
	}
}

func TestPassthroughNonStreamRelaysBytes(t *testing.T) {
	const upstreamBody = `{"id":"resp_1","object":"response","status":"completed","output":[]}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			http.NotFound(w, r)
			return
		}
		var in struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.Model != "muse-spark-1.3-contributor-free" {
			t.Errorf("upstream model = %q", in.Model)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, upstreamBody)
	}))
	defer upstream.Close()

	st := mustTestStore(t)
	cfg := config.Default()
	cfg.Upstreams = []config.Upstream{{BaseURL: upstream.URL, APIKey: "k", Enabled: true}}
	srv := New(cfg, st)

	req := httptest.NewRequest(http.MethodPost, "/v1/responses",
		strings.NewReader(`{"model":"muse-spark-1.3-contributor-free","input":"say ok"}`))
	rec := httptest.NewRecorder()
	srv.ResponsesProxy().ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()
	got, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if strings.TrimSpace(string(got)) != upstreamBody {
		t.Errorf("body = %q, want upstream bytes as-is", got)
	}
}

func TestChatAndMessagesServeResponsesCapableModel(t *testing.T) {
	// Meta serves muse-spark on every surface, so nothing fail-fasts:
	// chat passes through, messages goes native by default and bridges
	// through /v1/responses only when native routing is off.
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/chat/completions":
			_, _ = io.WriteString(w, `{"id":"c","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
		case "/v1/messages":
			_, _ = io.WriteString(w, `{"id":"m","type":"message","role":"assistant","model":"muse-spark-1.3-contributor","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
		case "/v1/responses":
			_, _ = io.WriteString(w, `{"id":"r","object":"response","status":"completed","model":"muse-spark-1.3-contributor","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(upstream.Close)

	newSrv := func(native bool) *Server {
		cfg := config.Default()
		cfg.UpstreamBase = upstream.URL
		cfg.MuseAPIKey = "test-key"
		cfg.NativeAnthropic = native
		return New(cfg, mustTestStore(t))
	}

	chat := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"muse-spark-1.3-contributor","messages":[{"role":"user","content":"hi"}],"max_tokens":8}`))
	chatRec := httptest.NewRecorder()
	newSrv(true).OpenAIProxy().ServeHTTP(chatRec, chat)
	if chatRec.Code != http.StatusOK || gotPath != "/v1/chat/completions" {
		t.Errorf("chat = %d path %q, want 200 /v1/chat/completions", chatRec.Code, gotPath)
	}

	msg := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"muse-spark-1.3-contributor","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`))
	msgRec := httptest.NewRecorder()
	newSrv(true).Proxy().ServeHTTP(msgRec, msg)
	if msgRec.Code != http.StatusOK || gotPath != "/v1/messages" {
		t.Errorf("messages(native) = %d path %q, want 200 /v1/messages", msgRec.Code, gotPath)
	}

	bridge := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"muse-spark-1.3-contributor","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`))
	bridgeRec := httptest.NewRecorder()
	newSrv(false).Proxy().ServeHTTP(bridgeRec, bridge)
	if bridgeRec.Code != http.StatusOK || gotPath != "/v1/responses" {
		t.Errorf("messages(bridge) = %d path %q, want 200 /v1/responses", bridgeRec.Code, gotPath)
	}
}

func mustTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestDoUpstreamWithRetrySucceedsAfterOne503(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body, _ := io.ReadAll(r.Body)
		if len(body) == 0 {
			t.Errorf("attempt %d: empty upstream body", calls)
		}
		if calls == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("busy"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	template, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, upstream.URL, nil)
	resp, err := doUpstreamWithRetry(upstream.Client(), template, []byte(`{"model":"m"}`))
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	defer resp.Body.Close()
	if calls != 2 {
		t.Fatalf("upstream calls = %d, want 2", calls)
	}
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), `"ok":true`) {
		t.Fatalf("unexpected body: %s", raw)
	}
}

func TestDoUpstreamWithRetryGivesUpAfterTwo503s(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("down"))
	}))
	defer upstream.Close()

	template, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, upstream.URL, nil)
	_, err := doUpstreamWithRetry(upstream.Client(), template, []byte(`{}`))
	use, ok := err.(*upstreamStatusError)
	if !ok {
		t.Fatalf("err type = %T (%v), want *upstreamStatusError", err, err)
	}
	if use.status != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", use.status)
	}
}
