package server

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/Wraient/muse-cc/internal/store"
)

// ctxKey is unexported so callers can't fabricate the context value.
type ctxKey int

const apiKeyCtxKey ctxKey = 1

// APIKeyFromContext returns the authenticated API key placed by clientAuth, or
// nil if the request was allowed through without a key (RequireAPIKey=false).
func APIKeyFromContext(ctx context.Context) *store.APIKey {
	v, _ := ctx.Value(apiKeyCtxKey).(*store.APIKey)
	return v
}

// withAPIKey returns ctx with k attached.
func withAPIKey(ctx context.Context, k *store.APIKey) context.Context {
	return context.WithValue(ctx, apiKeyCtxKey, k)
}

// keyCacheEntry holds a looked-up key plus its expiry. We cache the existence /
// enabled state of a key briefly to avoid hitting SQLite on every request; the
// quota numbers are always read live at check time.
type keyCacheEntry struct {
	key     *store.APIKey // nil = unknown/invalid
	expires time.Time
}

var keyCache sync.Map // hash -> *keyCacheEntry
const keyCacheTTL = 5 * time.Second

// InvalidateKeyCache drops all cached entries (call after create/update/delete).
func InvalidateKeyCache() {
	keyCache.Range(func(k, _ any) bool {
		keyCache.Delete(k)
		return true
	})
}

// clientAuth is the middleware gating /v1/* behind a valid client API key.
//
// Behaviour:
//   - If RequireAPIKey is false, the request is allowed through without a key
//     (backward compatible with single-user local use).
//   - Otherwise the request must carry Authorization: Bearer <key>. The key is
//     hashed and looked up; disabled / expired / nonexistent keys are rejected.
//   - If the key has an IP allowlist, the client IP must match (CIDR supported).
//   - Quota checks: token_quota / request_quota (lifetime) and daily_*_limit.
//
// On rejection it writes an error matching the requested API protocol.
func (s *Server) clientAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Fast path: auth disabled.
		if !s.cfg.Snapshot().RequireAPIKey {
			next.ServeHTTP(w, r)
			return
		}

		// Extract the bearer token.
		plain := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if plain == "" {
			plain = strings.TrimSpace(r.Header.Get("x-api-key"))
		}
		if plain == "" {
			writeClientAPIError(w, r, http.StatusUnauthorized, "authentication_error",
				"missing API key. Send a key from the panel as Authorization: Bearer <key> or x-api-key.")
			return
		}

		hash := store.HashKey(plain)
		k := s.lookupKeyCached(r.Context(), hash)
		if k == nil {
			writeClientAPIError(w, r, http.StatusUnauthorized, "authentication_error", "invalid API key")
			return
		}
		if !k.Enabled {
			writeClientAPIError(w, r, http.StatusForbidden, "authentication_error", "this API key is disabled")
			return
		}
		if k.ExpiresAt > 0 && time.Now().Unix() > k.ExpiresAt {
			writeClientAPIError(w, r, http.StatusForbidden, "authentication_error", "this API key has expired")
			return
		}

		// IP allowlist.
		if k.AllowedIPs != "" && !ipAllowed(r, k.AllowedIPs) {
			writeClientAPIError(w, r, http.StatusForbidden, "authentication_error",
				"client IP is not allowed for this API key")
			return
		}

		// Quota checks. We re-read the live key to get fresh usage numbers.
		fresh, _ := s.store.LookupKeyByHash(r.Context(), hash)
		if fresh != nil {
			k = fresh
		}
		if reason := quotaBlocked(k); reason != "" {
			w.Header().Set("Retry-After", "60")
			writeClientAPIError(w, r, http.StatusTooManyRequests, "rate_limit_error", reason)
			return
		}

		next.ServeHTTP(w, r.WithContext(withAPIKey(r.Context(), k)))
	})
}

func writeClientAPIError(w http.ResponseWriter, r *http.Request, status int, errType, msg string) {
	if r != nil && (r.URL.Path == "/v1/chat/completions" || r.URL.Path == "/v1/responses") {
		writeOpenAIError(w, status, errType, msg)
		return
	}
	writeAnthropicError(w, status, errType, msg)
}

// lookupKeyCached returns the key for hash, using a short-lived cache for the
// existence/enabled check.
func (s *Server) lookupKeyCached(ctx context.Context, hash string) *store.APIKey {
	if v, ok := keyCache.Load(hash); ok {
		e := v.(*keyCacheEntry)
		if time.Now().Before(e.expires) {
			return e.key
		}
	}
	k, err := s.store.LookupKeyByHash(ctx, hash)
	if err != nil {
		return nil
	}
	keyCache.Store(hash, &keyCacheEntry{key: k, expires: time.Now().Add(keyCacheTTL)})
	return k
}

// quotaBlocked returns a non-empty reason string if the key has exhausted any
// quota, else "".
func quotaBlocked(k *store.APIKey) string {
	if k.TokenQuota > 0 && k.UsedTokens >= k.TokenQuota {
		return "token quota exceeded for this API key"
	}
	if k.RequestQuota > 0 && k.UsedRequests >= k.RequestQuota {
		return "request quota exceeded for this API key"
	}
	if k.DailyTokenLimit > 0 && k.DailyUsedTokens >= k.DailyTokenLimit {
		return "daily token limit reached for this API key (resets at UTC midnight)"
	}
	if k.DailyRequestLimit > 0 && k.DailyUsedRequests >= k.DailyRequestLimit {
		return "daily request limit reached for this API key (resets at UTC midnight)"
	}
	return ""
}

// ipAllowed reports whether the request's client IP is in the comma-separated
// allowlist of IPs/CIDRs.
func ipAllowed(r *http.Request, allowlist string) bool {
	clientIP := clientIPFromRequest(r)
	if clientIP == "" {
		return false
	}
	parsed, err := netip.ParseAddr(clientIP)
	if err != nil {
		return false
	}
	for _, entry := range strings.Split(allowlist, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if entry == "*" {
			return true
		}
		if strings.Contains(entry, "/") {
			// CIDR
			cidr, err := netip.ParsePrefix(entry)
			if err == nil && cidr.Contains(parsed) {
				return true
			}
			continue
		}
		// bare IP
		other, err := netip.ParseAddr(entry)
		if err == nil && other == parsed {
			return true
		}
	}
	return false
}

// clientIPFromRequest extracts the direct peer IP. Forwarding headers are only
// trusted when the direct peer is loopback, which supports a same-host reverse
// proxy without allowing remote clients to spoof their source address.
func clientIPFromRequest(r *http.Request) string {
	remoteIP := remoteIPFromAddr(r.RemoteAddr)
	parsedRemote := net.ParseIP(remoteIP)
	if parsedRemote != nil && parsedRemote.IsLoopback() {
		// Only trust forwarding headers from a same-host reverse proxy.
		xff := r.Header.Get("X-Forwarded-For")
		if xff == "" {
			return remoteIP
		}
		// first entry is the original client
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	return remoteIP
}

func remoteIPFromAddr(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}
