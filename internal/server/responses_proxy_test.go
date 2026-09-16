package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wraient/muse-cc/internal/config"
	"github.com/Wraient/muse-cc/internal/store"
)

func newResponsesTranslationTestServer(t *testing.T, upstream http.Handler) (*Server, *store.Store, *httptest.Server) {
	t.Helper()
	mock := httptest.NewServer(upstream)
	t.Cleanup(mock.Close)

	cfg := config.Default()
	cfg.UpstreamBase = mock.URL
	cfg.MuseAPIKey = "upstream-test-key"
	// Non-Meta target: keeps these tests on the Responses->Chat
	// translation path (a muse target would take native passthrough)
	// and exercises the GLM thinking-budget mapping.
	cfg.ModelMappings = []config.ModelMapping{
		{Match: "client-model", Target: "glm-5.1"},
		{Match: "*", Target: ""},
	}
	st, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	srv := New(cfg, st)
	httpSrv := httptest.NewServer(srv.Handler(nil, nil))
	t.Cleanup(httpSrv.Close)
	return srv, st, httpSrv
}

func TestResponsesNonStream(t *testing.T) {
	var upstreamBody struct {
		Model    string `json:"model"`
		Thinking *struct {
			Type          string `json:"type"`
			ClearThinking *bool  `json:"clear_thinking"`
			BudgetTokens  *int   `json:"budget_tokens"`
		} `json:"thinking"`
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
	}
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("upstream path = %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&upstreamBody); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"id":"chatcmpl-response",
			"choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":6,"completion_tokens":1,"total_tokens":7}
		}`)
	})
	_, st, httpSrv := newResponsesTranslationTestServer(t, upstream)

	body := `{
		"model":"client-model",
		"instructions":"Be concise.",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"Say OK"}]}],
		"stream":false
	}`
	resp, err := http.Post(httpSrv.URL+"/v1/responses", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, raw)
	}
	if upstreamBody.Model != "glm-5.1" {
		t.Fatalf("mapped model = %q", upstreamBody.Model)
	}
	if upstreamBody.Thinking == nil ||
		upstreamBody.Thinking.Type != "enabled" ||
		upstreamBody.Thinking.ClearThinking != nil ||
		upstreamBody.Thinking.BudgetTokens != nil {
		t.Fatalf("GLM thinking object not applied: %+v", upstreamBody.Thinking)
	}
	if len(upstreamBody.Messages) != 2 || upstreamBody.Messages[0].Role != "system" {
		t.Fatalf("upstream messages = %+v", upstreamBody.Messages)
	}

	var out struct {
		Object string `json:"object"`
		Status string `json:"status"`
		Model  string `json:"model"`
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.Object != "response" || out.Status != "completed" || out.Model != "client-model" {
		t.Fatalf("unexpected response: %s", raw)
	}
	if len(out.Output) != 1 || out.Output[0].Content[0].Text != "OK" {
		t.Fatalf("unexpected output: %s", raw)
	}
	if out.Usage.InputTokens != 6 || out.Usage.OutputTokens != 1 {
		t.Fatalf("unexpected usage: %+v", out.Usage)
	}

	waitForRequestLog(t, st, func(row store.RequestRow) bool {
		return row.Path == "/v1/responses" &&
			row.IncomingModel == "client-model" &&
			row.TargetModel == "glm-5.1" &&
			row.InputTokens == 6 &&
			row.OutputTokens == 1
	})
}

func TestResponsesStream(t *testing.T) {
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model         string `json:"model"`
			Stream        bool   `json:"stream"`
			StreamOptions struct {
				IncludeUsage bool `json:"include_usage"`
			} `json:"stream_options"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		if body.Model != "glm-5.1" || !body.Stream || !body.StreamOptions.IncludeUsage {
			t.Errorf("unexpected upstream request: %+v", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		chunks := []string{
			`{"id":"chatcmpl-stream","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
			`{"id":"chatcmpl-stream","choices":[{"index":0,"delta":{"content":"OK"},"finish_reason":null}]}`,
			`{"id":"chatcmpl-stream","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`{"id":"chatcmpl-stream","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6}}`,
		}
		for _, chunk := range chunks {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk)
			if flusher != nil {
				flusher.Flush()
			}
		}
		// Some OpenAI-compatible providers close the HTTP stream cleanly
		// without a final data: [DONE] sentinel.
	})
	_, st, httpSrv := newResponsesTranslationTestServer(t, upstream)

	body := `{
		"model":"client-model",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"Say OK"}]}],
		"stream":true
	}`
	resp, err := http.Post(httpSrv.URL+"/v1/responses", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, raw)
	}
	out := string(raw)
	for _, want := range []string{
		"event: response.created",
		"event: response.output_text.delta",
		`"delta":"OK"`,
		"event: response.output_text.done",
		"event: response.completed",
		`"input_tokens":5`,
		`"output_tokens":1`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stream missing %q:\n%s", want, out)
		}
	}

	waitForRequestLog(t, st, func(row store.RequestRow) bool {
		return row.Path == "/v1/responses" &&
			row.Stream &&
			row.InputTokens == 5 &&
			row.OutputTokens == 1 &&
			row.StopReason == "stop"
	})
}

func TestResponsesStreamReportsPrematureEOF(t *testing.T) {
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprint(w, `data: {"id":"chatcmpl-stream","choices":[{"index":0,"delta":{"content":"partial"},"finish_reason":null}]}`+"\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	})
	_, _, httpSrv := newResponsesTranslationTestServer(t, upstream)

	body := `{
		"model":"client-model",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"Say OK"}]}],
		"stream":true
	}`
	resp, err := http.Post(httpSrv.URL+"/v1/responses", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := string(raw)
	if !strings.Contains(out, "event: response.failed") {
		t.Fatalf("premature EOF was not reported as failed:\n%s", out)
	}
	if strings.Contains(out, "event: response.completed") {
		t.Fatalf("premature EOF was incorrectly completed:\n%s", out)
	}
}

func TestResponsesStreamIgnoresConfiguredBodyTimeout(t *testing.T) {
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprint(w, `data: {"id":"chatcmpl-stream","choices":[{"index":0,"delta":{"content":"slow"},"finish_reason":null}]}`+"\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		time.Sleep(1200 * time.Millisecond)
		_, _ = fmt.Fprint(w, `data: {"id":"chatcmpl-stream","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"id":"chatcmpl-stream","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6}}`+"\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	})
	srv, _, httpSrv := newResponsesTranslationTestServer(t, upstream)
	srv.cfg.RequestTimeoutSeconds = 1

	body := `{
		"model":"client-model",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"Say OK"}]}],
		"stream":true
	}`
	resp, err := http.Post(httpSrv.URL+"/v1/responses", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := string(raw)
	if !strings.Contains(out, "event: response.completed") {
		t.Fatalf("slow stream was not completed:\n%s", out)
	}
	if strings.Contains(out, "event: response.failed") {
		t.Fatalf("slow stream was incorrectly failed:\n%s", out)
	}
}

func TestResponsesNativeAnthropicNonStream(t *testing.T) {
	var gotPath, gotAuth, gotAPIKey, gotAccept, gotVersion string
	var gotBody map[string]json.RawMessage
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotAPIKey = r.Header.Get("x-api-key")
		gotAccept = r.Header.Get("Accept")
		gotVersion = r.Header.Get("anthropic-version")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"id":"msg_resp_native",
			"type":"message",
			"role":"assistant",
			"model":"claude-sonnet-4-5",
			"content":[{"type":"text","text":"native OK"}],
			"stop_reason":"end_turn",
			"usage":{"input_tokens":11,"cache_read_input_tokens":4,"output_tokens":2}
		}`)
	})
	srv, st, httpSrv := newResponsesTranslationTestServer(t, upstream)
	srv.cfg.ModelMappings = []config.ModelMapping{
		{Match: "client-model", Target: "claude-sonnet-4-5"},
		{Match: "*", Target: ""},
	}

	body := `{
		"model":"client-model",
		"instructions":"Be concise.",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"Say OK"}]}],
		"max_output_tokens":128,
		"stream":false
	}`
	resp, err := http.Post(httpSrv.URL+"/v1/responses", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, raw)
	}
	if gotPath != "/v1/messages" {
		t.Fatalf("upstream path = %q, want /v1/messages", gotPath)
	}
	if gotAuth != "Bearer upstream-test-key" || gotAPIKey != "upstream-test-key" ||
		gotAccept != "application/json" || gotVersion != "2023-06-01" {
		t.Fatalf("upstream headers auth=%q x-api-key=%q accept=%q version=%q", gotAuth, gotAPIKey, gotAccept, gotVersion)
	}
	var model string
	if err := json.Unmarshal(gotBody["model"], &model); err != nil || model != "claude-sonnet-4-5" {
		t.Fatalf("mapped model = %q, err=%v", model, err)
	}
	var maxTokens int
	if err := json.Unmarshal(gotBody["max_tokens"], &maxTokens); err != nil || maxTokens != 128 {
		t.Fatalf("max_tokens = %d, err=%v", maxTokens, err)
	}
	if !bytes.Contains(gotBody["system"], []byte("cache_control")) {
		t.Fatalf("cache_control missing from native Anthropic request: %s", gotBody["system"])
	}

	var out struct {
		Object string `json:"object"`
		Status string `json:"status"`
		Model  string `json:"model"`
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Usage struct {
			InputTokens        int `json:"input_tokens"`
			OutputTokens       int `json:"output_tokens"`
			InputTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.Object != "response" || out.Status != "completed" || out.Model != "client-model" {
		t.Fatalf("unexpected response: %s", raw)
	}
	if len(out.Output) != 1 || out.Output[0].Content[0].Text != "native OK" {
		t.Fatalf("unexpected output: %s", raw)
	}
	if out.Usage.InputTokens != 11 || out.Usage.OutputTokens != 2 ||
		out.Usage.InputTokensDetails.CachedTokens != 4 {
		t.Fatalf("unexpected usage: %+v", out.Usage)
	}

	waitForRequestLog(t, st, func(row store.RequestRow) bool {
		return row.Path == "/v1/responses" &&
			row.IncomingModel == "client-model" &&
			row.TargetModel == "claude-sonnet-4-5" &&
			row.InputTokens == 11 &&
			row.OutputTokens == 2 &&
			row.StopReason == "end_turn"
	})
}

func TestResponsesNativeAnthropicStream(t *testing.T) {
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		if body.Model != "claude-sonnet-4-5" || !body.Stream {
			t.Fatalf("unexpected upstream request: %+v", body)
		}
		if r.Header.Get("Accept") != "text/event-stream" {
			t.Fatalf("Accept = %q", r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		events := []string{
			`event: message_start
data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[],"stop_reason":null,"usage":{"input_tokens":8,"output_tokens":0}}}

`,
			`event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

`,
			`event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"native stream"}}

`,
			`event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":3}}

`,
			`event: message_stop
data: {"type":"message_stop"}

`,
		}
		for _, event := range events {
			_, _ = io.WriteString(w, event)
			if flusher != nil {
				flusher.Flush()
			}
		}
	})
	srv, st, httpSrv := newResponsesTranslationTestServer(t, upstream)
	srv.cfg.ModelMappings = []config.ModelMapping{
		{Match: "client-model", Target: "claude-sonnet-4-5"},
		{Match: "*", Target: ""},
	}

	body := `{
		"model":"client-model",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"Say OK"}]}],
		"stream":true
	}`
	resp, err := http.Post(httpSrv.URL+"/v1/responses", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, raw)
	}
	out := string(raw)
	for _, want := range []string{
		"event: response.created",
		"event: response.output_text.delta",
		`"delta":"native stream"`,
		"event: response.completed",
		`"input_tokens":8`,
		`"output_tokens":3`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stream missing %q:\n%s", want, out)
		}
	}

	waitForRequestLog(t, st, func(row store.RequestRow) bool {
		return row.Path == "/v1/responses" &&
			row.Stream &&
			row.TargetModel == "claude-sonnet-4-5" &&
			row.InputTokens == 8 &&
			row.OutputTokens == 3 &&
			row.StopReason == "stop"
	})
}

func TestResponsesRejectsInvalidJSON(t *testing.T) {
	_, _, httpSrv := newResponsesTranslationTestServer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("upstream should not be called")
	}))
	resp, err := http.Post(httpSrv.URL+"/v1/responses", "application/json", strings.NewReader(`{"model":`))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d: %s", resp.StatusCode, raw)
	}
	if !strings.Contains(string(raw), `"type":"invalid_request_error"`) {
		t.Fatalf("unexpected error: %s", raw)
	}
}
