// Package webassets exposes the frontend bundle embedded in the Go binary.
package webassets

import (
	"embed"
	"io/fs"
)

// files contains the Vite production bundle. The tracked .keep file makes the
// package testable before a frontend build; make build replaces it with assets.
//
//go:embed all:dist
var files embed.FS

// FS returns the frontend bundle rooted at its dist directory.
func FS() fs.FS {
	bundle, err := fs.Sub(files, "dist")
	if err != nil {
		panic("embedded frontend bundle is unavailable: " + err.Error())
	}
	return bundle
}
