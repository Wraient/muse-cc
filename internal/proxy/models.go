package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// AnthropicModelInfo describes one entry in the model list.
type AnthropicModelInfo struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Type        string `json:"type"`
	Object      string `json:"object"`
	CreatedAt   int64  `json:"created_at,omitempty"`
	Created     int64  `json:"created,omitempty"`
	OwnedBy     string `json:"owned_by,omitempty"`
}

// AnthropicModelList is the body of GET /v1/models.
type AnthropicModelList struct {
	Data    []AnthropicModelInfo `json:"data"`
	Object  string               `json:"object"`
	FirstID string               `json:"first_id,omitempty"`
	LastID  string               `json:"last_id,omitempty"`
	HasMore bool                 `json:"has_more"`
}

// fetchUpstreamModels queries the upstream <base>/v1/models and returns the ids. It
// is used by the /v1/models handler so Claude Code sees the real Meta model ids.
//
// ctx MUST carry a timeout (the caller sets it): a stalled upstream fetch must
// fail fast instead of parking the handler goroutine forever. In Sep 2026 a
// flow-control-stalled HTTP/2 upstream connection wedged every API route this
// way — see the transport notes in server.New.
func fetchUpstreamModels(ctx context.Context, client *http.Client, base, apiKey string) ([]AnthropicModelInfo, error) {
	url := base + "/v1/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, nil // fall back to empty list
	}

	var raw struct {
		Data []struct {
			ID      string `json:"id"`
			Created int64  `json:"created"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := make([]AnthropicModelInfo, 0, len(raw.Data))
	for _, m := range raw.Data {
		out = append(out, AnthropicModelInfo{
			ID:        m.ID,
			Type:      "model",
			Object:    "model",
			CreatedAt: m.Created,
			Created:   m.Created,
			OwnedBy:   "meta",
		})
	}
	return out, nil
}

// ModelsHandler returns an http.HandlerFunc that dynamically lists the upstream
// (Meta) models. It caches the result for a minute so we don't hit Meta on every
// call. If the upstream is unreachable it returns a minimal fallback list.
func ModelsHandler(client *http.Client, upstreamBase, apiKey func() string) http.HandlerFunc {
	return ModelsHandlerWithUpstream(client, func() (string, string) {
		return upstreamBase(), apiKey()
	})
}

// ModelsHandlerWithUpstream is like ModelsHandler but obtains the upstream base
// URL and API key as one pair. Use this when the caller selects from a rotating
// upstream pool, so the base URL and key cannot drift apart between callbacks.
func ModelsHandlerWithUpstream(client *http.Client, upstream func() (base, apiKey string)) http.HandlerFunc {
	var (
		cache    []AnthropicModelInfo
		cachedAt time.Time
	)
	return func(w http.ResponseWriter, r *http.Request) {
		// Serve from cache if fresh (< 60s).
		if len(cache) > 0 && time.Since(cachedAt) < time.Minute {
			list := AnthropicModelList{Data: cache, Object: "list", HasMore: false}
			writeJSON(w, http.StatusOK, list)
			return
		}
		// Fetch live from upstream with a hard timeout so a stalled
		// upstream can never park this handler (and every later caller,
		// once the 60s cache expires) forever.
		base, key := upstream()
		if base != "" && client != nil {
			fetchCtx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
			models, err := fetchUpstreamModels(fetchCtx, client, base, key)
			cancel()
			if err == nil && len(models) > 0 {
				cache = models
				cachedAt = time.Now()
			}
		}
		if len(cache) == 0 {
			// Minimal fallback so the endpoint never errors.
			cache = []AnthropicModelInfo{{
				ID: "muse-spark-1.3-contributor", Type: "model", Object: "model", OwnedBy: "meta",
			}}
		}
		writeJSON(w, http.StatusOK, AnthropicModelList{
			Data: cache, Object: "list", HasMore: false,
		})
	}
}

// writeJSON is shared here because the server package also needs it; to avoid
// an import cycle we keep a local copy in helpers.go for the proxy package and
// the server package has its own.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
