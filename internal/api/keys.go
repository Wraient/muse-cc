package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/Kiowx/muse-cc/internal/store"
)

// invalidateCache, if set, is called after any key mutation so the auth
// middleware drops stale cache entries. Wired up by the server package to avoid
// an import cycle (api -> server).
var invalidateCache = func() {}

// keyBody is the request body for create/update key.
type keyBody struct {
	Name              string `json:"name"`
	Enabled           *bool  `json:"enabled"` // pointer so false is distinguishable from omitted
	TokenQuota        int64  `json:"token_quota"`
	RequestQuota      int64  `json:"request_quota"`
	DailyTokenLimit   int64  `json:"daily_token_limit"`
	DailyRequestLimit int64  `json:"daily_request_limit"`
	AllowedIPs        string `json:"allowed_ips"`
	ExpiresAt         int64  `json:"expires_at"`
}

// toOpts converts a keyBody into KeyOpts. enabled defaults to true on create.
func (b keyBody) toOpts() store.KeyOpts {
	enabled := true
	if b.Enabled != nil {
		enabled = *b.Enabled
	}
	return store.KeyOpts{
		Name:              strings.TrimSpace(b.Name),
		Enabled:           enabled,
		TokenQuota:        b.TokenQuota,
		RequestQuota:      b.RequestQuota,
		DailyTokenLimit:   b.DailyTokenLimit,
		DailyRequestLimit: b.DailyRequestLimit,
		AllowedIPs:        b.AllowedIPs,
		ExpiresAt:         b.ExpiresAt,
	}
}

// createResponse is returned once on key creation, carrying the plain key.
type createResponse struct {
	*store.APIKey
	PlainKey string `json:"plain_key"` // shown exactly once
}

// keysHandler dispatches GET/POST /api/keys and the /api/keys/{id} sub-routes.
func (a *API) keysHandler(w http.ResponseWriter, r *http.Request) {
	// /api/keys             -> list (GET) / create (POST)
	// /api/keys/{id}        -> get/update/delete
	// /api/keys/{id}/reset  -> reset usage
	// /api/keys/{id}/usage  -> usage series
	path := strings.TrimPrefix(r.URL.Path, "/api/keys")
	path = strings.Trim(path, "/")

	if path == "" {
		switch r.Method {
		case http.MethodGet:
			a.listKeys(w, r)
		case http.MethodPost:
			a.createKey(w, r)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}

	// Split into id and optional action.
	parts := strings.SplitN(path, "/", 2)
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad key id"})
		return
	}
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}

	switch {
	case action == "" && r.Method == http.MethodGet:
		a.getKey(w, r, id)
	case action == "" && r.Method == http.MethodPut:
		a.updateKey(w, r, id)
	case action == "" && r.Method == http.MethodDelete:
		a.deleteKey(w, r, id)
	case action == "reset" && r.Method == http.MethodPost:
		a.resetKeyUsage(w, r, id)
	case action == "usage" && r.Method == http.MethodGet:
		a.keyUsageSeries(w, r, id)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *API) listKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := a.store.ListKeys(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if keys == nil {
		keys = []store.APIKey{}
	}
	writeJSON(w, http.StatusOK, keys)
}

func (a *API) createKey(w http.ResponseWriter, r *http.Request) {
	var b keyBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	plain, err := a.store.CreateKey(r.Context(), b.toOpts())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Invalidate the auth cache so the new key is immediately usable.
	invalidateCache()
	// Fetch the created row to return full details alongside the plain key.
	created, _ := a.store.LookupKeyByHash(r.Context(), store.HashKey(plain))
	if created == nil {
		created = &store.APIKey{Name: b.Name}
	}
	writeJSON(w, http.StatusOK, createResponse{APIKey: created, PlainKey: plain})
}

func (a *API) getKey(w http.ResponseWriter, r *http.Request, id int64) {
	k, err := a.store.GetKey(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if k == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, k)
}

func (a *API) updateKey(w http.ResponseWriter, r *http.Request, id int64) {
	var b keyBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	// For updates, if the caller omits enabled (nil), keep current value.
	if b.Enabled == nil {
		if cur, _ := a.store.GetKey(r.Context(), id); cur != nil {
			t := cur.Enabled
			b.Enabled = &t
		}
	}
	if err := a.store.UpdateKey(r.Context(), id, b.toOpts()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	invalidateCache()
	k, _ := a.store.GetKey(r.Context(), id)
	writeJSON(w, http.StatusOK, k)
}

func (a *API) deleteKey(w http.ResponseWriter, r *http.Request, id int64) {
	if err := a.store.DeleteKey(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	invalidateCache()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *API) resetKeyUsage(w http.ResponseWriter, r *http.Request, id int64) {
	if err := a.store.ResetUsage(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	invalidateCache()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *API) keyUsageSeries(w http.ResponseWriter, r *http.Request, id int64) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 {
		days = 14
	}
	pts, err := a.store.KeyUsageSeries(r.Context(), id, days)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if pts == nil {
		pts = []store.KeyUsagePoint{}
	}
	writeJSON(w, http.StatusOK, pts)
}
