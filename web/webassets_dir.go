//go:build !web_embed

package webassets

import (
	"io/fs"
	"os"
	"sync/atomic"
)

var rootPath atomic.Pointer[string]

// SetRoot stores the directory the non-embed build should serve from.
// Pass an empty string to unset.
func SetRoot(path string) {
	if path == "" {
		rootPath.Store(nil)
		return
	}
	rootPath.Store(&path)
}

// WebFS returns an fs.FS rooted at the configured --web-root directory.
// Returns an error if no root has been configured — the caller is expected
// to surface that as a 404 with a hint.
func WebFS() (fs.FS, error) {
	p := rootPath.Load()
	if p == nil || *p == "" {
		return nil, ErrNoRoot
	}
	if _, err := os.Stat(*p); err != nil {
		return nil, err
	}
	return os.DirFS(*p), nil
}

// EmbedMode reports whether this build embeds the SPA. Always false here.
func EmbedMode() bool { return false }
