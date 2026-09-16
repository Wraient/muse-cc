package config

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// MuseAuthPath resolves the Muse CLI credential path using the same rules
// as the muse launcher script: $MUSE_AUTH_PATH wins, then
// $XDG_CONFIG_HOME/muse/auth.json, then ~/.config/muse/auth.json.
// "" means no candidate could be resolved (no HOME set).
func MuseAuthPath() string {
	if v := strings.TrimSpace(os.Getenv("MUSE_AUTH_PATH")); v != "" {
		return v
	}
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		return filepath.Join(xdg, "muse", "auth.json")
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".config", "muse", "auth.json")
}

// LoadMuseAPIKey reads the model-API key (providers.meta.api_key, the LLM|...
// value) from a Muse CLI auth.json file. It returns "" when the file is
// missing, unparseable, or carries no key. Only the api_key field is ever
// returned: the OAuth access credential in the same file is rejected by the
// model API (live-verified 401), so it must never be adopted here.
func LoadMuseAPIKey(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var doc struct {
		Providers map[string]struct {
			APIKey string `json:"api_key"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return ""
	}
	meta, ok := doc.Providers["meta"]
	if !ok {
		return ""
	}
	return strings.TrimSpace(meta.APIKey)
}

// maybeSyncMuseAuth refreshes the effective upstream key from the Muse CLI
// credential store when SyncMuseAuth is on and that file changed since the
// last check. The steady-state cost is one stat per upstream selection.
//
// Precedence: the subscription login always wins over the configured legacy
// key when the file yields a different non-empty key, so a fresh
// `muse login` (or server-side rotation) takes effect without a restart.
// Explicitly configured Upstreams pool entries are never rewritten, except
// the single auto-migrated entry that mirrors the legacy fields; set
// SyncMuseAuth=false (panel or MUSE_CC_SYNC_MUSE_AUTH=0) to pin the key.
//
// It never logs key material: at most the path and the key length.
func (c *Config) maybeSyncMuseAuth() {
	c.mu.RLock()
	enabled := c.SyncMuseAuth
	path := c.museAuthPath
	lastMod := c.museAuthMod
	c.mu.RUnlock()
	if !enabled {
		return
	}
	if path == "" {
		path = MuseAuthPath()
		if path == "" {
			return
		}
		c.mu.Lock()
		if c.museAuthPath == "" {
			c.museAuthPath = path
		} else {
			path = c.museAuthPath
		}
		c.mu.Unlock()
	}
	fi, err := os.Stat(path)
	if err != nil {
		return
	}
	if !lastMod.IsZero() && !fi.ModTime().After(lastMod) {
		return // unchanged since the last check
	}
	key := LoadMuseAPIKey(path)

	c.mu.Lock()
	defer c.mu.Unlock()
	c.museAuthMod = fi.ModTime()
	if key == "" || key == c.MuseAPIKey {
		return
	}
	c.MuseAPIKey = key
	switch {
	case len(c.Upstreams) == 0 && strings.TrimSpace(c.UpstreamBase) != "":
		// Started keyless: adopt the login as the managed entry so the
		// proxy serves traffic with zero restart.
		c.Upstreams = []Upstream{{
			BaseURL: strings.TrimRight(c.UpstreamBase, "/"),
			APIKey:  key,
			Enabled: true,
		}}
	case len(c.Upstreams) == 1 &&
		strings.TrimRight(c.Upstreams[0].BaseURL, "/") == strings.TrimRight(c.UpstreamBase, "/"):
		// Single auto-migrated entry mirroring the legacy fields.
		c.Upstreams[0].APIKey = key
	default:
		// Custom pool: leave it alone (it takes precedence by design).
	}
	log.Printf("muse-cc: synced Muse API key from %s (len %d)", path, len(key))
}
