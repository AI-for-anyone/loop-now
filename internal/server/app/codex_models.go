package app

import (
	"context"
	"time"

	"loop-now/internal/server/agentruntime"
	"loop-now/internal/server/modelcatalog"
)

// codexModelRefresher keeps the models the local Codex lists for its login in the catalog: once at startup, then every
// interval until ctx ends. When Codex is not installed or cannot list them, the codex-cli provider keeps the list it
// had (none at first).
func (a *App) codexModelRefresher(ctx context.Context, models *modelcatalog.Registry, every time.Duration) {
	refresh := func() {
		c, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		list, err := a.Executor.CodexModels(c)
		if err != nil {
			a.Log.Debug("could not read the models of the local Codex", "err", err)
			return
		}
		models.SetLocalProviders(agentruntime.Codex, []modelcatalog.Provider{modelcatalog.CodexProvider(list)})
	}
	refresh()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			refresh()
		}
	}
}
