package service

import (
	"errors"
	"io"
	"io/fs"
	"net/http"
	"strings"

	webassets "github.com/freetetra/server/web"
)

// newSPAHandler returns an http.Handler that serves files from fsys, with
// a try_files-style fallback to index.html for unknown paths so that the
// Vue router's history mode works.
func newSPAHandler(fsys fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

// registerSPAFallback hooks the SPA at /spa/ during Phase 5 so the embed
// vs non-embed wiring can be smoke-tested while the old Go HTML handlers
// still own /, /mitmachen, /live, /map, /ui. Phase 4 swaps this to "/"
// and removes the old handlers.
func (s *Service) registerSPAFallback() {
	fsys, err := webassets.WebFS()
	if err != nil {
		s.server.RegisterHTTPHandler("/spa/", func(w http.ResponseWriter, r *http.Request) {
			msg := "SPA assets unavailable: " + err.Error()
			if errors.Is(err, webassets.ErrNoRoot) {
				msg = "SPA assets unavailable. Either pass --web-root /path/to/web/dist (or set WEB_ROOT), or rebuild with -tags web_embed."
			}
			http.Error(w, msg, http.StatusServiceUnavailable)
		})
		s.server.RegisterHTTPHandler("/spa", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/spa/", http.StatusSeeOther)
		})
		return
	}

	handler := http.StripPrefix("/spa", newSPAHandler(fsys))
	s.server.RegisterHTTPHandler("/spa/", handler.ServeHTTP)
	s.server.RegisterHTTPHandler("/spa", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/spa/", http.StatusSeeOther)
	})
}
