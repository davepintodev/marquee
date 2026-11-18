// Package web embeds the built web UI.
package web

import (
	"embed"
	"io/fs"
)

// The all: prefix keeps dist/.gitkeep, so the package compiles before
// the first web build.
//
//go:embed all:dist
var dist embed.FS

// Dist returns the built UI rooted at its index.html.
func Dist() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
