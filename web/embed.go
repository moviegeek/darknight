// Package web exposes the compiled frontend as an embed.FS so the server can
// serve the SPA from the single Go binary.
//
// The embed directive is relative to this file (web/embed.go), so it picks up
// web/dist/*. The placeholder web/dist/index.html is checked in so `go build`
// works even before the frontend has been built; a real `npm run build`
// overwrites the directory.
package web

import "embed"

// Dist is the embedded production frontend.
//
//go:embed all:dist
var Dist embed.FS
