package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wraient/muse-cc/internal/config"
	"github.com/Wraient/muse-cc/internal/store"
)

func TestClientAuthFailsClosedWithoutKeys(t *testing.T) {
	cfg := config.Default()
	cfg.RequireAPIKey = true
	st, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	called := false
	handler := New(cfg, st).clientAuth(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/messages", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body = %s", rec.Code, rec.Body.String())
	}
	if called {
		t.Fatal("protected handler was called without an API key")
	}
}

func TestClientAuthAcceptsValidKey(t *testing.T) {
	cfg := config.Default()
	cfg.RequireAPIKey = true
	st, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	plain, err := st.CreateKey(context.Background(), store.KeyOpts{Name: "test", Enabled: true})
	if err != nil {
		t.Fatalf("create key: %v", err)
	}

	var authenticated *store.APIKey
	handler := New(cfg, st).clientAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authenticated = APIKeyFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("Authorization", "Bearer "+plain)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}
	if authenticated == nil || authenticated.Name != "test" {
		t.Fatalf("authenticated key = %+v", authenticated)
	}
}

func TestClientAuthUsesOpenAIErrorForChatCompletions(t *testing.T) {
	cfg := config.Default()
	cfg.RequireAPIKey = true
	st, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	New(cfg, st).clientAuth(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("protected handler was called")
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error.Type != "authentication_error" || body.Error.Message == "" {
		t.Fatalf("unexpected OpenAI error: %s", rec.Body.String())
	}
	if body.Type != "" {
		t.Fatalf("received Anthropic error shape: %s", rec.Body.String())
	}
}

func TestClientAuthUsesOpenAIErrorForResponses(t *testing.T) {
	cfg := config.Default()
	cfg.RequireAPIKey = true
	st, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	New(cfg, st).clientAuth(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("protected handler was called")
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Type  string `json:"type"`
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error.Type != "authentication_error" || body.Type != "" {
		t.Fatalf("unexpected error shape: %s", rec.Body.String())
	}
}

func TestClientIPDoesNotTrustForwardedHeaderFromRemotePeer(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.10:4321"
	req.Header.Set("X-Forwarded-For", "127.0.0.1")
	if got := clientIPFromRequest(req); got != "203.0.113.10" {
		t.Fatalf("client IP = %q, want direct peer", got)
	}
}

func TestClientIPTrustsForwardedHeaderFromLoopbackProxy(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:4321"
	req.Header.Set("X-Forwarded-For", "198.51.100.8, 127.0.0.1")
	if got := clientIPFromRequest(req); got != "198.51.100.8" {
		t.Fatalf("client IP = %q, want forwarded client", got)
	}
}
