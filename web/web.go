// Package web embeds the built frontend (web/dist) into the LibriNode binary.
//
// dist/ is produced by `npm run build` and is not committed; only a .gitkeep
// placeholder is. The `all:` prefix makes go:embed accept that placeholder,
// so the backend always compiles — FS reports whether a real build is
// present, and the API falls back to a plain status page when it isn't.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var assets embed.FS

// FS returns the built SPA rooted at dist/, and whether a build is actually
// present (dist/index.html exists).
func FS() (fs.FS, bool) {
	sub, err := fs.Sub(assets, "dist")
	if err != nil {
		return nil, false
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil, false
	}
	return sub, true
}
