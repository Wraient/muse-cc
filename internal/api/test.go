package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// testUpstream sends a minimal chat completion to the configured Meta endpoint
// and reports whether it succeeded. Used by the panel's "Test connection"
// button. It never streams. With multiple upstreams configured, it tests each
// enabled upstream and returns a per-upstream result.
func (a *API) testUpstream(w http.ResponseWriter, r *http.Request) {
	// Collect the enabled upstream pool (or fall back to the legacy single pair).
	a.cfg.RLock()
	pool := make([]upstreamProbe, 0, len(a.cfg.Upstreams))
	for _, u := range a.cfg.Upstreams {
		if u.Enabled && u.APIKey != "" {
			pool = append(pool, upstreamProbe{base: u.BaseURL, key: u.APIKey, name: u.Name})
		}
	}
	if len(pool) == 0 && a.cfg.UpstreamBase != "" && a.cfg.MuseAPIKey != "" {
		pool = append(pool, upstreamProbe{base: a.cfg.UpstreamBase, key: a.cfg.MuseAPIKey})
	}
	defaultModel := a.cfg.DefaultModel
	a.cfg.RUnlock()

	model := r.URL.Query().Get("model")
	if model == "" {
		model = defaultModel
	}
	if model == "" {
		model = "muse-spark-1.3-contributor"
	}

	if len(pool) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":         false,
			"model":      model,
			"elapsed_ms": 0,
			"error":      "no upstream API key configured",
		})
		return
	}

	// Test each upstream; report a list of results. The top-level fields are
	// kept for the UI's single-result display; upstreams carries the detailed
	// per-account breakdown.
	start := time.Now()
	results := make([]map[string]any, 0, len(pool))
	for _, up := range pool {
		results = append(results, a.probeOne(up, model))
	}
	elapsed := time.Since(start).Milliseconds()
	// Overall ok = all succeeded (panel can show per-upstream detail).
	overallOK := true
	var preview string
	var firstErr string
	var promptTokens, completionTokens int
	for _, rr := range results {
		if ok, _ := rr["ok"].(bool); !ok {
			overallOK = false
			if firstErr == "" {
				firstErr, _ = rr["error"].(string)
			}
			continue
		}
		if preview == "" {
			preview, _ = rr["preview"].(string)
		}
		if n, ok := rr["prompt_tokens"].(int); ok {
			promptTokens += n
		}
		if n, ok := rr["completion_tokens"].(int); ok {
			completionTokens += n
		}
	}
	out := map[string]any{
		"ok":                overallOK,
		"model":             model,
		"elapsed_ms":        elapsed,
		"prompt_tokens":     promptTokens,
		"completion_tokens": completionTokens,
		"upstreams":         results,
	}
	if preview != "" {
		out["preview"] = preview
	}
	if firstErr != "" {
		out["error"] = firstErr
	}
	writeJSON(w, http.StatusOK, out)
}

// upstreamProbe is a single upstream to test.
type upstreamProbe struct {
	base, key, name string
}

// probeOne tests a single upstream and returns its result map.
func (a *API) probeOne(up upstreamProbe, model string) map[string]any {
	result := map[string]any{
		"ok":         false,
		"model":      model,
		"name":       up.name,
		"base_url":   up.base,
		"elapsed_ms": int64(0),
	}
	payload := map[string]any{
		"model":       model,
		"max_tokens":  64, // Meta spends ~13 reasoning tokens first; 64 leaves room for a preview
		"temperature": 0,
		"messages": []map[string]string{
			{"role": "user", "content": "ping"},
		},
	}
	b, _ := json.Marshal(payload)

	url := strings.TrimRight(up.base, "/") + "/v1/chat/completions"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		result["error"] = err.Error()
		return result
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+up.key)

	client := &http.Client{Timeout: 30 * time.Second}
	start := time.Now()
	resp, err := client.Do(req)
	elapsed := time.Since(start).Milliseconds()
	result["elapsed_ms"] = elapsed
	if err != nil {
		result["error"] = err.Error()
		return result
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := readN(resp.Body, 2048)
		result["error"] = fmt.Sprintf("upstream status %d: %s", resp.StatusCode, strings.TrimSpace(body))
		return result
	}

	// Decode enough to confirm a usable response.
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		result["error"] = "could not decode response: " + err.Error()
		return result
	}
	result["ok"] = true
	result["prompt_tokens"] = parsed.Usage.PromptTokens
	result["completion_tokens"] = parsed.Usage.CompletionTokens
	if len(parsed.Choices) > 0 {
		result["preview"] = truncate(parsed.Choices[0].Message.Content, 200)
	}
	return result
}

func readN(r interface{ Read([]byte) (int, error) }, n int) (string, error) {
	buf := make([]byte, n)
	m, err := r.Read(buf)
	if m > 0 {
		return string(buf[:m]), nil
	}
	return "", err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
