package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wraient/muse-cc/internal/config"
	"github.com/Wraient/muse-cc/internal/store"
)

func newTestAPI(t *testing.T, cfg *config.Config) http.Handler {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	mux := http.NewServeMux()
	New(cfg, st).Mount(mux)
	return mux
}

func TestHealthEndpoints(t *testing.T) {
	mux := newTestAPI(t, config.Default())

	for _, path := range []string{"/api/health", "/healthz"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Fatalf("content-type = %q, want application/json", ct)
			}
			var out map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if out["ok"] != true {
				t.Fatalf("ok = %v, want true: %s", out["ok"], rec.Body.String())
			}
		})
	}
}

func TestEmptyListEndpointsReturnArrays(t *testing.T) {
	mux := newTestAPI(t, config.Default())

	for _, path := range []string{
		"/api/stats/hourly?hours=24",
		"/api/stats/models?hours=24",
		"/api/logs?limit=100",
	} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if body := strings.TrimSpace(rec.Body.String()); body != "[]" {
				t.Fatalf("body = %q, want []", body)
			}
		})
	}
}

func TestConfigPatchPreservesOmittedFields(t *testing.T) {
	cfg := config.Default()
	cfg.PanelToken = "old-password"
	cfg.RequireAPIKey = true
	cfg.NativeAnthropic = true
	cfg.LogRequests = true
	cfg.MaxBodyLogBytes = 1234
	cfg.PromptCacheEnabled = true
	cfg.PromptCacheKeyPrefix = "stable"
	cfg.WebSearchModel = "glm-cheap"
	cfg.WebSearchMode = config.WebSearchModeTranslate
	cfg.WebSearchBaseURL = "https://api.deepseek.com/anthropic"
	cfg.WebSearchAPIKey = "search-key"
	mux := newTestAPI(t, cfg)

	body := bytes.NewBufferString(`{"panel_token":"new-password"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/config", body)
	req.Header.Set("Authorization", "Bearer old-password")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	got := cfg.Snapshot()
	if got.PanelToken != "new-password" {
		t.Fatalf("panel token = %q, want new-password", got.PanelToken)
	}
	if !got.RequireAPIKey ||
		!got.NativeAnthropic ||
		!got.LogRequests ||
		got.MaxBodyLogBytes != 1234 ||
		!got.PromptCacheEnabled ||
		got.PromptCacheKeyPrefix != "stable" ||
		got.WebSearchModel != "glm-cheap" ||
		got.WebSearchMode != config.WebSearchModeTranslate ||
		got.WebSearchBaseURL != "https://api.deepseek.com/anthropic" ||
		got.WebSearchAPIKey != "search-key" {
		t.Fatalf("omitted fields changed: %+v", got)
	}
}

func TestConfigPatchUpdatesNativeAnthropic(t *testing.T) {
	cfg := config.Default()
	mux := newTestAPI(t, cfg)

	body := bytes.NewBufferString(`{"native_anthropic":false}`)
	req := httptest.NewRequest(http.MethodPut, "/api/config", body)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if cfg.Snapshot().NativeAnthropic {
		t.Fatal("NativeAnthropic was not updated")
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out["native_anthropic"] != false {
		t.Fatalf("native_anthropic missing from public config: %s", rec.Body.String())
	}
}

func TestConfigPatchUpdatesPromptCache(t *testing.T) {
	cfg := config.Default()
	mux := newTestAPI(t, cfg)

	body := bytes.NewBufferString(`{
		"prompt_cache_enabled":false,
		"prompt_cache_key_prefix":"local",
		"prompt_cache_anthropic_control":false,
		"prompt_cache_normalize":false
	}`)
	req := httptest.NewRequest(http.MethodPut, "/api/config", body)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	snap := cfg.Snapshot()
	if snap.PromptCacheEnabled ||
		snap.PromptCacheKeyPrefix != "local" ||
		snap.PromptCacheAnthropicControl ||
		snap.PromptCacheNormalize {
		t.Fatalf("prompt cache config was not updated: %+v", snap)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out["prompt_cache_enabled"] != false ||
		out["prompt_cache_key_prefix"] != "local" ||
		out["prompt_cache_anthropic_control"] != false ||
		out["prompt_cache_normalize"] != false {
		t.Fatalf("prompt cache missing from public config: %s", rec.Body.String())
	}
}

func TestConfigPatchUpdatesWebSearchSettings(t *testing.T) {
	cfg := config.Default()
	mux := newTestAPI(t, cfg)

	body := bytes.NewBufferString(`{
		"web_search_model":"anthropic/glm-cheap",
		"web_search_mode":"native",
		"web_search_base_url":"https://api.deepseek.com/anthropic/",
		"web_search_api_key":"search-key"
	}`)
	req := httptest.NewRequest(http.MethodPut, "/api/config", body)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if snap := cfg.Snapshot(); snap.WebSearchModel != "glm-cheap" ||
		snap.WebSearchMode != config.WebSearchModeNative ||
		snap.WebSearchBaseURL != "https://api.deepseek.com/anthropic" ||
		snap.WebSearchAPIKey != "search-key" {
		t.Fatalf("web search settings were not updated: %+v", snap)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out["web_search_model"] != "glm-cheap" ||
		out["web_search_mode"] != "native" ||
		out["web_search_base_url"] != "https://api.deepseek.com/anthropic" ||
		out["web_search_api_key_set"] != true ||
		out["web_search_api_key_masked"] == "" ||
		strings.Contains(rec.Body.String(), "search-key") {
		t.Fatalf("web search settings missing from public config: %s", rec.Body.String())
	}
}

func TestConfigPatchUpdatesThinkingBudgetMappings(t *testing.T) {
	cfg := config.Default()
	mux := newTestAPI(t, cfg)

	body := bytes.NewBufferString(`{
		"thinking_budget_mappings":[
			{"match":"deepseek-","field":"reasoning_effort"},
			{"match":"kimi-","field":"thinking_budget","low":512,"medium":2048,"high":8192,"max":32768}
		]
	}`)
	req := httptest.NewRequest(http.MethodPut, "/api/config", body)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	snap := cfg.Snapshot()
	if len(snap.ThinkingBudgetMappings) != 2 ||
		snap.ThinkingBudgetMappings[0].Field != "reasoning_effort" ||
		snap.ThinkingBudgetMappings[1].Max != 32768 {
		t.Fatalf("thinking budget config was not updated: %+v", snap.ThinkingBudgetMappings)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, ok := out["thinking_budget_mappings"]; !ok {
		t.Fatalf("thinking budget mappings missing from public config: %s", rec.Body.String())
	}
}

func TestPanelTokenChangeInvalidatesSessions(t *testing.T) {
	invalidateSessions()
	t.Cleanup(invalidateSessions)

	cfg := config.Default()
	cfg.PanelToken = "old-password"
	mux := newTestAPI(t, cfg)

	loginBody, _ := json.Marshal(map[string]string{"password": "old-password"})
	loginReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(loginBody))
	loginRec := httptest.NewRecorder()
	mux.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", loginRec.Code, loginRec.Body.String())
	}
	cookies := loginRec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("login did not set a session cookie")
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewBufferString(`{"panel_token":"new-password"}`))
	updateReq.AddCookie(cookies[0])
	updateRec := httptest.NewRecorder()
	mux.ServeHTTP(updateRec, updateReq)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("update status = %d, body = %s", updateRec.Code, updateRec.Body.String())
	}

	checkReq := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	checkReq.AddCookie(cookies[0])
	checkRec := httptest.NewRecorder()
	mux.ServeHTTP(checkRec, checkReq)
	if checkRec.Code != http.StatusUnauthorized {
		t.Fatalf("old session status = %d, want 401", checkRec.Code)
	}
}

func TestTestUpstreamIncludesTopLevelElapsed(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %q, want /v1/chat/completions", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices":[{"message":{"content":"pong"}}],
			"usage":{"prompt_tokens":1,"completion_tokens":2}
		}`))
	}))
	defer upstream.Close()

	cfg := config.Default()
	cfg.Upstreams = []config.Upstream{{BaseURL: upstream.URL, APIKey: "test-key", Enabled: true}}
	mux := newTestAPI(t, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/test?model=muse-spark-1.3-contributor", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var out struct {
		OK               bool `json:"ok"`
		ElapsedMS        *int `json:"elapsed_ms"`
		PromptTokens     int  `json:"prompt_tokens"`
		CompletionTokens int  `json:"completion_tokens"`
		Upstreams        []struct {
			OK        bool `json:"ok"`
			ElapsedMS int  `json:"elapsed_ms"`
		} `json:"upstreams"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !out.OK || out.ElapsedMS == nil || *out.ElapsedMS < 0 {
		t.Fatalf("top-level result missing elapsed/ok: %+v", out)
	}
	if out.PromptTokens != 1 || out.CompletionTokens != 2 {
		t.Fatalf("usage = %d/%d, want 1/2", out.PromptTokens, out.CompletionTokens)
	}
	if len(out.Upstreams) != 1 || !out.Upstreams[0].OK || out.Upstreams[0].ElapsedMS < 0 {
		t.Fatalf("upstreams result wrong: %+v", out.Upstreams)
	}
}
