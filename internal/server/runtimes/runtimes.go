// Package runtimes reports whether the Agent runtimes are available on this machine (GET /runtimes): the bundled Pi is
// always available; for a CLI runtime (Claude Code, Codex) it checks whether the CLI is on the login shell's PATH,
// whether its version can be read and is supported, and the login state as answered by the CLI itself (the platform
// does not read its credentials, it only takes whether it is logged in, the login method and the subscription type).
package runtimes

import (
	"context"
	"sync"
	"time"

	"loop-now/internal/server/agentruntime"
	"loop-now/internal/server/claudecli"
)

// cacheFor is how long a result counts as fresh: a read after that still gets it at once and starts a check in the
// background; refresh checks again and waits.
const cacheFor = 30 * time.Second

// probeTimeout is the default time limit of one runtime's check (all its commands). A check that runs over reports
// what it found in time: a CLI whose version could not be read is version_failed, a login not told is unknown.
const probeTimeout = 10 * time.Second

// Reasons for being unavailable.
const (
	ReasonNotFound      = "not_found"
	ReasonVersionFailed = "version_failed"
	// ReasonVersionUnsupported: the CLI is older than the runtime's MinVersion.
	ReasonVersionUnsupported = "version_unsupported"
)

// Login states.
const (
	LoginLoggedIn  = "logged_in"
	LoginLoggedOut = "logged_out"
	LoginUnknown   = "unknown"
)

// Status corresponds to RuntimeStatus in the OpenAPI spec.
type Status struct {
	ID                string    `json:"id"`
	Available         bool      `json:"available"`
	Version           *string   `json:"version"`
	BinaryPath        *string   `json:"binaryPath"`
	UnavailableReason *string   `json:"unavailableReason"`
	Login             *Login    `json:"login"`
	CheckedAt         time.Time `json:"checkedAt"`
}

// Login is the login state reported by Claude Code itself; it has no email, organization or similar details.
type Login struct {
	State            string  `json:"state"`
	AuthMethod       *string `json:"authMethod"`
	SubscriptionType *string `json:"subscriptionType"`
}

// Checker checks each runtime. The zero value is not usable: PiVersion must be set; when UserPath or RealHome is nil,
// Claude Code is reported as not found.
type Checker struct {
	PiVersion    func() string
	UserPath     func(context.Context) []string
	RealHome     func() (string, error)
	ExecBasePath string
	// HelperEnv is the environment for running claude's own short commands (the same base variables the executor
	// gives the Agent's Claude Code process, including USER);
	// when nil, there are only HOME and PATH.
	HelperEnv func(ctx context.Context, home string) []string
	// ClaudeBin is the claude executable (for tests); when empty it is looked up in UserPath.
	ClaudeBin string
	// CodexEnv is the environment for running codex's own short commands (with CODEX_HOME); nil, or a nil result,
	// reports Codex as not found. CodexBin is the codex executable (for tests); when empty it is looked up in UserPath.
	CodexEnv func(ctx context.Context) []string
	CodexBin string
	// ACPEnv is the environment for running grok's and agy's own short commands; nil, or a nil result, reports them as
	// not found. GrokBin and AntigravityBin are their executables (for tests); when empty they are looked up in UserPath.
	ACPEnv                  func(ctx context.Context) []string
	GrokBin, AntigravityBin string
	// ACPLogin gives the last login conclusion for Grok or Antigravity (acpcli.LoggedIn, LoggedOut or Unknown; empty when
	// there is none yet), which the background read of their model lists keeps; the status then runs no command that
	// goes online. Nil runs grok models or agy models in the check.
	ACPLogin func(runtime string) string
	// ProbeTimeout replaces probeTimeout (for tests).
	ProbeTimeout time.Duration
	Now          func() time.Time

	probe    claudecli.Probe
	mu       sync.Mutex
	cached   []Status
	at       time.Time
	checking bool
}

// Forget drops the cached results, so the next List checks again (the runtimes changed).
func (c *Checker) Forget() {
	c.mu.Lock()
	c.cached = nil
	c.mu.Unlock()
}

func (c *Checker) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// List returns the status of each runtime. Once there is a result, a read without refresh returns it at once; when it
// is older than cacheFor, a check starts in the background (one at a time) and a later read gets its result. The first
// read and refresh check now and wait. The runtimes are checked at the same time, each within the time limit.
func (c *Checker) List(ctx context.Context, refresh bool) []Status {
	c.mu.Lock()
	if !refresh && c.cached != nil {
		out := append([]Status(nil), c.cached...)
		if c.now().Sub(c.at) >= cacheFor && !c.checking {
			c.checking = true
			go func() {
				c.store(c.check(context.Background()))
				c.mu.Lock()
				c.checking = false
				c.mu.Unlock()
			}()
		}
		c.mu.Unlock()
		return out
	}
	c.mu.Unlock()
	out := c.check(ctx)
	c.store(out)
	return append([]Status(nil), out...)
}

// store keeps a result unless a newer one is already kept.
func (c *Checker) store(out []Status) {
	if len(out) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if at := out[0].CheckedAt; c.cached == nil || !at.Before(c.at) {
		c.cached, c.at = out, at
	}
}

// check checks every runtime at the same time, each within the time limit, and returns them in registry order.
func (c *Checker) check(ctx context.Context) []Status {
	at := c.now().UTC()
	timeout := c.ProbeTimeout
	if timeout <= 0 {
		timeout = probeTimeout
	}
	all := agentruntime.All()
	results := make([]*Status, len(all))
	var wg sync.WaitGroup
	for i, rt := range all {
		var run func(context.Context) Status
		switch probe, ok := probes[rt.ID]; {
		case rt.Capabilities.Bundled && bundledVersions[rt.ID] != nil:
			version := bundledVersions[rt.ID](c)
			run = func(context.Context) Status { return c.bundled(rt.ID, version, at) }
		case ok && !rt.Capabilities.Bundled:
			run = func(ctx context.Context) Status { return probe(c, ctx, at) }
		case rt.Command != nil:
			run = func(ctx context.Context) Status { return c.localCommand(ctx, at, rt) }
		default:
			continue
		}
		wg.Go(func() {
			pctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			s := run(pctx)
			results[i] = &s
		})
	}
	wg.Wait()
	out := []Status{}
	for _, s := range results {
		if s != nil {
			out = append(out, *s)
		}
	}
	return out
}

// bundledVersions reads the version of each bundled runtime; a bundled runtime is always available.
var bundledVersions = map[string]func(*Checker) string{
	agentruntime.Pi: func(c *Checker) string {
		if c.PiVersion == nil {
			return ""
		}
		return c.PiVersion()
	},
}

// probes check each runtime that is not bundled: whether it is installed, its version and its login.
var probes = map[string]func(*Checker, context.Context, time.Time) Status{
	agentruntime.ClaudeCode: (*Checker).claude,
	agentruntime.Codex:      (*Checker).codex,
	agentruntime.Grok:       func(c *Checker, ctx context.Context, at time.Time) Status { return c.acp(ctx, at, agentruntime.Grok) },
	agentruntime.Antigravity: func(c *Checker, ctx context.Context, at time.Time) Status {
		return c.acp(ctx, at, agentruntime.Antigravity)
	},
}

func (c *Checker) bundled(id, version string, at time.Time) Status {
	s := Status{ID: id, Available: true, CheckedAt: at}
	if version != "" {
		s.Version = &version
	}
	return s
}
