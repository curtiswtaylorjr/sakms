// Package web serves SAK's frontend — a single dependency-free static
// page (no build step, no framework) embedded directly into the binary, so
// there's nothing extra to deploy alongside it.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed static
var embedded embed.FS

// Handler serves the embedded static assets at the root of whatever path
// it's mounted on (typically "/"). It is an SPA "try file, else
// index.html" wrapper: a request for a path that exists on disk (app.js,
// style.css, images, ...) is served as that file; a request for anything
// else falls back to index.html, so the page's client-side router can
// handle deep-links/refreshes on routes like "/discover" or "/settings"
// that have no matching file. Requests under "/api/" or "/healthz" are
// never rewritten — those are claimed by other muxes before this handler
// is reached in cmd/sakms/main.go, but the check is kept here too as
// defense-in-depth against this handler ever being mounted more broadly.
func Handler() http.Handler {
	sub, err := fs.Sub(embedded, "static")
	if err != nil {
		panic(err) // static is embedded at build time — this can only fail if the embed itself is broken
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/healthz" {
			http.NotFound(w, r)
			return
		}

		if existsAsFile(sub, r.URL.Path) {
			http.ServeFileFS(w, r, sub, r.URL.Path)
			return
		}
		http.ServeFileFS(w, r, sub, "index.html")
	})
}

// existsAsFile reports whether urlPath resolves to a regular (non-directory)
// file within sub. A directory match (or no match at all) is not "exists" —
// both fall back to index.html in Handler.
func existsAsFile(sub fs.FS, urlPath string) bool {
	clean := strings.TrimPrefix(path.Clean("/"+urlPath), "/")
	if clean == "" || clean == "." {
		return false
	}
	info, err := fs.Stat(sub, clean)
	if err != nil {
		return false
	}
	return !info.IsDir()
}
