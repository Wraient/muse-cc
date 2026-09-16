package store

import (
	"context"
	"testing"
	"time"
)

// newTestStore opens an in-temp-dir SQLite store for tests.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestCreateKeyExpiresAtPersisted confirms a created key's ExpiresAt is stored
// and read back faithfully. If the round-trip drops expires_at, the panel's
// "set validity period" would appear broken.
func TestCreateKeyExpiresAtPersisted(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	want := time.Now().Add(7 * 24 * time.Hour).Unix()
	plain, err := s.CreateKey(ctx, KeyOpts{
		Name: "exp", Enabled: true,
		TokenQuota: 1000, ExpiresAt: want,
	})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	k, err := s.LookupKeyByHash(ctx, HashKey(plain))
	if err != nil || k == nil {
		t.Fatalf("LookupKeyByHash: %v / %v", k, err)
	}
	if k.ExpiresAt != want {
		t.Errorf("expires_at not persisted on create: got %d want %d", k.ExpiresAt, want)
	}

	// Update should also persist a new expires_at.
	newWant := time.Now().Add(30 * 24 * time.Hour).Unix()
	if err := s.UpdateKey(ctx, k.ID, KeyOpts{
		Name: "exp", Enabled: true, TokenQuota: 1000, ExpiresAt: newWant,
	}); err != nil {
		t.Fatalf("UpdateKey: %v", err)
	}
	k2, _ := s.GetKey(ctx, k.ID)
	if k2.ExpiresAt != newWant {
		t.Errorf("expires_at not updated: got %d want %d", k2.ExpiresAt, newWant)
	}
}
