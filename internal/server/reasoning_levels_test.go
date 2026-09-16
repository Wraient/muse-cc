package server

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestEffortLevelsCacheResolution(t *testing.T) {
	c := LoadEffortLevelsCache("")
	if got := c.LevelsForModel("muse-spark-1.3-contributor-free"); !reflect.DeepEqual(got,
		[]string{"minimal", "low", "medium", "high", "xhigh"}) {
		t.Errorf("muse family: %v", got)
	}
	if got := c.LevelsForModel("muse-spark-1.2"); !reflect.DeepEqual(got,
		[]string{"minimal", "low", "medium", "high", "xhigh"}) {
		t.Errorf("muse prefix: %v", got)
	}
	if got := c.LevelsForModel("some-unknown-model"); !reflect.DeepEqual(got,
		[]string{"minimal", "low", "medium", "high", "xhigh"}) {
		t.Errorf("unknown default: %v", got)
	}
	// Returned slices are copies.
	got := c.LevelsForModel("muse-spark-1.3")
	got[0] = "MUTATED"
	if c.LevelsForModel("muse-spark-1.3")[0] != "minimal" {
		t.Error("LevelsForModel must return a copy")
	}
}

func TestEffortLevelsCacheLearnAndPersist(t *testing.T) {
	dir := t.TempDir()
	c := LoadEffortLevelsCache(dir)
	c.Learn("nemo-1", []string{"low", "medium", "high"})
	if got := c.LevelsForModel("nemo-1"); !reflect.DeepEqual(got, []string{"low", "medium", "high"}) {
		t.Fatalf("learned: %v", got)
	}
	// none/max in taught lists are filtered (listed-but-rejected upstream).
	c.Learn("nemo-2", []string{"none", "minimal", "low", "max"})
	if got := c.LevelsForModel("nemo-2"); !reflect.DeepEqual(got, []string{"minimal", "low"}) {
		t.Fatalf("filtered: %v", got)
	}
	// Garbage is ignored, never persisted.
	c.Learn("nemo-3", []string{"ok", "not a level!!"})
	if got := c.LevelsForModel("nemo-3"); !reflect.DeepEqual(got,
		[]string{"minimal", "low", "medium", "high", "xhigh"}) {
		t.Fatalf("garbage must not stick: %v", got)
	}
	// Reload from disk.
	c2 := LoadEffortLevelsCache(dir)
	if got := c2.LevelsForModel("nemo-1"); !reflect.DeepEqual(got, []string{"low", "medium", "high"}) {
		t.Errorf("persisted: %v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "reasoning-levels.json")); err != nil {
		t.Errorf("cache file: %v", err)
	}
}

func TestEffortLevelsCacheDemote(t *testing.T) {
	c := LoadEffortLevelsCache("")
	c.Learn("nemo-9", []string{"low", "medium", "high", "xhigh"})
	if got := c.DemoteTop("nemo-9"); !reflect.DeepEqual(got, []string{"low", "medium", "high"}) {
		t.Fatalf("demoted: %v", got)
	}
	if got := c.LevelsForModel("nemo-9"); !reflect.DeepEqual(got, []string{"low", "medium", "high"}) {
		t.Errorf("demote must stick: %v", got)
	}
	c.Learn("nemo-1", []string{"low"})
	if got := c.DemoteTop("nemo-1"); !reflect.DeepEqual(got, []string{"low"}) {
		t.Errorf("must never drop the last level: %v", got)
	}
	// Demoting a family-table model snapshots the demoted list as learned.
	if got := c.DemoteTop("muse-spark-1.3"); !reflect.DeepEqual(got,
		[]string{"minimal", "low", "medium", "high"}) {
		t.Errorf("family demote: %v", got)
	}
}

func TestClampPassthroughEffort(t *testing.T) {
	srv := New(nil, nil)
	in := []byte(`{"model":"m","reasoning":{"effort":"ultra"},"input":[]}`)
	out := srv.clampPassthroughEffort(in, "muse-spark-1.3-contributor-free", "m")
	if !strings.Contains(string(out), `"effort":"xhigh"`) {
		t.Errorf("ultra must clamp to xhigh: %s", out)
	}
	exact := []byte(`{"model":"m","reasoning":{"effort":"high"},"input":[]}`)
	if out := srv.clampPassthroughEffort(exact, "muse-spark-1.3-contributor-free", "m"); string(out) != string(exact) {
		t.Errorf("exact effort must pass through byte-identical: %s", out)
	}
	cased := []byte(`{"model":"m","reasoning":{"effort":"XHIGH"},"input":[]}`)
	if out := srv.clampPassthroughEffort(cased, "muse-spark-1.3-contributor-free", "m"); !strings.Contains(string(out), `"effort":"xhigh"`) {
		t.Errorf("case must normalize: %s", out)
	}
	absent := []byte(`{"model":"m","input":[]}`)
	if out := srv.clampPassthroughEffort(absent, "muse-spark-1.3-contributor-free", "m"); string(out) != string(absent) {
		t.Errorf("missing effort must pass through: %s", out)
	}
}
