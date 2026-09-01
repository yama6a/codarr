// Package web serves the built single-page app from the binary.
package web

import (
	"embed"
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// dist is populated by `make web`; only .gitkeep is committed, so a clean checkout still compiles.
//
//go:embed all:dist
var dist embed.FS

// ErrNotBuilt is returned when the binary was built without running the frontend build first.
var ErrNotBuilt = errors.New("web: frontend not built, run `make web`")

// FS returns the built SPA rooted at dist/.
func FS() (fs.FS, error) {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, ErrNotBuilt
	}

	return sub, nil
}

// Available reports whether a real frontend build is embedded.
func Available() bool {
	sub, err := FS()
	if err != nil {
		return false
	}

	_, err = fs.Stat(sub, "index.html")

	return err == nil
}

// Handler serves the SPA with a fallback to index.html, so client-side routes resolve on a hard refresh.
// index.html is never cached, or a redeploy serves a stale shell pointing at deleted bundles.
func Handler() http.Handler {
	sub, err := FS()
	if err != nil || !Available() {
		return http.HandlerFunc(notBuilt)
	}

	files := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")

		if _, statErr := fs.Stat(sub, name); statErr != nil {
			serveIndex(w, r, sub)

			return
		}

		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}

		files.ServeHTTP(w, r)
	})
}

func serveIndex(w http.ResponseWriter, r *http.Request, sub fs.FS) {
	index, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		notBuilt(w, r)

		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(index)
}

func notBuilt(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(ErrNotBuilt.Error() + "\n"))
}
