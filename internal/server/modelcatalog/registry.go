package modelcatalog

import (
	"slices"
	"sync"
	"sync/atomic"
)

// Registry atomically publishes complete catalog snapshots; saving the config never exposes a half-merged catalog.
// Published catalog = Pi catalog (built-in plus models.json, given by Replace) + the providers of local CLIs (Claude
// Code's fixed, Codex's as the local Codex lists them, given by SetLocalProviders) + the providers registered by
// installed plugins (given by SetPluginProviders).
type Registry struct {
	current atomic.Pointer[Catalog]
	mu      sync.Mutex
	base    *Catalog
	local   map[string][]Provider
	plugins []PluginProvider
}

func NewRegistry(initial *Catalog) *Registry {
	r := &Registry{base: initial, local: map[string][]Provider{}}
	r.current.Store(withPlugins(withLocal(initial, nil), nil))
	return r
}

// SetLocalProviders replaces the providers a runtime brings itself with the ones its CLI listed (such as the models the
// local Codex lists for its login).
func (r *Registry) SetLocalProviders(runtime string, providers []Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.local[runtime] = slices.Clone(providers)
	r.current.Store(withPlugins(withLocal(r.base, r.local), r.plugins))
}

func (r *Registry) Snapshot() *Catalog { return r.current.Load() }

// Replace replaces the Pi catalog part; plugin providers are kept.
func (r *Registry) Replace(c *Catalog) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.base = c
	r.current.Store(withPlugins(withLocal(c, r.local), r.plugins))
}

// SetPluginProviders replaces the plugin provider part; only providers that registered an OAuth login and whose id
// differs from the Pi catalog go into the catalog.
func (r *Registry) SetPluginProviders(plugins []PluginProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.plugins = slices.Clone(plugins)
	r.current.Store(withPlugins(withLocal(r.base, r.local), r.plugins))
}

// Base returns the catalog without plugin providers: the Pi catalog plus the providers of local CLIs (used to check
// whether a plugin provider has the same id as an existing one).
func (r *Registry) Base() *Catalog {
	r.mu.Lock()
	defer r.mu.Unlock()
	return withLocal(r.base, r.local)
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
