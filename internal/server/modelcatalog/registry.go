package modelcatalog

import (
	"slices"
	"sync"
	"sync/atomic"
)

// Registry 原子发布完整的目录快照；保存配置时不会暴露合并到一半的目录。
// 发布的目录 = Pi 目录（内置加 models.json，Replace 给出）+ 已安装插件注册的提供方（SetPluginProviders 给出）。
type Registry struct {
	current atomic.Pointer[Catalog]
	mu      sync.Mutex
	base    *Catalog
	plugins []PluginProvider
}

func NewRegistry(initial *Catalog) *Registry {
	r := &Registry{base: initial}
	r.current.Store(initial)
	return r
}

func (r *Registry) Snapshot() *Catalog { return r.current.Load() }

// Replace 换掉 Pi 目录部分，插件提供方保留。
func (r *Registry) Replace(c *Catalog) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.base = c
	r.current.Store(withPlugins(c, r.plugins))
}

// SetPluginProviders 换掉插件提供方部分；只有注册了 OAuth 登录、与 Pi 目录不同 id 的进目录。
func (r *Registry) SetPluginProviders(plugins []PluginProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.plugins = slices.Clone(plugins)
	r.current.Store(withPlugins(r.base, r.plugins))
}

// Base 返回不含插件提供方的 Pi 目录（判断插件提供方是否与内置或 models.json 同 id 时用）。
func (r *Registry) Base() *Catalog {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.base
}

func (r *Registry) Provider(id string) (*Provider, bool) {
	return r.Snapshot().Provider(id)
}
func (r *Registry) HasModel(provider, model string) bool {
	return r.Snapshot().HasModel(provider, model)
}
func (r *Registry) ResolveThinking(provider, model string, selected *string) (string, error) {
	return r.Snapshot().ResolveThinking(provider, model, selected)
}
func (r *Registry) SupportsImages(provider, model string) bool {
	return r.Snapshot().SupportsImages(provider, model)
}
func (r *Registry) ModelsJSONFor(provider string) ([]byte, string) {
	return r.Snapshot().ModelsJSONFor(provider)
}
