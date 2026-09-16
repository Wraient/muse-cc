// Command muse-cc is a local reverse proxy that lets Claude Code talk to
// the Meta Model API on a Muse Code subscription: Anthropic Messages,
// OpenAI Chat Completions and Codex Responses requests are served from
// https://api.meta.ai (native on all three surfaces), with a built-in
// web control panel.
//
// Quick start (no key export needed when the Muse CLI is logged in:
// the proxy auto-syncs providers.meta.api_key from ~/.config/muse/auth.json):
//
//	muse-cc
//	# then point Claude Code at it:
//	export ANTHROPIC_BASE_URL=http://localhost:8788
//	export ANTHROPIC_AUTH_TOKEN=anything
//	claude
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/Wraient/muse-cc/internal/assets"
	"github.com/Wraient/muse-cc/internal/config"
	"github.com/Wraient/muse-cc/internal/server"
	"github.com/Wraient/muse-cc/internal/store"
)

func main() {
	dataDir := flag.String("data", "data", "directory for config + SQLite database")
	flag.Parse()

	abs, err := filepath.Abs(*dataDir)
	if err != nil {
		log.Fatalf("resolve data dir: %v", err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		log.Fatalf("create data dir: %v", err)
	}

	cfg, err := config.Load(abs)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	st, err := store.Open(filepath.Join(abs, "muse-cc.db"))
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer st.Close()

	srv := server.New(cfg, st)

	// Resolve panel assets (embedded build or on-disk web/dist).
	panelFS, hasAssets := assets.FileSystem()
	var panelHandler http.Handler
	if hasAssets {
		panelHandler = assets.SPAMux(panelFS)
	}

	handler := srv.Handler(panelFS, panelHandler)

	addr := cfg.Snapshot().ListenAddr
	log.Printf("muse-cc listening on http://%s  (panel: %v, data: %s)", addr, hasAssets, abs)
	if cfg.Snapshot().MuseAPIKey == "" {
		log.Printf("WARNING: no upstream key — run `muse login`, set MUSE_API_KEY, or add one in the web panel at http://%s", addr)
	} else {
		log.Printf("upstream: %s   default model: %s", cfg.Snapshot().UpstreamBase, cfg.Snapshot().DefaultModel)
	}

	// Graceful shutdown on Ctrl+C / service stop.
	go func() {
		if err := srv.Start(handler); err != nil {
			log.Fatalf("server: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	log.Printf("shutting down...")
	_ = context.Background()
}
