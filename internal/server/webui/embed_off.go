//go:build !embedweb

package webui

import "io/fs"

// 不带 embedweb 标签构建（开发与测试）时不嵌入控制台。
func embedded() fs.FS { return nil }
