//go:build embedweb

package webui

import (
	"embed"
	"io/fs"
)

// dist is the console build output copied from web/dist before a release build (see scripts/release/build.sh).
//
//go:embed all:dist
var dist embed.FS

func embedded() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // can only mean the directory name above is misspelled
	}
	return sub
}
