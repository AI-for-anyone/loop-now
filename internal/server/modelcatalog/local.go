package modelcatalog

import "loop-now/internal/server/agentruntime"

// SourceLocalCLI marks models provided by a CLI installed on the user's computer: they are run by the matching runtime,
// use the CLI's own login, and need no account.
const SourceLocalCLI = "local_cli"

// localSources are the model providers a runtime brings itself, by runtime id, as they are before the CLI was asked:
// Claude Code's are fixed; Codex's, Grok's and Antigravity's are empty until Registry.SetLocalProviders gives the models
// the local CLI lists.
var localSources = map[string]func() []Provider{
	agentruntime.ClaudeCode:  func() []Provider { return []Provider{ClaudeCodeProvider()} },
	agentruntime.Codex:       func() []Provider { return []Provider{CodexProvider(nil)} },
	agentruntime.Grok:        func() []Provider { return []Provider{GrokProvider(nil)} },
	agentruntime.Antigravity: func() []Provider { return []Provider{AntigravityProvider(nil)} },
}

// LocalCommandDefaultModel is the one model of a local ACP command's provider: whatever model the program uses by
// default (its own model list is known only once a session is open).
const LocalCommandDefaultModel = "default"

// LocalCommandProvider is the catalog of a local ACP command's runtime: one provider of the runtime's id and name, with
// the program's default model.
func LocalCommandProvider(rt agentruntime.Runtime) Provider {
	return LocalCLIProvider(rt.ID, rt.Name, []LocalModel{{ID: LocalCommandDefaultModel, Name: "Default model"}})
}

// withLocal returns base plus the providers the runtimes bring themselves (in registry order): the ones a runtime's
// CLI listed (listed, by runtime id) or else its local source. The ids of those providers are reserved: providers in
// base with the same id (custom ones from models.json) are not published.
func withLocal(base *Catalog, listed map[string][]Provider) *Catalog {
	if base == nil {
		return nil
	}
	var local []Provider
	for _, rt := range agentruntime.All() {
		switch providers, ok := listed[rt.ID]; {
		case ok:
			local = append(local, providers...)
		case rt.Command != nil:
			local = append(local, LocalCommandProvider(rt))
		case localSources[rt.ID] != nil:
			local = append(local, localSources[rt.ID]()...)
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
