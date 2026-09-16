package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// APIKey is one client API key record. The plain key is never stored; only the
// SHA256 hash and a short prefix (for display in the panel).
type APIKey struct {
	ID                int64  `json:"id"`
	KeyPrefix         string `json:"key_prefix"` // first 10 chars of the plain key
	Name              string `json:"name"`
	Enabled           bool   `json:"enabled"`
	TokenQuota        int64  `json:"token_quota"`         // 0 = unlimited
	RequestQuota      int64  `json:"request_quota"`       // 0 = unlimited
	DailyTokenLimit   int64  `json:"daily_token_limit"`   // 0 = unlimited
	DailyRequestLimit int64  `json:"daily_request_limit"` // 0 = unlimited
	AllowedIPs        string `json:"allowed_ips"`         // comma-sep CIDRs, "" = any
	UsedTokens        int64  `json:"used_tokens"`
	UsedRequests      int64  `json:"used_requests"`
	DailyUsedTokens   int64  `json:"daily_used_tokens"`
	DailyUsedRequests int64  `json:"daily_used_requests"`
	DailyResetTs      int64  `json:"daily_reset_ts"`
	CreatedAt         int64  `json:"created_at"`
	ExpiresAt         int64  `json:"expires_at"` // 0 = never
}

// KeyOpts carries the mutable, user-settable fields of a key. Used by
// Create/Update so callers don't juggle positional args.
type KeyOpts struct {
	Name              string
	Enabled           bool
	TokenQuota        int64
	RequestQuota      int64
	DailyTokenLimit   int64
	DailyRequestLimit int64
	AllowedIPs        string
	ExpiresAt         int64 // 0 = never
}

// HashKey returns the hex SHA256 of a plain key, for storage / lookup.
func HashKey(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// generatePlainKey returns a new random "sk-" + 32 hex chars key.
func generatePlainKey() (string, error) {
	b := make([]byte, 16) // 128 bits → 32 hex chars
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "sk-" + hex.EncodeToString(b), nil
}

// CreateKey generates a new key, stores its hash, and returns the plain key
// exactly once. The caller is responsible for delivering it to the user.
func (s *Store) CreateKey(ctx context.Context, opts KeyOpts) (plain string, err error) {
	plain, err = generatePlainKey()
	if err != nil {
		return "", err
	}
	hash := HashKey(plain)
	prefix := plain[:10] // "sk-" + 7 chars
	now := time.Now().Unix()
	_, err = s.db.ExecContext(ctx, `
INSERT INTO api_keys
(key_hash, key_prefix, name, enabled,
 token_quota, request_quota, daily_token_limit, daily_request_limit,
 allowed_ips, created_at, expires_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		hash, prefix, opts.Name, boolToInt(opts.Enabled),
		opts.TokenQuota, opts.RequestQuota, opts.DailyTokenLimit, opts.DailyRequestLimit,
		normalizeIPs(opts.AllowedIPs), now, opts.ExpiresAt,
	)
	if err != nil {
		return "", fmt.Errorf("insert api key: %w", err)
	}
	return plain, nil
}

// LookupKeyByHash returns the key with the given hash, or (nil, nil) if absent.
func (s *Store) LookupKeyByHash(ctx context.Context, hash string) (*APIKey, error) {
	row := s.db.QueryRowContext(ctx, selectKeyCols+` FROM api_keys WHERE key_hash = ?`, hash)
	k, err := scanKey(row)
	if err != nil {
		if errors.Is(err, errNoRow) {
			return nil, nil
		}
		return nil, err
	}
	return k, nil
}

// GetKey returns a key by id (nil, nil if absent).
func (s *Store) GetKey(ctx context.Context, id int64) (*APIKey, error) {
	row := s.db.QueryRowContext(ctx, selectKeyCols+` FROM api_keys WHERE id = ?`, id)
	k, err := scanKey(row)
	if err != nil {
		if errors.Is(err, errNoRow) {
			return nil, nil
		}
		return nil, err
	}
	return k, nil
}

// ListKeys returns all keys ordered by creation (newest first).
func (s *Store) ListKeys(ctx context.Context) ([]APIKey, error) {
	rows, err := s.db.QueryContext(ctx, selectKeyCols+` FROM api_keys ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIKey
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *k)
	}
	return out, rows.Err()
}

const selectKeyCols = `SELECT id, key_prefix, name, enabled,
       token_quota, request_quota, daily_token_limit, daily_request_limit,
       allowed_ips, used_tokens, used_requests,
       daily_used_tokens, daily_used_requests, daily_reset_ts,
       created_at, expires_at`

// scanner abstracts *sql.Row and *sql.Rows for scanKey.
type scanner interface {
	Scan(dest ...any) error
}

// errNoRow is a sentinel for the "no rows" case from QueryRow.Scan.
var errNoRow = errors.New("no row")

func scanKey(sc scanner) (*APIKey, error) {
	k := &APIKey{}
	var enabled int
	if err := sc.Scan(
		&k.ID, &k.KeyPrefix, &k.Name, &enabled,
		&k.TokenQuota, &k.RequestQuota, &k.DailyTokenLimit, &k.DailyRequestLimit,
		&k.AllowedIPs, &k.UsedTokens, &k.UsedRequests,
		&k.DailyUsedTokens, &k.DailyUsedRequests, &k.DailyResetTs,
		&k.CreatedAt, &k.ExpiresAt,
	); err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return nil, errNoRow
		}
		return nil, err
	}
	k.Enabled = enabled == 1
	dayStart := time.Now().UTC().Truncate(24 * time.Hour).Unix()
	if k.DailyResetTs < dayStart {
		k.DailyUsedTokens = 0
		k.DailyUsedRequests = 0
	}
	return k, nil
}

// UpdateKey replaces all mutable fields with the supplied values.
func (s *Store) UpdateKey(ctx context.Context, id int64, opts KeyOpts) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE api_keys SET
    name                = ?,
    enabled             = ?,
    token_quota         = ?,
    request_quota       = ?,
    daily_token_limit   = ?,
    daily_request_limit = ?,
    allowed_ips         = ?,
    expires_at          = ?
WHERE id = ?`,
		opts.Name, boolToInt(opts.Enabled),
		opts.TokenQuota, opts.RequestQuota, opts.DailyTokenLimit, opts.DailyRequestLimit,
		normalizeIPs(opts.AllowedIPs), opts.ExpiresAt, id,
	)
	return err
}

// DeleteKey removes a key permanently.
func (s *Store) DeleteKey(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM api_keys WHERE id = ?`, id)
	return err
}

// ResetUsage zeroes the usage counters for a key (both lifetime and daily).
func (s *Store) ResetUsage(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE api_keys SET used_tokens=0, used_requests=0,
       daily_used_tokens=0, daily_used_requests=0, daily_reset_ts=? WHERE id = ?`,
		time.Now().Unix(), id)
	return err
}

// RecordUsage atomically increments a key's usage counters. If the day has
// rolled over since daily_reset_ts, the daily counters are reset first within
// the same transaction. Negative tokens are clamped to 0.
func (s *Store) RecordUsage(ctx context.Context, id int64, tokens, requests int) error {
	if tokens < 0 {
		tokens = 0
	}
	if requests < 0 {
		requests = 0
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Roll over daily counters if we crossed a UTC day boundary since the last reset.
	// dayStart = midnight UTC of today; if daily_reset_ts < dayStart, reset daily.
	dayStart := time.Now().UTC().Truncate(24 * time.Hour).Unix()
	_, err = tx.ExecContext(ctx, `
UPDATE api_keys SET
    used_tokens         = used_tokens + ?,
    used_requests       = used_requests + ?,
    daily_used_tokens   = CASE WHEN daily_reset_ts < ? THEN ? ELSE daily_used_tokens + ? END,
    daily_used_requests = CASE WHEN daily_reset_ts < ? THEN ? ELSE daily_used_requests + ? END,
    daily_reset_ts      = CASE WHEN daily_reset_ts < ? THEN ? ELSE daily_reset_ts END
WHERE id = ?`,
		tokens, requests,
		dayStart, tokens, tokens,
		dayStart, requests, requests,
		dayStart, dayStart,
		id,
	)
	if err != nil {
		return fmt.Errorf("record usage: %w", err)
	}
	return tx.Commit()
}

// KeyUsagePoint is one day's aggregated usage for a key.
type KeyUsagePoint struct {
	Day          int64 `json:"day"`
	Requests     int64 `json:"requests"`
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

// KeyUsageSeries returns per-day usage for a key over the last `days` days.
func (s *Store) KeyUsageSeries(ctx context.Context, id int64, days int) ([]KeyUsagePoint, error) {
	if days <= 0 {
		days = 7
	}
	from := time.Now().Add(-time.Duration(days) * 24 * time.Hour).UnixMilli()
	rows, err := s.db.QueryContext(ctx, `
SELECT (ts/86400000)*86400 AS day, COUNT(*),
       COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0)
FROM requests WHERE api_key_id = ? AND ts >= ?
GROUP BY day ORDER BY day ASC`, id, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KeyUsagePoint
	for rows.Next() {
		var p KeyUsagePoint
		if err := rows.Scan(&p.Day, &p.Requests, &p.InputTokens, &p.OutputTokens); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// CountKeys returns the number of keys (used to decide whether to allow
// unauthenticated access when no keys exist yet).
func (s *Store) CountKeys(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_keys`).Scan(&n)
	return n, err
}

// normalizeIPs trims whitespace around comma-separated CIDR entries.
func normalizeIPs(s string) string {
	parts := strings.Split(s, ",")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, ",")
}
