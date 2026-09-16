package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain isolates unit tests from the developer's real Muse CLI login:
// subscription sync reads $MUSE_AUTH_PATH, so point it at a path that never
// exists. Tests for the sync itself override the variable per-test.
func TestMain(m *testing.M) {
	_ = os.Setenv("MUSE_AUTH_PATH", filepath.Join(os.TempDir(), "muse-cc-test-no-auth.json"))
	_ = os.Remove(os.Getenv("MUSE_AUTH_PATH"))
	os.Exit(m.Run())
}
