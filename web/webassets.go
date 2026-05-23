// Package webassets exposes the built Vue SPA as an fs.FS so the Go
// service can serve it without a separate proxy.
//
// Two build flavors expose the same WebFS() / SetRoot() / EmbedMode()
// symbols:
//
//   - default (no tag): WebFS reads from a directory on disk set via
//     SetRoot (driven by --web-root or WEB_ROOT). Returns ErrNoRoot if
//     neither is configured.
//   - -tags web_embed: //go:embed bakes web/dist into the binary;
//     SetRoot is a no-op and WebFS returns the embedded FS.
//
// Errors and other build-tag-independent declarations live here.
package webassets

import "errors"

// ErrNoRoot is returned by WebFS in the default (non-embed) build when
// no web root has been configured. Embed builds never return this.
var ErrNoRoot = errors.New("webassets: --web-root not configured (or rebuild with -tags web_embed)")
