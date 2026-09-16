package server

// Per-model reasoning-effort scales, taught by upstream.
//
// Models accept different effort levels (verified live 2026-09-15:
// muse-spark-1.3 takes minimal/low/medium/high/xhigh and rejects none, max,
// and unknown names with HTTP 400; other families differ — some lack
// minimal, some add none/max). LevelsForModel resolves the ordered scale
// (low to high) for a target model from, in order:
//
//  1. upstream-taught entries: invalid-effort 400s name the accepted set,
//     which Learn persists — one wasted round-trip teaches the proxy a
//     model permanently;
//  2. the built-in family table below (live-verified);
//  3. proxy.DefaultReasoningLevels.
//
// Taught entries persist to <datadir>/reasoning-levels.json. Everything here
// is best-effort: any failure degrades to the built-in tables, never to a
// failed request.

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Kiowx/muse-cc/internal/proxy"
)

// builtinEffortFamilies maps lowercase model-id prefixes to live-verified
// effort scales (ordered low to high). muse-spark probed 2026-09-15:
// minimal/low/medium/high/xhigh all 200; none/max/unknown/cased variants
// all 400 (max is listed upstream but rejected — excluded on purpose).
var builtinEffortFamilies = []struct {
	prefix string
	levels []string
}{
	{"muse-spark", []string{"minimal", "low", "medium", "high", "xhigh"}},
}

// EffortLevelsCache resolves and learns per-model reasoning scales.
type EffortLevelsCache struct {
	mu     sync.RWMutex
	path   string // "" = memory only (tests)
	levels map[string][]string
}

// LoadEffortLevelsCache opens the taught-levels store, loading any
// previously persisted entries. Missing/corrupt files start empty.
func LoadEffortLevelsCache(dataDir string) *EffortLevelsCache {
	c := &EffortLevelsCache{levels: map[string][]string{}}
	if dataDir == "" {
		return c
	}
	c.path = filepath.Join(dataDir, "reasoning-levels.json")
	raw, err := os.ReadFile(c.path)
	if err != nil {
		return c
	}
	var disk map[string][]string
	if err := json.Unmarshal(raw, &disk); err != nil {
		log.Printf("muse-cc: ignoring corrupt %s: %v", c.path, err)
		return c
	}
	for model, lv := range disk {
		if saneEffortLevels(lv) && model != "" {
			c.levels[model] = append([]string(nil), lv...)
		}
	}
	return c
}

// LevelsForModel returns the ordered effort scale (low to high) for a
// target model. The result is a copy and always non-empty.
func (c *EffortLevelsCache) LevelsForModel(model string) []string {
	if c != nil {
		c.mu.RLock()
		lv, ok := c.levels[model]
		c.mu.RUnlock()
		if ok && len(lv) > 0 {
			return append([]string(nil), lv...)
		}
	}
	lower := strings.ToLower(model)
	for _, fam := range builtinEffortFamilies {
		if strings.HasPrefix(lower, fam.prefix) {
			return append([]string(nil), fam.levels...)
		}
	}
	return append([]string(nil), proxy.DefaultReasoningLevels...)
}

// Learn records an upstream-taught level list for a model (replacing any
// previous entry, including built-in family rows for that exact model) and
// persists it. Taught lists arrive WITHOUT none/max: upstream names them in
// "expected one of" enumerations even for models that reject them (verified
// on muse-spark), so adopting them would manufacture 400s; models where
// they genuinely work need a family-table row instead.
func (c *EffortLevelsCache) Learn(model string, levels []string) {
	filtered := make([]string, 0, len(levels))
	for _, lv := range levels {
		if strings.EqualFold(lv, "none") || strings.EqualFold(lv, "max") {
			continue
		}
		filtered = append(filtered, lv)
	}
	if model == "" || !saneEffortLevels(filtered) {
		return
	}
	if c == nil {
		return
	}
	c.mu.Lock()
	c.levels[model] = filtered
	c.mu.Unlock()
	log.Printf("muse-cc: learned reasoning levels for %s: %v", model, filtered)
	c.persist()
}

// DemoteTop drops the highest level for a model (it just failed despite
// being listed) and persists the demoted list. It never drops the last
// level, and returns the effective new list.
func (c *EffortLevelsCache) DemoteTop(model string) []string {
	current := []string(nil)
	if c != nil {
		current = c.LevelsForModel(model)
	} else {
		current = append([]string(nil), proxy.DefaultReasoningLevels...)
	}
	if len(current) <= 1 {
		return current
	}
	demoted := current[:len(current)-1]
	if c != nil && model != "" {
		c.mu.Lock()
		c.levels[model] = append([]string(nil), demoted...)
		c.mu.Unlock()
		log.Printf("muse-cc: demoted reasoning levels for %s to %v (top level rejected upstream)", model, demoted)
		c.persist()
	}
	return append([]string(nil), demoted...)
}

func (c *EffortLevelsCache) persist() {
	if c == nil || c.path == "" {
		return
	}
	c.mu.RLock()
	raw, err := json.MarshalIndent(c.levels, "", "  ")
	c.mu.RUnlock()
	if err != nil {
		return
	}
	if err := os.WriteFile(c.path, raw, 0o644); err != nil {
		log.Printf("muse-cc: cannot persist %s: %v", c.path, err)
	}
}

// levelsForModel resolves the effort scale for a target model, tolerating a
// missing cache (tests constructing Server without New).
func (s *Server) levelsForModel(model string) []string {
	if s == nil || s.effortLevels == nil {
		return append([]string(nil), proxy.DefaultReasoningLevels...)
	}
	return s.effortLevels.LevelsForModel(model)
}

// clampPassthroughEffort normalizes a client-supplied reasoning.effort into
// the target model's known scale (exact names canonicalized, the rest
// clamped to the model's maximum). Requests without an effort pass through
// untouched. Never errors: on any surprise the original body is returned.
func (s *Server) clampPassthroughEffort(upBody []byte, targetModel, incomingModel string) []byte {
	effort, ok := proxy.ExtractRequestEffort(upBody)
	if !ok {
		return upBody
	}
	clamped := proxy.ClampEffort(effort, s.levelsForModel(targetModel))
	if clamped == effort {
		return upBody
	}
	fixed, err := proxy.SetRequestEffort(upBody, clamped)
	if err != nil {
		return upBody
	}
	log.Printf("muse-cc: clamped reasoning.effort %q -> %q model=%s target=%s",
		effort, clamped, incomingModel, targetModel)
	return fixed
}

func saneEffortLevels(lv []string) bool {
	if len(lv) == 0 || len(lv) > 16 {
		return false
	}
	for _, s := range lv {
		if len(s) == 0 || len(s) > 24 {
			return false
		}
		for _, r := range s {
			if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' && r != '-' {
				return false
			}
		}
	}
	return true
}
