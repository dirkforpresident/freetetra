package service

import (
	"errors"
	"io"
	"io/fs"
	"net/http"
	"strings"

	webassets "github.com/freetetra/server/web"
)

// reservedSPAExcludes are URL prefixes the SPA "/" handler must not catch.
// All of them are owned by other handlers; if an unknown path under one of
// them shows up, we 404 explicitly rather than fall through to index.html.
var reservedSPAExcludes = []string{
	"/api/",
	"/lang/",
	"/brew",
	"/telemetry",
	"/ws",
}

func isReservedSPAPath(p string) bool {
	for _, prefix := range reservedSPAExcludes {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

// newSPAHandler returns an http.Handler that serves files from fsys, with
// a try_files-style fallback to index.html for unknown paths so that the
// Vue router's history mode works. Paths under reservedSPAExcludes return
// 404 instead of falling back to index.html, so that an unknown /api/foo
// or /lang/zh doesn't silently serve HTML.
func newSPAHandler(fsys fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isReservedSPAPath(r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		f, err := fsys.Open(p)
		if err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		idx, err := fsys.Open("index.html")
		if err != nil {
			http.Error(w, "SPA not built. Run `npm run build` in web/ and pass --web-root, or rebuild with -tags web_embed.", http.StatusServiceUnavailable)
			return
		}
		defer idx.Close()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.Copy(w, idx)
	})
}

// registerSPAFallback mounts the Vue SPA at the root path "/", which means
// "/" itself plus any path not claimed by a more specific handler resolves
// to the SPA's index.html (so vue-router history mode works without page
// reloads). Specific API and infrastructure prefixes are explicitly
// excluded inside newSPAHandler.
func (s *Service) registerSPAFallback() {
	fsys, err := webassets.WebFS()
	if err != nil {
		s.server.RegisterHTTPHandler("/", func(w http.ResponseWriter, r *http.Request) {
			if isReservedSPAPath(r.URL.Path) {
				http.NotFound(w, r)
				return
			}
			msg := "SPA assets unavailable: " + err.Error()
			if errors.Is(err, webassets.ErrNoRoot) {
				msg = "SPA assets unavailable. Either pass --web-root /path/to/web/dist (or set WEB_ROOT), or rebuild with -tags web_embed."
			}
			http.Error(w, msg, http.StatusServiceUnavailable)
		})
		return
	}

	spa := newSPAHandler(fsys)
	s.server.RegisterHTTPHandler("/", func(w http.ResponseWriter, r *http.Request) {
		// Backwards-compat: when the old handleLandingPage owned "/", it
		// short-circuited WS upgrades to the telemetry handler. Preserve
		// that so clients still using ws://host/ keep working.
		if r.Header.Get("Upgrade") == "websocket" && s.telemetry != nil {
			s.telemetry.handleConnection(w, r)
			return
		}
		spa.ServeHTTP(w, r)
	})
}
