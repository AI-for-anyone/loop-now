//go:build embedweb

package webui

import (
	"embed"
	"io/fs"
)

// dist 是发布构建前从 web/dist 拷来的控制台构建产物（见 scripts/release/build.sh）。
//
//go:embed all:dist
var dist embed.FS

func embedded() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // 只会是上面的目录名写错
	}
	return sub
}
