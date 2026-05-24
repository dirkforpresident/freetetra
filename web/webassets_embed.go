//go:build web_embed

package webassets

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var embeddedDist embed.FS

// SetRoot is a no-op in embed builds — the SPA is baked into the binary
// and --web-root has no effect.
func SetRoot(_ string) {}

// WebFS returns the embedded SPA file system, rooted at the dist/ directory
// (so the caller sees index.html at the root, not dist/index.html).
func WebFS() (fs.FS, error) {
	return fs.Sub(embeddedDist, "dist")
}

// EmbedMode reports whether this build embeds the SPA. Always true here.
func EmbedMode() bool { return true }
