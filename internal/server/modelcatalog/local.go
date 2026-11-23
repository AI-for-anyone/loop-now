package modelcatalog

import "loop-now/internal/server/agentruntime"

// SourceLocalCLI marks models provided by a CLI installed on the user's computer: they are run by the matching runtime,
// use the CLI's own login, and need no account.
const SourceLocalCLI = "local_cli"

// localSources are the model providers a runtime brings itself, by runtime id. A source is called every time the
// catalog is rebuilt, so it may return a list it asked the CLI for and cached. Today the only one is static.
var localSources = map[string]func() []Provider{
	agentruntime.ClaudeCode: func() []Provider { return []Provider{ClaudeCodeProvider()} },
}

// withLocal returns base plus the providers the runtimes bring themselves (in registry order). The ids of those
// providers are reserved: providers in base with the same id (custom ones from models.json) are not published.
func withLocal(base *Catalog) *Catalog {
	if base == nil {
		return nil
	}
	var local []Provider
	for _, rt := range agentruntime.All() {
		if source := localSources[rt.ID]; source != nil {
			local = append(local, source()...)
		}
	}
	reserved := map[string]bool{}
	for _, p := range local {
		reserved[p.ID] = true
	}
	providers := make([]Provider, 0, len(base.Providers)+len(local))
	for _, p := range base.Providers {
		if !reserved[p.ID] {
			providers = append(providers, p)
		}
	}
	c := New(base.PiVersion, base.GeneratedAt, append(providers, local...))
	c.Revision, c.providerConfig = base.Revision, base.providerConfig
	return c
}
