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

// cacheFor is how long check results are cached; refresh checks again.
const cacheFor = 30 * time.Second

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
	Now                     func() time.Time

	probe  claudecli.Probe
	mu     sync.Mutex
	cached []Status
	at     time.Time
}

// Forget drops the cached results, so the next List checks again (the runtimes changed).
func (c *Checker) Forget() {
	c.mu.Lock()
	c.cached = nil
	c.mu.Unlock()
}

// List returns the status of each runtime; when refresh is false, results within the cache duration are reused.
func (c *Checker) List(ctx context.Context, refresh bool) []Status {
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	c.mu.Lock()
	if !refresh && c.cached != nil && now().Sub(c.at) < cacheFor {
		out := append([]Status(nil), c.cached...)
		c.mu.Unlock()
		return out
	}
	c.mu.Unlock()
	at := now().UTC()
	out := []Status{}
	for _, rt := range agentruntime.All() {
		if version, ok := bundledVersions[rt.ID]; ok && rt.Capabilities.Bundled {
			out = append(out, c.bundled(rt.ID, version(c), at))
		} else if probe, ok := probes[rt.ID]; ok && !rt.Capabilities.Bundled {
			out = append(out, probe(c, ctx, at))
		} else if rt.Command != nil {
			out = append(out, c.localCommand(ctx, at, rt))
		}
	}
	c.mu.Lock()
	c.cached, c.at = out, now()
	c.mu.Unlock()
	return append([]Status(nil), out...)
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
