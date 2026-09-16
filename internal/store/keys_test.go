package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestLookupKeyClearsStaleDailyUsage(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	plain, err := st.CreateKey(context.Background(), KeyOpts{Enabled: true})
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	yesterday := time.Now().UTC().Add(-24 * time.Hour).Unix()
	if _, err := st.db.Exec(`
UPDATE api_keys SET daily_used_tokens=100, daily_used_requests=10,
daily_reset_ts=? WHERE key_hash=?`, yesterday, HashKey(plain)); err != nil {
		t.Fatalf("seed stale usage: %v", err)
	}

	key, err := st.LookupKeyByHash(context.Background(), HashKey(plain))
	if err != nil {
		t.Fatalf("lookup key: %v", err)
	}
	if key == nil {
		t.Fatal("lookup returned nil key")
	}
	if key.DailyUsedTokens != 0 || key.DailyUsedRequests != 0 {
		t.Fatalf("stale daily usage was not cleared: %+v", key)
	}
}

func TestOpenMigratesLegacyRequestsTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	_, err = db.Exec(`
CREATE TABLE requests (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    ts INTEGER NOT NULL,
    method TEXT NOT NULL,
    path TEXT NOT NULL,
    incoming_model TEXT NOT NULL,
    target_model TEXT NOT NULL,
    stream INTEGER NOT NULL DEFAULT 0,
    status INTEGER NOT NULL DEFAULT 0,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    input_tokens INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    stop_reason TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    req_body TEXT NOT NULL DEFAULT '',
    resp_body TEXT NOT NULL DEFAULT ''
);`)
	if err != nil {
		_ = db.Close()
		t.Fatalf("create legacy schema: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	st, err := Open(path)
	if err != nil {
		t.Fatalf("migrate legacy db: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	if err := st.InsertRequest(context.Background(), &RequestRow{
		Ts:     time.Now(),
		Method: "POST",
		Path:   "/v1/messages",
	}); err != nil {
		t.Fatalf("insert after migration: %v", err)
	}
	rows, err := st.ListRequests(context.Background(), 1)
	if err != nil {
		t.Fatalf("list after migration: %v", err)
	}
	if len(rows) != 1 || rows[0].APIKeyID != 0 {
		t.Fatalf("unexpected migrated row: %+v", rows)
	}
}
