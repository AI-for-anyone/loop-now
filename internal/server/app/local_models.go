package app

import (
	"context"
	"slices"
	"sync"
	"time"

	"loop-now/internal/server/agentruntime"
	"loop-now/internal/server/modelcatalog"
)

// Defaults for reading a local CLI's model list again when the lists are read (see localModelReader).
const (
	localModelsMaxAge   = time.Minute
	localModelsStaleAge = 10 * time.Second
)

// runtimeListsProvider reports whether provider is one whose models the runtime's local CLI lists.
func runtimeListsProvider(runtime, provider string) bool {
	rt, ok := agentruntime.ByID(runtime)
	return ok && slices.Contains(rt.Providers, provider)
}

// localModelRefresher reads a local CLI's model list once at startup, then every interval until ctx ends.
func (a *App) localModelRefresher(ctx context.Context, r *localModelReader, every time.Duration) {
	r.refresh(ctx)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.refresh(ctx)
		}
	}
}

// readCodexModels reads the models the local Codex lists once and puts them in the catalog; on failure (Codex not
// installed, cannot list) the catalog keeps the list it had. It reports whether the list may be out of date: Codex
// could not refresh it online (a rejected login, no network) and answered from its own cache.
func (a *App) readCodexModels(ctx context.Context, models *modelcatalog.Registry) bool {
	c, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	list, err := a.Executor.CodexModels(c)
	if err != nil {
		a.Log.Debug("could not read the models of the local Codex", "err", err)
		return false
	}
	models.SetLocalProviders(agentruntime.Codex, []modelcatalog.Provider{modelcatalog.CodexProvider(list.Models)})
	if list.Cached {
		a.Log.Info("Codex could not refresh its model list online and answered from its cache; it is read again at the next chance",
			"loginRejected", list.LoginRejected)
	}
	return list.Cached
}

// acpProviders build the catalog of each ACP runtime from the models its CLI lists.
var acpProviders = map[string]func([]modelcatalog.LocalModel) modelcatalog.Provider{
	agentruntime.Grok:        modelcatalog.GrokProvider,
	agentruntime.Antigravity: modelcatalog.AntigravityProvider,
}

// readACPModels reads the models the local CLI of an ACP runtime lists once and puts them in the catalog; on failure
// (not installed, not logged in) the catalog keeps the list it had. These CLIs do not say whether the list is out of
// date.
func (a *App) readACPModels(ctx context.Context, models *modelcatalog.Registry, runtime string) bool {
	c, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	list, err := a.Executor.ACPModels(c, runtime)
	if err != nil {
		a.Log.Debug("could not read the models of a local CLI", "runtime", runtime, "err", err)
		return false
	}
	models.SetLocalProviders(runtime, []modelcatalog.Provider{acpProviders[runtime](list)})
	return false
}

// localModelReader decides when a local CLI's model list (Codex's, Grok's, Antigravity's) is read again. Besides the reads at startup and on the timer, a read
// of the model lists (the console opening a model picker) starts one in the background when the list is older than
// maxAge, or than staleAge when the last read said it may be out of date; that read of the lists gets the list as it
// is, the next one the new list. Only one read of Codex's list runs at a time.
type localModelReader struct {
	maxAge, staleAge time.Duration
	now              func() time.Time
	// read reads the list into the catalog and reports whether it may be out of date.
	read func(ctx context.Context) (stale bool)

	readMu  sync.Mutex // one read at a time
	mu      sync.Mutex
	readAt  time.Time
	stale   bool
	running int
	wg      sync.WaitGroup
}

// refresh reads the list now and waits for it (startup, the timer, GET /runtimes?refresh=true); a read already running
// finishes first.
func (r *localModelReader) refresh(ctx context.Context) {
	r.mu.Lock()
	r.running++
	r.mu.Unlock()
	r.readMu.Lock()
	stale := r.read(ctx)
	r.readMu.Unlock()
	r.mu.Lock()
	r.running--
	r.readAt, r.stale = r.now(), stale
	r.mu.Unlock()
}

// touch is called when the model lists are read: it starts a background read (under ctx) when one is due and none is
// running, and returns at once.
func (r *localModelReader) touch(ctx context.Context) {
	r.mu.Lock()
	limit := r.maxAge
	if r.stale {
		limit = r.staleAge
	}
	if r.running > 0 || r.now().Sub(r.readAt) < limit || ctx.Err() != nil {
		r.mu.Unlock()
		return
	}
	r.running++ // counted now, so a touch right after does not start another
	r.wg.Add(1)
	r.mu.Unlock()
	go func() {
		defer r.wg.Done()
		r.refresh(ctx)
		r.mu.Lock()
		r.running--
		r.mu.Unlock()
	}()
}

// wait waits for the background reads (tests).
func (r *localModelReader) wait() { r.wg.Wait() }
