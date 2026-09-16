package assets

import (
	"net/http"
	"strings"
)

// SPAMux wraps an http.FileSystem serving a single-page app and adds SPA-style
// fallback: any non-file request returns index.html so client-side routing
// works. Asset requests that don't exist return a real 404.
func SPAMux(fsys http.FileSystem) http.Handler {
	fileServer := http.FileServer(fsys)
	indexBytes, _ := fsys.Open("/index.html")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// If a file exists, serve it directly.
		if f, err := fsys.Open(r.URL.Path); err == nil {
			_ = f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		// Otherwise serve index.html (SPA fallback) unless it looks like an
		// asset (has an extension) — those should 404 cleanly.
		if strings.Contains(r.URL.Path, ".") {
			http.NotFound(w, r)
			return
		}
		if indexBytes != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			// Re-open since FileServer doesn't expose a reusable reader.
			if f, err := fsys.Open("/index.html"); err == nil {
				defer f.Close()
				_, _ = copyAll(w, f)
				return
			}
		}
		http.NotFound(w, r)
	})
}

// copyAll is a tiny io.Copy without importing io at package level collisions.
func copyAll(w http.ResponseWriter, f http.File) (int64, error) {
	buf := make([]byte, 4096)
	var total int64
	for {
		n, err := f.Read(buf)
		if n > 0 {
			nn, werr := w.Write(buf[:n])
			total += int64(nn)
			if werr != nil {
				return total, werr
			}
		}
		if err != nil {
			if err.Error() == "EOF" {
				return total, nil
			}
			return total, err
		}
	}
}
