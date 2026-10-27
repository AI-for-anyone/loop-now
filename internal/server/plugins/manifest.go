// Package plugins 是插件第二版的安装、配置、启用、授权与账号（plugins/SPEC.md §2、§5、§6），
// 给宿主监护提供要加载的插件，给 Pi 组装 Agent 半边的参数。清单的读取与校验在 pluginmanifest。
package plugins

import "loop-now/internal/pluginmanifest"

// 清单的类型沿用 pluginmanifest。
type (
	Manifest       = pluginmanifest.Manifest
	PublicManifest = pluginmanifest.PublicManifest
	AccountKind    = pluginmanifest.AccountKind
	Action         = pluginmanifest.Action
	Channel        = pluginmanifest.Channel
	Schedule       = pluginmanifest.Schedule
)

// ManifestFile 是插件目录里唯一的清单文件。
const ManifestFile = pluginmanifest.ManifestFile

// ReadManifest 读取并校验插件目录的清单；不合规则时返回 manifest 字段上的 422。
func ReadManifest(dir, platformVersion string) (Manifest, error) {
	m, err := pluginmanifest.ReadManifest(dir, platformVersion)
	return m, asInvalid(err, "manifest")
}
