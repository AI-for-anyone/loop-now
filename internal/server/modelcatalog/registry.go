package modelcatalog

import "sync/atomic"

// Registry 原子发布完整的目录快照；保存配置时不会暴露合并到一半的目录。
type Registry struct{ current atomic.Pointer[Catalog] }

func NewRegistry(initial *Catalog) *Registry {
	r := &Registry{}
	r.current.Store(initial)
	return r
}

func (r *Registry) Snapshot() *Catalog { return r.current.Load() }
func (r *Registry) Replace(c *Catalog) { r.current.Store(c) }
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
