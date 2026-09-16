package server

import (
	"net/http"
	"strings"
	"testing"
)

func TestSetMuseHeadersStampsUAOnly(t *testing.T) {
	up, _ := http.NewRequest(http.MethodPost, "https://api.meta.ai/v1/messages", nil)
	down := http.Header{}
	// Even when the downstream client sends another family's identity
	// headers, nothing but our UA goes upstream to Meta.
	down.Set("x-opencode-session", "ses_client123")
	down.Set("x-opencode-request", "req_1")
	setMuseHeaders(up, down, "sticky-key")
	if got := up.Header.Get("User-Agent"); got != museUA() {
		t.Fatalf("User-Agent = %q, want %q", got, museUA())
	}
	for key := range up.Header {
		if strings.HasPrefix(strings.ToLower(key), "x-opencode-") {
			t.Fatalf("leaked upstream header %q", key)
		}
	}
}

func TestSetMuseHeadersNilSafe(t *testing.T) {
	setMuseHeaders(nil, http.Header{}, "")
}
