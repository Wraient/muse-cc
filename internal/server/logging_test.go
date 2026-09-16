package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Kiowx/muse-cc/internal/config"
)

func TestWithLoggingRecoversAnthropicPanic(t *testing.T) {
	handler := New(config.Default(), nil).withLogging(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{`"type":"error"`, `"type":"api_error"`, `"internal server error"`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("missing %q in body: %s", want, rec.Body.String())
		}
	}
}

func TestWithLoggingRecoversStreamingPanic(t *testing.T) {
	handler := New(config.Default(), nil).withLogging(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: message_start\ndata: {}\n\n"))
		panic("stream boom")
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "event: error") || !strings.Contains(body, `"internal server error"`) {
		t.Fatalf("stream panic was not surfaced as SSE error: %s", body)
	}
}
