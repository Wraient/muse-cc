package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeMuseAuth(t *testing.T, apiKey string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "auth.json")
	doc := `{"schema_version":1,"providers":{"meta":{` +
		`"access_token":"dca:oauth-not-for-api","api_base_url":"https://api.meta.ai/v1",` +
		`"api_key":` + quoteJSON(apiKey) + `,"mechanism":"oauth"}}}`
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func quoteJSON(s string) string {
	// Minimal string quoter for controlled test inputs (no escapes needed).
	return `"` + s + `"`
}

func TestLoadMuseAPIKeyReadsAPIKeyNotAccessToken(t *testing.T) {
	path := writeMuseAuth(t, "LLM|test-key-123")
	if got := LoadMuseAPIKey(path); got != "LLM|test-key-123" {
		t.Fatalf("LoadMuseAPIKey = %q", got)
	}
	if got := LoadMuseAPIKey(filepath.Join(t.TempDir(), "missing.json")); got != "" {
		t.Fatalf("missing file = %q, want empty", got)
	}
	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(bad, []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := LoadMuseAPIKey(bad); got != "" {
		t.Fatalf("corrupt file = %q, want empty", got)
	}
}

func TestMaybeSyncMuseAuthAdoptsAndRefreshes(t *testing.T) {
	path := writeMuseAuth(t, "LLM|first")
	t.Setenv("MUSE_AUTH_PATH", path)

	c := Default()
	if _, _, ok := c.NextUpstream(); !ok {
		t.Fatal("expected sync to provide an upstream")
	}
	if _, key, _ := c.NextUpstream(); key != "LLM|first" {
		t.Fatalf("synced key = %q", key)
	}

	// Rewrite the file with a rotated key and a newer mtime.
	future := time.Now().Add(2 * time.Second)
	if err := os.WriteFile(path, []byte(`{"providers":{"meta":{"api_key":"LLM|rotated"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	if _, key, _ := c.NextUpstream(); key != "LLM|rotated" {
		t.Fatalf("after rotation key = %q", key)
	}
}

func TestMaybeSyncMuseAuthDisabled(t *testing.T) {
	path := writeMuseAuth(t, "LLM|ignored")
	t.Setenv("MUSE_AUTH_PATH", path)

	c := Default()
	c.SyncMuseAuth = false
	if _, _, ok := c.NextUpstream(); ok {
		t.Fatal("expected no upstream when sync is disabled and no key configured")
	}
}

func TestMaybeSyncMuseAuthLeavesCustomPool(t *testing.T) {
	path := writeMuseAuth(t, "LLM|subscription")
	t.Setenv("MUSE_AUTH_PATH", path)

	c := Default()
	c.Upstreams = []Upstream{
		{BaseURL: "https://custom.example", APIKey: "custom-1", Enabled: true},
		{BaseURL: "https://custom2.example", APIKey: "custom-2", Enabled: true},
	}
	base, key, ok := c.NextUpstream()
	if !ok {
		t.Fatal("expected pool upstream")
	}
	if base != "https://custom.example" && base != "https://custom2.example" {
		t.Fatalf("unexpected base %q", base)
	}
	if key != "custom-1" && key != "custom-2" {
		t.Fatalf("custom pool key overwritten: %q", key)
	}
}
