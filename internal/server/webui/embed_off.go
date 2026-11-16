//go:build !embedweb

package webui

import "io/fs"

// Builds without the embedweb tag (development and tests) do not embed the console.
func embedded() fs.FS { return nil }
