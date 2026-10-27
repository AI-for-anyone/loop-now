// Package buildinfo 提供服务端与 loopctl 共用的版本号，用于响应头 Loop-Now-Version 与客户端的版本比对。
//
// 发布构建时用 ldflags 注入：
//
//	go build -ldflags "-X loop-now/internal/buildinfo.version=<版本>" ./cmd/...
//
// 未注入时，按 Go 写入二进制的 VCS 信息生成 dev-<修订号前 12 位>，工作区有未提交改动时加 -dirty；
// 连 VCS 信息也没有时为 dev。
package buildinfo

import (
	"runtime/debug"
	"sync"
)

// version 由 ldflags 注入，不导出，只能通过 Version 读取。
var version string

var fallback = sync.OnceValue(func() string {
	bi, ok := debug.ReadBuildInfo()
	return resolve("", bi, ok)
})

// Version 返回本程序的版本。
func Version() string {
	if version != "" {
		return version
	}
	return fallback()
}

// resolve 按注入值与构建信息计算版本。
func resolve(injected string, bi *debug.BuildInfo, ok bool) string {
	if injected != "" {
		return injected
	}
	if !ok || bi == nil {
		return "dev"
	}
	var rev string
	var modified bool
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if rev == "" {
		return "dev"
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	v := "dev-" + rev
	if modified {
		v += "-dirty"
	}
	return v
}
