// Package assets serves the built web panel (a static SPA). Embedded assets
// are used by default. An external directory can be selected explicitly with
// MUSE_CC_WEB_DIR for development.
package assets

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
)

// embedded holds the panel built into the binary. The dist/ directory contains
// a .gitkeep so the embed pattern always matches even before the first build.
//
//go:embed all:dist
var embedded embed.FS

// FileSystem returns an http.FileSystem for the panel SPA. It does not
// automatically use web/dist: a stale or partially-built directory beside the
// binary must not shadow the complete embedded panel.
func FileSystem() (http.FileSystem, bool) {
	if dir := os.Getenv("MUSE_CC_WEB_DIR"); dir != "" {
		if panelFS, ok := panelFileSystem(os.DirFS(dir)); ok {
			return panelFS, true
		}
	}

	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		return nil, false
	}
	return panelFileSystem(sub)
}

func panelFileSystem(root fs.FS) (http.FileSystem, bool) {
	info, err := fs.Stat(root, "index.html")
	if err != nil || info.IsDir() {
		return nil, false
	}
	return http.FS(root), true
}
