// Package web embeds the built React SPA (web/dist) into the server binary.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS returns the built SPA rooted at dist/. When the SPA has not been built,
// it contains only the .keep placeholder and HasIndex reports false.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // dist is a compile-time constant path
	}
	return sub
}

// HasIndex reports whether the embedded SPA contains an index.html.
func HasIndex() bool {
	_, err := fs.Stat(FS(), "index.html")
	return err == nil
}
